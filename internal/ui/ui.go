// Package ui implements the embedded browser console and the USB4-bound peer
// agent. Both compose the same internal packages the CLI commands use; the
// HTTP layer never applies host changes, it only collects state, previews
// plans, and orchestrates the time-boxed benchmark listener.
package ui

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/bench"
	"github.com/ciru-ai/CiruStrixLink/internal/link"
	"github.com/ciru-ai/CiruStrixLink/internal/prereq"
	"github.com/ciru-ai/CiruStrixLink/internal/transport"
)

// SchemaVersion is the UI HTTP API schema version.
const SchemaVersion = 1

const (
	// DefaultConsolePort is the console's default HTTP port.
	DefaultConsolePort = 7749
	// DefaultAgentPort is the peer agent's default HTTP port.
	DefaultAgentPort = 7748

	peerTimeout      = 4 * time.Second
	activityCapacity = 200
	maxServeSeconds  = 120
	maxJSONBody      = 1 << 20
)

// HostInfo is the stable identity header of the composite host payload.
type HostInfo struct {
	Hostname        string `json:"hostname"`
	OSName          string `json:"os_name"`
	OSVersion       string `json:"os_version"`
	Kernel          string `json:"kernel"`
	Architecture    string `json:"architecture"`
	StrixHaloLikely bool   `json:"strix_halo_likely"`
	Supported       bool   `json:"supported"`
}

// HostPayload is the composite host document shared by the console's
// /api/host/local and the agent's /api/agent/host. Every collection section
// is best-effort: a failure lands in Errors and the rest is still served.
type HostPayload struct {
	CollectedAt   time.Time         `json:"collected_at"`
	CollectionMs  int64             `json:"collection_ms"`
	Host          HostInfo          `json:"host"`
	Privileged    bool              `json:"privileged"`
	Prerequisites *prereq.Report    `json:"prerequisites,omitempty"`
	Probe         *link.Probe       `json:"probe,omitempty"`
	Transport     *transport.Report `json:"transport,omitempty"`
	Errors        map[string]string `json:"errors"`
}

// TestReport mirrors the JSON shape of the CLI test report exactly.
type TestReport struct {
	Version       string       `json:"ciru_strixlink_version"`
	TimestampUTC  time.Time    `json:"timestamp_utc"`
	Hostname      string       `json:"hostname"`
	Interface     string       `json:"interface"`
	LocalIP       string       `json:"local_ip"`
	PeerIP        string       `json:"peer_ip"`
	Route         link.Route   `json:"route"`
	PathMTUPassed bool         `json:"path_mtu_passed"`
	PathMTUDetail string       `json:"path_mtu_detail"`
	Benchmark     bench.Result `json:"benchmark"`
}

// ActivityEntry records one UI-initiated action.
type ActivityEntry struct {
	Time    time.Time `json:"time"`
	Action  string    `json:"action"`
	Host    string    `json:"host"`
	Status  string    `json:"status"`
	Summary string    `json:"summary"`
	Detail  string    `json:"detail,omitempty"`
}

type activityLog struct {
	mu      sync.Mutex
	entries []ActivityEntry
}

func (l *activityLog) add(action, host, status, summary, detail string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := ActivityEntry{Time: time.Now().UTC(), Action: action, Host: host, Status: status, Summary: summary, Detail: detail}
	if len(l.entries) < activityCapacity {
		l.entries = append(l.entries, e)
		return
	}
	copy(l.entries, l.entries[1:])
	l.entries[activityCapacity-1] = e
}

// list returns entries newest first.
func (l *activityLog) list() []ActivityEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ActivityEntry, 0, len(l.entries))
	for i := len(l.entries) - 1; i >= 0; i-- {
		out = append(out, l.entries[i])
	}
	return out
}

// peerState is served instead of a peer host payload when the peer agent
// cannot produce one.
type peerState struct {
	State  string `json:"state"` // no_peer, unreachable, or no_agent
	Detail string `json:"detail,omitempty"`
}

// pairEnvelope carries the reconciled pair plus the two per-host transport
// reports it was built from. a_kind/b_kind record provenance: in live mode A
// is the console host and B is the peer agent; in files mode both are the
// loaded report files. When State is "unavailable" only Reason is set.
type pairEnvelope struct {
	State  string                `json:"state"` // ok or unavailable
	Reason string                `json:"reason,omitempty"`
	Pair   *transport.PairReport `json:"pair,omitempty"`
	A      *transport.Report     `json:"a,omitempty"`
	B      *transport.Report     `json:"b,omitempty"`
	AKind  string                `json:"a_kind,omitempty"` // local or file
	BKind  string                `json:"b_kind,omitempty"` // agent or file
}

