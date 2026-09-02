package bench

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestProtocolHeaderRoundTrip(t *testing.T) {
	want := header{Op: opDownload, DurationMs: 1234, BlockBytes: 65536, Payload: 99, Auth: auth("secret")}
	got, err := decodeHeader(encodeHeader(want))
	if err != nil || got != want {
		t.Fatalf("got %#v, %v", got, err)
	}
}

func TestLoopbackRun(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, l, "test-token")
	port := l.Addr().(*net.TCPAddr).Port
	r, err := Run(Config{LocalIP: "127.0.0.1", PeerIP: "127.0.0.1", Port: port, Streams: 2, Duration: 300 * time.Millisecond, RTTSamples: 10, ReconnectTests: 2, IntegrityBytes: 1 << 20, Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Integrity.UploadOK || !r.Integrity.DownloadOK || r.ReconnectPassed != 2 {
		t.Fatalf("got %#v", r)
	}
	if r.Upload.Gbps <= 0 || r.Download.Gbps <= 0 {
		t.Fatalf("got %#v", r)
	}
}
