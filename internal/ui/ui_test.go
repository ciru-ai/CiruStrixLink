package ui

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/transport"
)

func fixtureReport(host, local, peer string) transport.Report {
	return transport.Report{
		Hostname: host, Interface: "thunderbolt0", LocalAddress: local, Peer: peer,
		Portable:      transport.Mode{ID: "portable", Ready: true},
		NHI:           transport.Mode{ID: "nhi", Status: "available"},
		LocalArmReady: true,
		Fallback:      transport.Fallback{Mode: "portable", Available: true},
	}
}

func writeReport(t *testing.T, dir, name string, r transport.Report) string {
	t.Helper()
	r.SchemaVersion = transport.SchemaVersion
	r.GeneratedAt = time.Now().UTC()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newFilesConsole(t *testing.T, a, b transport.Report) *Console {
	t.Helper()
	dir := t.TempDir()
	c, err := NewConsole(ConsoleConfig{
		Version: "test",
		ReportA: writeReport(t, dir, "a.transport.json", a),
		ReportB: writeReport(t, dir, "b.transport.json", b),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConsoleListenDefaultAndRemoteOverride(t *testing.T) {
	c, err := NewConsole(ConsoleConfig{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if c.cfg.Addr != "127.0.0.1" {
		t.Fatalf("default listen address = %q, want loopback", c.cfg.Addr)
	}
	if got := c.URLs(); len(got) != 1 || got[0].Label != "loopback" || got[0].URL != "http://127.0.0.1:7749" {
		t.Fatalf("default URLs = %#v", got)
	}

	remote, err := NewConsole(ConsoleConfig{Version: "test", Addr: "0.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if remote.cfg.Addr != "0.0.0.0" {
		t.Fatalf("explicit listen address = %q", remote.cfg.Addr)
	}
	if got := remote.URLs(); len(got) == 0 || got[0].URL != "http://127.0.0.1:7749" {
		t.Fatalf("explicit remote URLs = %#v", got)
	}
}

func getJSON(t *testing.T, srv *httptest.Server, path string, wantStatus int) []byte {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status %d, want %d; body: %s", path, resp.StatusCode, wantStatus, b.String())
	}
	return []byte(b.String())
}

func postJSONStatus(t *testing.T, srv *httptest.Server, path, body string) ([]byte, int) {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return []byte(b.String()), resp.StatusCode
}

func postJSON(t *testing.T, srv *httptest.Server, path, body string, wantStatus int) []byte {
	t.Helper()
	b, status := postJSONStatus(t, srv, path, body)
	if status != wantStatus {
		t.Fatalf("POST %s: status %d, want %d; body: %s", path, status, wantStatus, b)
	}
	return b
}

func TestPairFromReportFiles(t *testing.T) {
	c := newFilesConsole(t,
		fixtureReport("ciru", "10.77.77.1", "10.77.77.2"),
		fixtureReport("sozo", "10.77.77.2", "10.77.77.1"),
	)
	srv := httptest.NewServer(c)
	defer srv.Close()

	assertEnvelope := func(env pairEnvelope, where string) {
		t.Helper()
		if env.State != "ok" || env.Reason != "" {
			t.Fatalf("%s: state=%s reason=%q", where, env.State, env.Reason)
		}
		if env.Pair == nil || !env.Pair.PairIdentityValid || !env.Pair.PortableReady || !env.Pair.ArmAllowed || env.Pair.NHIStatus != "arm_allowed" {
			t.Fatalf("%s: got pair %#v", where, env.Pair)
		}
		if env.A == nil || env.A.Hostname != "ciru" || env.B == nil || env.B.Hostname != "sozo" {
			t.Fatalf("%s: got a=%#v b=%#v", where, env.A, env.B)
		}
		if env.AKind != "file" || env.BKind != "file" {
			t.Fatalf("%s: got kinds %s/%s", where, env.AKind, env.BKind)
		}
	}

	var env pairEnvelope
	if err := json.Unmarshal(getJSON(t, srv, "/api/pair", http.StatusOK), &env); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(env, "/api/pair")

	var refresh struct {
		Pair pairEnvelope `json:"pair"`
	}
	if err := json.Unmarshal(postJSON(t, srv, "/api/refresh", `{}`, http.StatusOK), &refresh); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(refresh.Pair, "/api/refresh pair")

	var health healthPayload
	if err := json.Unmarshal(getJSON(t, srv, "/api/health", http.StatusOK), &health); err != nil {
		t.Fatal(err)
	}
	if health.Mode != "console" || health.PairSource != "files" || health.SchemaVersion != SchemaVersion || health.StartedAt.IsZero() {
		t.Fatalf("got %#v", health)
	}
}

func TestPairFromReportFilesRejectsNonReciprocal(t *testing.T) {
	bad := fixtureReport("sozo", "10.77.77.2", "10.77.77.9")
	c := newFilesConsole(t, fixtureReport("ciru", "10.77.77.1", "10.77.77.2"), bad)
	srv := httptest.NewServer(c)
	defer srv.Close()

	var env pairEnvelope
	if err := json.Unmarshal(getJSON(t, srv, "/api/pair", http.StatusOK), &env); err != nil {
		t.Fatal(err)
	}
	if env.State != "ok" || env.Pair == nil || env.Pair.PairIdentityValid || env.Pair.NHIStatus != "invalid_pair" {
		t.Fatalf("got %#v", env)
	}
	if env.A == nil || env.B == nil {
		t.Fatal("a non-reciprocal but loadable pair must still carry both host reports")
	}
}

func TestPairLiveUnavailableHasNoHalfEnvelope(t *testing.T) {
	// Live mode with an unreachable peer must serve state=unavailable with a
	// reason and no partial pair/a/b fields.
	c, err := NewConsole(ConsoleConfig{Version: "test", Peer: "127.0.0.1", AgentPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	defer srv.Close()
	var env pairEnvelope
	if err := json.Unmarshal(getJSON(t, srv, "/api/pair", http.StatusOK), &env); err != nil {
		t.Fatal(err)
	}
	if env.State != "unavailable" || env.Reason == "" {
		t.Fatalf("got %#v", env)
	}
	if env.Pair != nil || env.A != nil || env.B != nil || env.AKind != "" || env.BKind != "" {
		t.Fatalf("unavailable must not be half-populated: %#v", env)
	}
}

func TestPairRequiresBothReportFiles(t *testing.T) {
	dir := t.TempDir()
	a := writeReport(t, dir, "a.transport.json", fixtureReport("ciru", "10.77.77.1", "10.77.77.2"))
	if _, err := NewConsole(ConsoleConfig{Version: "test", ReportA: a}); err == nil {
		t.Fatal("expected an error when only one report file is given")
	}
	if _, err := NewConsole(ConsoleConfig{Version: "test", ReportA: a, ReportB: filepath.Join(dir, "missing.json")}); err == nil {
		t.Fatal("expected an error when a report file cannot be loaded")
	}
}

func TestEndpointPlanGatedByPairIdentity(t *testing.T) {
	c := newFilesConsole(t,
		fixtureReport("ciru", "10.77.77.1", "10.77.77.2"),
		fixtureReport("sozo", "10.77.77.2", "10.77.77.1"),
	)
	srv := httptest.NewServer(c)
	defer srv.Close()

	b, status := postJSONStatus(t, srv, "/api/endpoint/plan", `{"action":"prepare","peer":"10.77.77.2"}`)
	if status == http.StatusConflict {
		t.Fatalf("a valid pair must not disable the preview: %s", b)
	}
	if runtime.GOOS == "linux" {
		// Endpoint plans are Linux-only; on other hosts the plan builder
		// rejects the /run state directory and the handler answers 400.
		if status != http.StatusOK {
			t.Fatalf("status %d; body: %s", status, b)
		}
		var resp endpointPlanResponse
		if err := json.Unmarshal(b, &resp); err != nil {
			t.Fatal(err)
		}
		if !resp.PairIdentityValid || resp.Local.Action != "prepare" || resp.Peer != nil {
			t.Fatalf("got %#v", resp)
		}
	}

	bad := newFilesConsole(t, fixtureReport("ciru", "10.77.77.1", "10.77.77.2"), fixtureReport("sozo", "10.77.77.2", "10.77.77.9"))
	badSrv := httptest.NewServer(bad)
	defer badSrv.Close()
	var errBody errorBody
	if err := json.Unmarshal(postJSON(t, badSrv, "/api/endpoint/plan", `{"action":"prepare","peer":"10.77.77.2"}`, http.StatusConflict), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error == "" {
		t.Fatalf("expected a conflict error, got %#v", errBody)
	}
}

func TestEnvFromReportFilePair(t *testing.T) {
	c := newFilesConsole(t,
		fixtureReport("ciru", "10.77.77.1", "10.77.77.2"),
		fixtureReport("sozo", "10.77.77.2", "10.77.77.1"),
	)
	srv := httptest.NewServer(c)
	defer srv.Close()

	var resp envResponse
	if err := json.Unmarshal(postJSON(t, srv, "/api/env", `{"mode":"auto","runtime":"generic"}`, http.StatusOK), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Environment.Mode != "portable" || resp.Environment.Variables["NCCL_SOCKET_IFNAME"] != "=thunderbolt0" {
		t.Fatalf("got %#v", resp.Environment)
	}
	if !strings.Contains(resp.DotEnv, "CIRU_STRIXLINK_MODE=portable") {
		t.Fatalf("dotenv missing mode line:\n%s", resp.DotEnv)
	}
}

func TestCollectHostToleratesUnsupportedHost(t *testing.T) {
	p := collectHost("test", "auto", "")
	if p.CollectedAt.IsZero() || p.Host.Hostname == "" {
		t.Fatalf("got %#v", p)
	}
	if p.Errors == nil {
		t.Fatal("errors map must always be initialized")
	}
	if p.Prerequisites == nil || p.Transport == nil {
		t.Fatal("prerequisites and transport sections must always be present")
	}
	if p.Transport.NHI.Status == "needs_privilege" && p.Privileged {
		t.Fatal("privileged must be false when NHI state needs privilege")
	}
	if runtime.GOOS != "linux" {
		if p.Host.Supported {
			t.Fatal("a non-Linux host must report supported=false")
		}
		if p.Errors["probe"] == "" {
			t.Fatal("the probe failure must be captured in errors")
		}
		if p.Probe != nil {
			t.Fatal("probe must be omitted when it fails")
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestAgentServeTimeBoxesListener(t *testing.T) {
	a := NewAgent(AgentConfig{Version: "test", Interface: "auto", LocalIP: "127.0.0.1"})
	srv := httptest.NewServer(a)
	defer srv.Close()

	post := func(port, seconds int, want int) serveResponse {
		t.Helper()
		var out serveResponse
		b := postJSON(t, srv, "/api/agent/serve", fmt.Sprintf(`{"port":%d,"seconds":%d}`, port, seconds), want)
		if want == http.StatusOK {
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}

	first := post(freePort(t), 1, http.StatusOK)
	if !first.Listening || first.Address != "127.0.0.1" || first.Seconds != 1 || first.ExpiresAt.IsZero() {
		t.Fatalf("got %#v", first)
	}
	post(freePort(t), 1, http.StatusConflict) // a second listener cannot stack
	time.Sleep(1500 * time.Millisecond)       // the time box must self-terminate
	post(freePort(t), 1, http.StatusOK)
}

func TestAgentServeClampsHardCap(t *testing.T) {
	if got := clampServeSeconds(1000); got != maxServeSeconds {
		t.Fatalf("clampServeSeconds(1000) = %d", got)
	}
	if got := clampServeSeconds(5); got != 5 {
		t.Fatalf("clampServeSeconds(5) = %d", got)
	}
	a := NewAgent(AgentConfig{Version: "test", Interface: "auto", LocalIP: "127.0.0.1"})
	srv := httptest.NewServer(a)
	defer srv.Close()
	var resp serveResponse
	if err := json.Unmarshal(postJSON(t, srv, "/api/agent/serve", fmt.Sprintf(`{"port":%d,"seconds":1000}`, freePort(t)), http.StatusOK), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Seconds != maxServeSeconds {
		t.Fatalf("got %d seconds, want the %d hard cap", resp.Seconds, maxServeSeconds)
	}
}

func TestAgentRequiresTokenWhenSet(t *testing.T) {
	a := NewAgent(AgentConfig{Version: "test", Interface: "auto", LocalIP: "127.0.0.1", Token: "secret"})
	srv := httptest.NewServer(a)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/agent/host")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/agent/host", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d, want 401", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/agent/host", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct token: status %d, want 200", resp.StatusCode)
	}
	var p HostPayload
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.CollectedAt.IsZero() {
		t.Fatalf("got %#v", p)
	}
}

func splitHostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil {
		t.Fatal(err)
	}
	return host, p
}

func TestHostPeerStates(t *testing.T) {
	// no_peer: console started without --peer
	c, err := NewConsole(ConsoleConfig{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	defer srv.Close()
	var state peerState
	if err := json.Unmarshal(getJSON(t, srv, "/api/host/peer", http.StatusOK), &state); err != nil {
		t.Fatal(err)
	}
	if state.State != "no_peer" {
		t.Fatalf("got %#v", state)
	}

	// unreachable: nothing listens on the peer port
	c2, err := NewConsole(ConsoleConfig{Version: "test", Peer: "127.0.0.1", AgentPort: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(c2)
	defer srv2.Close()
	state = peerState{}
	if err := json.Unmarshal(getJSON(t, srv2, "/api/host/peer", http.StatusOK), &state); err != nil {
		t.Fatal(err)
	}
	if state.State != "unreachable" || state.Detail == "" {
		t.Fatalf("got %#v", state)
	}

	// no_agent: the peer answers HTTP but has no agent endpoints
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	host, port := splitHostPort(t, plain.URL)
	c3, err := NewConsole(ConsoleConfig{Version: "test", Peer: host, AgentPort: port})
	if err != nil {
		t.Fatal(err)
	}
	srv3 := httptest.NewServer(c3)
	defer srv3.Close()
	state = peerState{}
	if err := json.Unmarshal(getJSON(t, srv3, "/api/host/peer", http.StatusOK), &state); err != nil {
		t.Fatal(err)
	}
	if state.State != "no_agent" {
		t.Fatalf("got %#v", state)
	}

	// ok: a real agent answers
	agent := httptest.NewServer(NewAgent(AgentConfig{Version: "test", Interface: "auto", LocalIP: "127.0.0.1"}))
	defer agent.Close()
	host, port = splitHostPort(t, agent.URL)
	c4, err := NewConsole(ConsoleConfig{Version: "test", Peer: host, AgentPort: port})
	if err != nil {
		t.Fatal(err)
	}
	srv4 := httptest.NewServer(c4)
	defer srv4.Close()
	var payload HostPayload
	if err := json.Unmarshal(getJSON(t, srv4, "/api/host/peer", http.StatusOK), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.CollectedAt.IsZero() || payload.Host.Hostname == "" {
		t.Fatalf("got %#v", payload)
	}
}

func TestSPAServedAndAPIUnknown404(t *testing.T) {
	c, err := NewConsole(ConsoleConfig{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	defer srv.Close()

	for _, path := range []string{"/", "/pair", "/setup/wizard"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body := make([]byte, 64<<10)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") || n == 0 {
			t.Fatalf("GET %s: status %d content-type %s bytes %d", path, resp.StatusCode, resp.Header.Get("Content-Type"), n)
		}
	}

	var errBody errorBody
	if err := json.Unmarshal(getJSON(t, srv, "/api/nope", http.StatusNotFound), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error == "" {
		t.Fatalf("got %#v", errBody)
	}
}

func TestSetupPlanRequiresRole(t *testing.T) {
	c, err := NewConsole(ConsoleConfig{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	defer srv.Close()
	var errBody errorBody
	if err := json.Unmarshal(postJSON(t, srv, "/api/setup/plan", `{}`, http.StatusBadRequest), &errBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBody.Error, "role") {
		t.Fatalf("got %#v", errBody)
	}
}

func TestActivityLogRing(t *testing.T) {
	var l activityLog
	for i := 0; i < 250; i++ {
		l.add("action", "host", "ok", fmt.Sprint(i), "")
	}
	got := l.list()
	if len(got) != activityCapacity {
		t.Fatalf("len = %d, want %d", len(got), activityCapacity)
	}
	if got[0].Summary != "249" || got[len(got)-1].Summary != "50" {
		t.Fatalf("newest-first ring order broken: first=%s last=%s", got[0].Summary, got[len(got)-1].Summary)
	}
	if got[0].Time.IsZero() {
		t.Fatal("entries must carry a UTC timestamp")
	}
}