type endpointPlanRequest struct {
	Action       string `json:"action"`
	Peer         string `json:"peer"`
	Name         string `json:"name"`
	Ring         int    `json:"ring"`
	ThrottlingNS int    `json:"throttling_ns"`
	Adopt        bool   `json:"adopt"`
}

// endpointOptions applies the same defaults the CLI flags use (see
// runEndpoint in cmd/ciru-strixlink/main.go) so HTTP previews match `transport
// endpoint prepare|cleanup` dry runs exactly.
func endpointOptions(version, iface string, req endpointPlanRequest) transport.EndpointOptions {
	o := transport.EndpointOptions{
		ToolVersion: version,
		Action:      req.Action,
		Peer:        req.Peer,
		Interface:   iface,
		Name:        req.Name,
		Ring:        req.Ring,
		Throttling:  req.ThrottlingNS,
		Adopt:       req.Adopt,
		StateDir:    "/run/ciru-strixlink-nhi",
		DeviceGroup: "render",
		Timeout:     60 * time.Second,
	}
	if o.Name == "" {
		o.Name = "ciru-nhi"
	}
	if o.Ring == 0 {
		o.Ring = transport.ExpectedRing
	}
	if o.Throttling == 0 {
		o.Throttling = transport.ExpectedThrottle
	}
	return o
}

type serveRequest struct {
	Port    int `json:"port"`
	Seconds int `json:"seconds"`
}

type serveResponse struct {
	Listening bool      `json:"listening"`
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	Seconds   int       `json:"seconds"`
	ExpiresAt time.Time `json:"expires_at"`
}

type errorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	_ = e.Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error, detail string) {
	writeJSON(w, status, errorBody{Error: err.Error(), Detail: detail})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), "")
		return false
	}
	return true
}

// bearerAuth requires the shared token when one is configured, matching the
// serve/test token mechanism.
func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			h := r.Header.Get("Authorization")
			provided := strings.TrimPrefix(h, "Bearer ")
			if provided == h || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				writeError(w, http.StatusUnauthorized, errors.New("a valid bearer token is required"), "send Authorization: Bearer <token>; the console sends it when started with the same --token-file")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func componentAvailable(r prereq.Report, id string) bool {
	for _, c := range r.Components {
		if c.ID == id {
			return c.Status == prereq.Available
		}
	}
	return false
}

// hostSupported reports whether this host can run the link at all: Linux on
// an AMD Strix Halo platform.
func hostSupported(toolVersion string) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	return componentAvailable(prereq.Check(toolVersion), "strix_halo")
}

// collectHost assembles the composite host payload. It never fails
// wholesale: prereq.Check and transport.Inspect are read-only inventories
// that always return, and a probe failure is captured in Errors.
func collectHost(toolVersion, iface, peer string) HostPayload {
	start := time.Now()
	p := HostPayload{Errors: map[string]string{}}
	pre := prereq.Check(toolVersion)
	p.Prerequisites = &pre
	halo := componentAvailable(pre, "strix_halo")
	probe, err := link.ProbeLocal()
	if err != nil {
		p.Errors["probe"] = err.Error()
	} else {
		p.Probe = &probe
		halo = probe.StrixHaloLikely
	}
	tr, err := inspectTransportForUI(toolVersion, iface, peer)
	if err != nil {
		p.Errors["transport_helper"] = err.Error()
	}
	p.Transport = &tr
	kernel := pre.System.Kernel
	if kernel == "" {
		kernel = tr.Kernel
	}
	p.Host = HostInfo{
		Hostname: pre.System.Hostname, OSName: pre.System.OSName, OSVersion: pre.System.OSVersion,
		Kernel: kernel, Architecture: pre.System.Architecture,
		StrixHaloLikely: halo, Supported: runtime.GOOS == "linux" && halo,
	}
	p.Privileged = tr.NHI.Status != "needs_privilege"
	p.CollectedAt = time.Now().UTC()
	p.CollectionMs = time.Since(start).Milliseconds()
	return p
}

// selectIPv4 mirrors the CLI's selectIP: resolve the requested (or single
// discovered) USB4NET interface and its first non-link-local IPv4 address.
func selectIPv4(requested string) (link.Interface, string, error) {
	i, err := link.SelectInterface(requested)
	if err != nil {
		return link.Interface{}, "", err
	}
	n, err := net.InterfaceByName(i.Name)
	if err != nil {
		return i, "", err
	}
	addrs, err := n.Addrs()
	if err != nil {
		return i, "", err
	}
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		if err == nil && ip.To4() != nil && !ip.IsLinkLocalUnicast() {
			return i, ip.String(), nil
		}
	}
	return i, "", fmt.Errorf("%s has no non-link-local IPv4 address; run setup on this peer", i.Name)
}

// clampServeSeconds enforces the agent's hard listener time-box.
func clampServeSeconds(seconds int) int {
	if seconds > maxServeSeconds {
		return maxServeSeconds
	}
	return seconds
}

func queryBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
