// Package bench implements a dependency-free USB4 transport test protocol.
package bench

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/quality"
)

const (
	DefaultPort     = 55321
	protocolVersion = uint16(1)
	headerBytes     = 48

	opPing              = uint16(1)
	opUpload            = uint16(2)
	opDownload          = uint16(3)
	opIntegrityUpload   = uint16(4)
	opIntegrityDownload = uint16(5)
)

var magic = [8]byte{'S', 'T', 'R', 'X', 'L', 'N', 'K', '1'}

type header struct {
	Op         uint16
	DurationMs uint32
	BlockBytes uint32
	Payload    uint64
	Auth       [16]byte
}

type RTT struct {
	Samples int     `json:"samples"`
	MinMs   float64 `json:"min_ms"`
	P50Ms   float64 `json:"p50_ms"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
	MaxMs   float64 `json:"max_ms"`
}

type Direction struct {
	Gbps    float64 `json:"gbps"`
	Bytes   uint64  `json:"bytes"`
	Seconds float64 `json:"seconds"`
	Streams int     `json:"streams"`
}

type Integrity struct {
	UploadOK   bool   `json:"upload_ok"`
	DownloadOK bool   `json:"download_ok"`
	BytesEach  uint64 `json:"bytes_each"`
}

type Result struct {
	RTT             RTT            `json:"rtt"`
	ReconnectPassed int            `json:"reconnect_passed"`
	ReconnectTotal  int            `json:"reconnect_total"`
	Integrity       Integrity      `json:"integrity"`
	Upload          Direction      `json:"local_to_peer"`
	Download        Direction      `json:"peer_to_local"`
	AsymmetryRatio  float64        `json:"asymmetry_ratio"`
	FasterSender    string         `json:"faster_sender"`
	Policy          quality.Policy `json:"policy"`
}

type Config struct {
	LocalIP        string
	PeerIP         string
	Port           int
	Streams        int
	Duration       time.Duration
	RTTSamples     int
	ReconnectTests int
	IntegrityBytes uint64
	Token          string
}

func (c *Config) defaults() {
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.Streams == 0 {
		c.Streams = 4
	}
	if c.Duration == 0 {
		c.Duration = 5 * time.Second
	}
	if c.RTTSamples == 0 {
		c.RTTSamples = 100
	}
	if c.ReconnectTests == 0 {
		c.ReconnectTests = 5
	}
	if c.IntegrityBytes == 0 {
		c.IntegrityBytes = 8 << 20
	}
}

func auth(token string) [16]byte {
	if token == "" {
		return [16]byte{}
	}
	s := sha256.Sum256([]byte(token))
	var out [16]byte
	copy(out[:], s[:16])
	return out
}

func encodeHeader(h header) []byte {
	b := make([]byte, headerBytes)
	copy(b[:8], magic[:])
	binary.BigEndian.PutUint16(b[8:10], protocolVersion)
	binary.BigEndian.PutUint16(b[10:12], h.Op)
	binary.BigEndian.PutUint32(b[12:16], h.DurationMs)
	binary.BigEndian.PutUint32(b[16:20], h.BlockBytes)
	binary.BigEndian.PutUint64(b[20:28], h.Payload)
	copy(b[28:44], h.Auth[:])
	return b
}

func decodeHeader(b []byte) (header, error) {
	if len(b) != headerBytes || string(b[:8]) != string(magic[:]) {
		return header{}, errors.New("not a CiruStrixLink test connection")
	}
	if binary.BigEndian.Uint16(b[8:10]) != protocolVersion {
		return header{}, errors.New("incompatible CiruStrixLink test protocol")
	}
	var h header
	h.Op = binary.BigEndian.Uint16(b[10:12])
	h.DurationMs = binary.BigEndian.Uint32(b[12:16])
	h.BlockBytes = binary.BigEndian.Uint32(b[16:20])
	h.Payload = binary.BigEndian.Uint64(b[20:28])
	copy(h.Auth[:], b[28:44])
	return h, nil
}

func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

func setBuffers(c *net.TCPConn) {
	_ = c.SetReadBuffer(4 << 20)
	_ = c.SetWriteBuffer(4 << 20)
	_ = c.SetNoDelay(true)
}

// Serve accepts test connections until the listener is closed or ctx ends.
func Serve(ctx context.Context, l net.Listener, token string) error {
	wantAuth := auth(token)
	go func() { <-ctx.Done(); _ = l.Close() }()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go handle(c, wantAuth)
	}
}

func handle(raw net.Conn, wantAuth [16]byte) {
	defer raw.Close()
	c, ok := raw.(*net.TCPConn)
	if !ok {
		return
	}
	setBuffers(c)
	_ = c.SetDeadline(time.Now().Add(2 * time.Minute))
	b := make([]byte, headerBytes)
	if _, err := io.ReadFull(c, b); err != nil {
		return
	}
	h, err := decodeHeader(b)
	if err != nil || h.Auth != wantAuth || h.DurationMs > uint32((10*time.Minute).Milliseconds()) || h.Payload > 1<<30 {
		_, _ = c.Write([]byte{1})
		return
	}
	opTimeout := 2 * time.Minute
	if candidate := time.Duration(h.DurationMs)*time.Millisecond + 30*time.Second; candidate > opTimeout {
		opTimeout = candidate
	}
	_ = c.SetDeadline(time.Now().Add(opTimeout))
	if _, err := c.Write([]byte{0}); err != nil {
		return
	}

	switch h.Op {
	case opPing:
		pingServer(c, int(h.Payload))
	case opUpload:
		if !readGo(c) {
			return
		}
		buf := make([]byte, blockSize(h.BlockBytes))
		n, _ := io.CopyBuffer(io.Discard, c, buf)
		var reply [8]byte
		binary.BigEndian.PutUint64(reply[:], uint64(n))
		_ = writeFull(c, reply[:])
	case opDownload:
		if !readGo(c) {
			return
		}
		throughputSend(c, time.Duration(h.DurationMs)*time.Millisecond, blockSize(h.BlockBytes))
		_ = c.CloseWrite()
	case opIntegrityUpload:
		if !readGo(c) {
			return
		}
		integrityReceive(c, h.Payload, blockSize(h.BlockBytes))
	case opIntegrityDownload:
		if !readGo(c) {
			return
		}
		integritySend(c, h.Payload, blockSize(h.BlockBytes))
	}
}

func blockSize(n uint32) int {
	if n < 4<<10 || n > 4<<20 {
		return 256 << 10
	}
	return int(n)
}

func readGo(c net.Conn) bool {
	var b [1]byte
	_, err := io.ReadFull(c, b[:])
	return err == nil && b[0] == 1
}

func pingServer(c net.Conn, count int) {
	if count < 1 || count > 10000 {
		return
	}
	var b [8]byte
	for i := 0; i < count; i++ {
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return
		}
		if err := writeFull(c, b[:]); err != nil {
			return
		}
	}
}

func throughputSend(c net.Conn, d time.Duration, size int) {
	buf := pattern(size)
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if err := writeFull(c, buf); err != nil {
			return
		}
	}
}

func pattern(size int) []byte {
	b := make([]byte, size)
	var x uint32 = 0x6d2b79f5
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

func integrityReceive(c net.Conn, total uint64, size int) {
	h := crc32.NewIEEE()
	n, err := io.CopyBuffer(h, io.LimitReader(c, int64(total)), make([]byte, size))
	if err != nil {
		return
	}
	var reply [12]byte
	binary.BigEndian.PutUint64(reply[:8], uint64(n))
	binary.BigEndian.PutUint32(reply[8:], h.Sum32())
	_ = writeFull(c, reply[:])
}

func integritySend(c net.Conn, total uint64, size int) {
	buf := pattern(size)
	h := crc32.NewIEEE()
	var sent uint64
	for sent < total {
		n := uint64(len(buf))
		if total-sent < n {
			n = total - sent
		}
		part := buf[:n]
		if err := writeFull(c, part); err != nil {
			return
		}
		_, _ = h.Write(part)
		sent += n
	}
	var reply [12]byte
	binary.BigEndian.PutUint64(reply[:8], sent)
	binary.BigEndian.PutUint32(reply[8:], h.Sum32())
	_ = writeFull(c, reply[:])
}

type client struct{ cfg Config }

func (cl client) dial(op uint16, duration time.Duration, payload uint64) (*net.TCPConn, error) {
	local := net.ParseIP(cl.cfg.LocalIP)
	if local == nil {
		return nil, fmt.Errorf("invalid local IP %q", cl.cfg.LocalIP)
	}
	d := net.Dialer{Timeout: 5 * time.Second, LocalAddr: &net.TCPAddr{IP: local}}
	raw, err := d.Dial("tcp4", net.JoinHostPort(cl.cfg.PeerIP, fmt.Sprint(cl.cfg.Port)))
	if err != nil {
		return nil, err
	}
	c := raw.(*net.TCPConn)
	setBuffers(c)
	_ = c.SetDeadline(time.Now().Add(duration + 30*time.Second))
	h := header{Op: op, DurationMs: uint32(duration.Milliseconds()), BlockBytes: 256 << 10, Payload: payload, Auth: auth(cl.cfg.Token)}
	if err := writeFull(c, encodeHeader(h)); err != nil {
		c.Close()
		return nil, err
	}
	var status [1]byte
	if _, err := io.ReadFull(c, status[:]); err != nil {
		c.Close()
		return nil, err
	}
	if status[0] != 0 {
		c.Close()
		return nil, errors.New("test agent rejected connection (token or protocol mismatch)")
	}
	return c, nil
}

func (cl client) pings(samples int) ([]float64, error) {
	c, err := cl.dial(opPing, 5*time.Second, uint64(samples))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	values := make([]float64, 0, samples)
	var b [8]byte
	for i := 0; i < samples; i++ {
		binary.BigEndian.PutUint64(b[:], uint64(i))
		start := time.Now()
		if err := writeFull(c, b[:]); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return nil, err
		}
		values = append(values, float64(time.Since(start).Nanoseconds())/1e6)
	}
	return values, nil
}

func stats(v []float64) RTT {
	sort.Float64s(v)
	pick := func(q float64) float64 {
		if len(v) == 0 {
			return 0
		}
		i := int(math.Ceil(q*float64(len(v)))) - 1
		if i < 0 {
			i = 0
		}
		if i >= len(v) {
			i = len(v) - 1
		}
		return v[i]
	}
	return RTT{Samples: len(v), MinMs: pick(0), P50Ms: pick(.50), P95Ms: pick(.95), P99Ms: pick(.99), MaxMs: pick(1)}
}

func (cl client) reconnect(n int) int {
	passed := 0
	for i := 0; i < n; i++ {
		if _, err := cl.pings(1); err == nil {
			passed++
		}
	}
	return passed
}

func (cl client) integrity(op uint16, total uint64) (bool, error) {
	c, err := cl.dial(op, 10*time.Second, total)
	if err != nil {
		return false, err
	}
	defer c.Close()
	if err := writeFull(c, []byte{1}); err != nil {
		return false, err
	}
	buf := pattern(256 << 10)
	h := crc32.NewIEEE()
	if op == opIntegrityUpload {
		var sent uint64
		for sent < total {
			n := uint64(len(buf))
			if total-sent < n {
				n = total - sent
			}
			if err := writeFull(c, buf[:n]); err != nil {
				return false, err
			}
			_, _ = h.Write(buf[:n])
			sent += n
		}
	} else {
		remaining := total
		for remaining > 0 {
			n := uint64(len(buf))
			if remaining < n {
				n = remaining
			}
			if _, err := io.ReadFull(c, buf[:n]); err != nil {
				return false, err
			}
			_, _ = h.Write(buf[:n])
			remaining -= n
		}
	}
	var reply [12]byte
	if _, err := io.ReadFull(c, reply[:]); err != nil {
		return false, err
	}
	return binary.BigEndian.Uint64(reply[:8]) == total && binary.BigEndian.Uint32(reply[8:]) == h.Sum32(), nil
}

func (cl client) throughput(op uint16) (Direction, error) {
	conns := make([]*net.TCPConn, 0, cl.cfg.Streams)
	for i := 0; i < cl.cfg.Streams; i++ {
		c, err := cl.dial(op, cl.cfg.Duration, 0)
		if err != nil {
			for _, opened := range conns {
				opened.Close()
			}
			return Direction{}, err
		}
		conns = append(conns, c)
	}
	start := time.Now()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var total uint64
	var firstErr error
	for _, c := range conns {
		wg.Add(1)
		go func(c *net.TCPConn) {
			defer wg.Done()
			defer c.Close()
			if err := writeFull(c, []byte{1}); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			var n uint64
			if op == opUpload {
				buf := pattern(256 << 10)
				deadline := start.Add(cl.cfg.Duration)
				for time.Now().Before(deadline) {
					if err := writeFull(c, buf); err != nil {
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
						return
					}
					n += uint64(len(buf))
				}
				_ = c.CloseWrite()
				var reply [8]byte
				if _, err := io.ReadFull(c, reply[:]); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				n = binary.BigEndian.Uint64(reply[:])
			} else {
				read, err := io.CopyBuffer(io.Discard, c, make([]byte, 256<<10))
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				n = uint64(read)
			}
			mu.Lock()
			total += n
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	if firstErr != nil {
		return Direction{}, firstErr
	}
	return Direction{Gbps: float64(total) * 8 / elapsed / 1e9, Bytes: total, Seconds: elapsed, Streams: cl.cfg.Streams}, nil
}

// Run executes reconnect, RTT, integrity, and isolated directional throughput tests.
func Run(cfg Config) (Result, error) {
	cfg.defaults()
	if cfg.Streams < 1 || cfg.Streams > 32 {
		return Result{}, errors.New("streams must be between 1 and 32")
	}
	if cfg.Duration < 250*time.Millisecond || cfg.Duration > 10*time.Minute {
		return Result{}, errors.New("duration must be between 250ms and 10m")
	}
	cl := client{cfg: cfg}
	r := Result{ReconnectTotal: cfg.ReconnectTests, Integrity: Integrity{BytesEach: cfg.IntegrityBytes}}
	r.ReconnectPassed = cl.reconnect(cfg.ReconnectTests)
	values, err := cl.pings(cfg.RTTSamples)
	if err != nil {
		return r, fmt.Errorf("RTT test: %w", err)
	}
	r.RTT = stats(values)
	if r.Integrity.UploadOK, err = cl.integrity(opIntegrityUpload, cfg.IntegrityBytes); err != nil {
		return r, fmt.Errorf("upload integrity: %w", err)
	}
	if r.Integrity.DownloadOK, err = cl.integrity(opIntegrityDownload, cfg.IntegrityBytes); err != nil {
		return r, fmt.Errorf("download integrity: %w", err)
	}
	if r.Upload, err = cl.throughput(opUpload); err != nil {
		return r, fmt.Errorf("local-to-peer throughput: %w", err)
	}
	if r.Download, err = cl.throughput(opDownload); err != nil {
		return r, fmt.Errorf("peer-to-local throughput: %w", err)
	}
	lo, hi := math.Min(r.Upload.Gbps, r.Download.Gbps), math.Max(r.Upload.Gbps, r.Download.Gbps)
	if lo > 0 {
		r.AsymmetryRatio = hi / lo
	}
	if r.Upload.Gbps >= r.Download.Gbps {
		r.FasterSender = "local"
	} else {
		r.FasterSender = "peer"
	}
	r.Policy = quality.Classify(quality.Metrics{UploadGbps: r.Upload.Gbps, DownloadGbps: r.Download.Gbps, RTTP99Ms: r.RTT.P99Ms, ReconnectPassed: r.ReconnectPassed, ReconnectTotal: r.ReconnectTotal, IntegrityUpload: r.Integrity.UploadOK, IntegrityDown: r.Integrity.DownloadOK})
	return r, nil
}
