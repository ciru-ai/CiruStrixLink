package ui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/bench"
	"github.com/ciru-ai/CiruStrixLink/internal/install"
	"github.com/ciru-ai/CiruStrixLink/internal/link"
	"github.com/ciru-ai/CiruStrixLink/internal/transport"
)

//go:embed static
var staticFS embed.FS

// ConsoleConfig configures the browser console server.
type ConsoleConfig struct {
	Version   string
	Addr      string
	Port      int
	Peer      string // peer USB4 address; empty disables live peer collection
	AgentPort int
	Token     string
	ReportA   string // transport report file for endpoint A (requires ReportB)
	ReportB   string // transport report file for endpoint B (requires ReportA)
	ModelURL  string // optional read-only OpenAI-compatible model frontend
}

// ConsoleURL is one address the console is reachable at.
type ConsoleURL struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type peerConfigInfo struct {
	Configured bool   `json:"configured"`
	Address    string `json:"address,omitempty"`
	AgentPort  int    `json:"agent_port,omitempty"`
	TokenSet   bool   `json:"token_configured"`
}

type healthPayload struct {
	Version       string         `json:"version"`
	SchemaVersion int            `json:"schema_version"`
	Mode          string         `json:"mode"`
	Hostname      string         `json:"hostname"`
	OS            string         `json:"os"`
	Supported     bool           `json:"supported"`
	StartedAt     time.Time      `json:"started_at"`
	Peer          peerConfigInfo `json:"peer"`
	PairSource    string         `json:"pair_source"`
}

type endpointPlanResponse struct {
	PairIdentityValid bool                    `json:"pair_identity_valid"`
	Local             transport.EndpointPlan  `json:"local"`
	Peer              *transport.EndpointPlan `json:"peer"`
	PeerState         string                  `json:"peer_state,omitempty"`
	PeerError         string                  `json:"peer_error,omitempty"`
}

type benchRequest struct {
	DurationS  float64 `json:"duration_s"`
	Streams    int     `json:"streams"`
	RTTSamples int     `json:"rtt_samples"`
	Port       int     `json:"port"`
}

type envRequest struct {
	Mode    string `json:"mode"`
	Runtime string `json:"runtime"`
}

type envResponse struct {
	Environment transport.Environment `json:"environment"`
	DotEnv      string                `json:"dotenv"`
}

// Console is the browser console server. It is an http.Handler.
type Console struct {
	cfg       ConsoleConfig
	startedAt time.Time
	hostname  string
	supported bool
	urls      []ConsoleURL
	collectMu sync.Mutex
	benchMu   sync.Mutex
	activity  activityLog
	client    *http.Client
	mux       *http.ServeMux
	static    fs.FS
	indexHTML []byte
	model     *modelMonitor
}

// NewConsole builds the console. In report-file pair mode both reports are
// validated up front so a bad path fails at startup.
func NewConsole(cfg ConsoleConfig) (*Console, error) {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultConsolePort
	}
	if cfg.AgentPort == 0 {
		cfg.AgentPort = DefaultAgentPort
	}
	if (cfg.ReportA == "") != (cfg.ReportB == "") {
		return nil, errors.New("--report-a and --report-b must be provided together")
	}
	if cfg.ReportA != "" {
		if _, err := transport.LoadReport(cfg.ReportA); err != nil {
			return nil, fmt.Errorf("load endpoint A report: %w", err)
		}
		if _, err := transport.LoadReport(cfg.ReportB); err != nil {
			return nil, fmt.Errorf("load endpoint B report: %w", err)
		}
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("embedded static assets: %w", err)
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("embedded index.html: %w", err)
	}
	c := &Console{
		cfg: cfg, startedAt: time.Now().UTC(), supported: hostSupported(cfg.Version),
		client: &http.Client{Timeout: peerTimeout}, static: sub, indexHTML: index,
	}
	c.hostname, _ = os.Hostname()
	c.model, err = newModelMonitor(cfg.ModelURL, os.Getenv("CIRU_STRIXLINK_MODEL_TOKEN"), c.hostname)
	if err != nil {
		return nil, err
	}
	c.urls = consoleURLs(cfg.Addr, cfg.Port)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", c.handleHealth)
	mux.HandleFunc("GET /api/model", c.handleModel)
	mux.HandleFunc("GET /api/host/local", c.handleHostLocal)
	mux.HandleFunc("GET /api/host/peer", c.handleHostPeer)
	mux.HandleFunc("POST /api/refresh", c.handleRefresh)
	mux.HandleFunc("GET /api/pair", c.handlePair)
	mux.HandleFunc("GET /api/install/plan", c.handleInstallPlan)
	mux.HandleFunc("POST /api/setup/plan", c.handleSetupPlan)
	mux.HandleFunc("POST /api/rollback/plan", c.handleRollbackPlan)
	mux.HandleFunc("POST /api/doctor", c.handleDoctor)
	mux.HandleFunc("POST /api/endpoint/plan", c.handleEndpointPlan)
	mux.HandleFunc("POST /api/bench", c.handleBench)
	mux.HandleFunc("POST /api/env", c.handleEnv)
	mux.HandleFunc("GET /api/activity", c.handleActivity)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown API path %s", r.URL.Path), "")
	})
	mux.Handle("/", c.spaHandler())
	c.mux = mux
	return c, nil
}

// ServeHTTP implements http.Handler.
func (c *Console) ServeHTTP(w http.ResponseWriter, r *http.Request) { c.mux.ServeHTTP(w, r) }

// URLs lists every address the console is reachable at, loopback first.
func (c *Console) URLs() []ConsoleURL { return append([]ConsoleURL(nil), c.urls...) }

func (c *Console) filesMode() bool { return c.cfg.ReportA != "" }

// consoleURLs enumerates loopback, every non-loopback LAN IPv4, and the USB4
// interface address (when present) as full URLs.
func consoleURLs(addr string, port int) []ConsoleURL {
	if addr == "127.0.0.1" {
		return []ConsoleURL{{Label: "loopback", URL: fmt.Sprintf("http://127.0.0.1:%d", port)}}
	}
	if addr != "" && addr != "0.0.0.0" && addr != "::" {
		return []ConsoleURL{{Label: "console", URL: fmt.Sprintf("http://%s:%d", addr, port)}}
	}
	urls := []ConsoleURL{{Label: "loopback", URL: fmt.Sprintf("http://127.0.0.1:%d", port)}}
	seen := map[string]bool{"127.0.0.1": true}
	var usb4 []ConsoleURL
	if ifs, err := link.Discover(); err == nil {
		for _, i := range ifs {
			ip := transport.InterfaceIPv4(i.Name)
			if ip != "" && !seen[ip] {
				seen[ip] = true
				usb4 = append(usb4, ConsoleURL{Label: "usb4[" + i.Name + "]", URL: fmt.Sprintf("http://%s:%d", ip, port)})
			}
		}
	}
	if ifs, err := net.Interfaces(); err == nil {
		for _, n := range ifs {
			addrs, _ := n.Addrs()
			for _, a := range addrs {
				ip, _, err := net.ParseCIDR(a.String())
				if err != nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || seen[ip.String()] {
					continue
				}
				seen[ip.String()] = true
				urls = append(urls, ConsoleURL{Label: "lan", URL: fmt.Sprintf("http://%s:%d", ip.String(), port)})
			}
		}
	}
	return append(urls, usb4...)
}

// spaHandler serves the embedded SPA; unknown non-/api paths fall back to
// index.html so client-side routes load directly.
func (c *Console) spaHandler() http.Handler {
	files := http.FileServer(http.FS(c.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"), "")
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if f, err := c.static.Open(name); err == nil {
				_ = f.Close()
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(c.indexHTML)
	})
}

// tryCollect enforces single-flight collection so concurrent clicks cannot
// stack. The caller must call the returned function exactly once.
func (c *Console) tryCollect(w http.ResponseWriter) (func(), bool) {
	if !c.collectMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("collection already in progress"), "a refresh is running; retry in a few seconds")
		return nil, false
	}
	return c.collectMu.Unlock, true
}

func (c *Console) collectLocal() HostPayload {
	return collectHost(c.cfg.Version, "auto", c.cfg.Peer)
}

// peerRequest performs one authenticated request against the peer agent with
// the peer proxy timeout.
func (c *Console) peerRequest(r *http.Request, method, apiPath string, body any, query url.Values) (int, []byte, error) {
	if c.cfg.Peer == "" {
		return 0, nil, errors.New("no peer configured")
	}
	u := "http://" + net.JoinHostPort(c.cfg.Peer, fmt.Sprint(c.cfg.AgentPort)) + apiPath
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(r.Context(), method, u, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read peer response: %w", err)
	}
	return resp.StatusCode, b, nil
}

// fetchPeerHost returns the peer's composite host payload, or a state of
// no_peer, unreachable, or no_agent.
func (c *Console) fetchPeerHost(r *http.Request) (json.RawMessage, string, string) {
	if c.cfg.Peer == "" {
		return nil, "no_peer", "start the console with --peer PEER_USB4_ADDRESS and run ciru-strixlink agent on the peer"
	}
	query := url.Values{}
	if _, ip, err := selectIPv4("auto"); err == nil {
		query.Set("peer", ip)
	}
	status, body, err := c.peerRequest(r, http.MethodGet, "/api/agent/host", nil, query)
	if err != nil {
		return nil, "unreachable", err.Error()
	}
	switch {
	case status == http.StatusNotFound:
		return nil, "no_agent", "the peer answered HTTP but has no agent endpoints; start ciru-strixlink agent on the peer"
	case status != http.StatusOK:
		return nil, "unreachable", fmt.Sprintf("peer agent returned status %d: %s", status, strings.TrimSpace(string(body)))
	}
	return json.RawMessage(body), "", ""
}

// reconcileReports applies the pair decision authority exactly like the CLI:
// transport.Reconcile over the two freshest transport reports. In report-file
// mode the disk reports are re-read and reconciled; otherwise the freshly
// collected local and peer reports are used. The returned envelope carries
// the pair and both per-host reports; on any gap its State is "unavailable"
// with only Reason set, never a half-populated envelope.
func (c *Console) reconcileReports(local HostPayload, peerRaw json.RawMessage, peerState string) pairEnvelope {
	if c.filesMode() {
		a, err := transport.LoadReport(c.cfg.ReportA)
		if err != nil {
			return pairEnvelope{State: "unavailable", Reason: "load endpoint A report: " + err.Error()}
		}
		b, err := transport.LoadReport(c.cfg.ReportB)
		if err != nil {
			return pairEnvelope{State: "unavailable", Reason: "load endpoint B report: " + err.Error()}
		}
		p := transport.Reconcile(a, b)
		return pairEnvelope{State: "ok", Pair: &p, A: &a, B: &b, AKind: "file", BKind: "file"}
	}
	if c.cfg.Peer == "" {
		return pairEnvelope{State: "unavailable", Reason: "no peer configured; pair reconciliation requires --peer or --report-a/--report-b"}
	}
	if peerState != "" {
		return pairEnvelope{State: "unavailable", Reason: "peer host payload unavailable: " + peerState}
	}
	if local.Transport == nil {
		return pairEnvelope{State: "unavailable", Reason: "local transport report unavailable"}
	}
	var pp HostPayload
	if err := json.Unmarshal(peerRaw, &pp); err != nil {
		return pairEnvelope{State: "unavailable", Reason: "cannot parse peer host payload: " + err.Error()}
	}
	if pp.Transport == nil {
		return pairEnvelope{State: "unavailable", Reason: "peer agent did not return a transport report"}
	}
	p := transport.Reconcile(*local.Transport, *pp.Transport)
	return pairEnvelope{State: "ok", Pair: &p, A: local.Transport, B: pp.Transport, AKind: "local", BKind: "agent"}
}

func (c *Console) handleHealth(w http.ResponseWriter, _ *http.Request) {
	pairSource := "live"
	if c.filesMode() {
		pairSource = "files"
	}
	writeJSON(w, http.StatusOK, healthPayload{
		Version: c.cfg.Version, SchemaVersion: SchemaVersion, Mode: "console",
		Hostname: c.hostname, OS: runtime.GOOS, Supported: c.supported, StartedAt: c.startedAt,
		Peer:       peerConfigInfo{Configured: c.cfg.Peer != "", Address: c.cfg.Peer, AgentPort: c.cfg.AgentPort, TokenSet: c.cfg.Token != ""},
		PairSource: pairSource,
	})
}

func (c *Console) handleHostLocal(w http.ResponseWriter, r *http.Request) {
	unlock, ok := c.tryCollect(w)
	if !ok {
		return
	}
	defer unlock()
	writeJSON(w, http.StatusOK, c.collectLocal())
}

func (c *Console) handleHostPeer(w http.ResponseWriter, r *http.Request) {
	unlock, ok := c.tryCollect(w)
	if !ok {
		return
	}
	defer unlock()
	raw, state, detail := c.fetchPeerHost(r)
	if state != "" {
		writeJSON(w, http.StatusOK, peerState{State: state, Detail: detail})
		return
	}
	writeJSON(w, http.StatusOK, raw)
}

func (c *Console) handleRefresh(w http.ResponseWriter, r *http.Request) {
	unlock, ok := c.tryCollect(w)
	if !ok {
		return
	}
	defer unlock()
	local := c.collectLocal()
	var peerRaw json.RawMessage
	var state, detail string
	if c.cfg.Peer != "" {
		peerRaw, state, detail = c.fetchPeerHost(r)
	}
	pairEnv := c.reconcileReports(local, peerRaw, state)
	var peerView any = peerState{State: "no_peer", Detail: "start the console with --peer PEER_USB4_ADDRESS and run ciru-strixlink agent on the peer"}
	if state != "" {
		peerView = peerState{State: state, Detail: detail}
	} else if peerRaw != nil {
		peerView = peerRaw
	}
	summary := "refreshed local host"
	if c.cfg.Peer != "" && state == "" {
		summary = "refreshed both hosts"
	}
	if pairEnv.State != "ok" {
		c.activity.add("refresh", "pair", "error", summary+"; pair unavailable", pairEnv.Reason)
	} else {
		c.activity.add("refresh", "pair", "ok", summary, "")
	}
	writeJSON(w, http.StatusOK, struct {
		Local HostPayload  `json:"local"`
		Peer  any          `json:"peer"`
		Pair  pairEnvelope `json:"pair"`
	}{local, peerView, pairEnv})
}

func (c *Console) handlePair(w http.ResponseWriter, r *http.Request) {
	unlock, ok := c.tryCollect(w)
	if !ok {
		return
	}
	defer unlock()
	var pairEnv pairEnvelope
	if c.filesMode() {
		pairEnv = c.reconcileReports(HostPayload{}, nil, "")
	} else {
		local := c.collectLocal()
		var peerRaw json.RawMessage
		var state string
		if c.cfg.Peer != "" {
			peerRaw, state, _ = c.fetchPeerHost(r)
		}
		pairEnv = c.reconcileReports(local, peerRaw, state)
	}
	writeJSON(w, http.StatusOK, pairEnv)
}

func (c *Console) handleInstallPlan(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := install.Build(install.Options{ToolVersion: c.cfg.Version, IncludeOptional: queryBool(q.Get("include_optional")), InstallSelf: queryBool(q.Get("self")), Prefix: "/usr/local"})
	if err != nil {
		c.activity.add("install_plan", "local", "error", "install preview failed", err.Error())
		writeError(w, http.StatusBadRequest, err, "")
		return
	}
	c.activity.add("install_plan", "local", "ok", fmt.Sprintf("install preview: %d action(s), can_apply=%t", len(p.Actions), p.CanApply), "")
	writeJSON(w, http.StatusOK, p)
}

type setupPlanRequest struct {
	Role     string `json:"role"`
	Subnet   string `json:"subnet"`
	MTU      int    `json:"mtu"`
	Backend  string `json:"backend"`
	Profile  string `json:"profile"`
	TakeOver bool   `json:"take_over"`
}

func (c *Console) handleSetupPlan(w http.ResponseWriter, r *http.Request) {
	var req setupPlanRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role == "" {
		writeError(w, http.StatusBadRequest, errors.New("role is required (a or b)"), "")
		return
	}
	if req.Subnet == "" {
		req.Subnet = "10.77.77.0/30"
	}
	if req.MTU == 0 {
		req.MTU = 1500
	}
	p, err := link.BuildSetupPlan(link.SetupOptions{Interface: "auto", Role: req.Role, Subnet: req.Subnet, MTU: req.MTU, Profile: req.Profile, Backend: req.Backend, Replace: req.TakeOver})
	if err != nil {
		c.activity.add("setup_plan", "local", "error", "setup preview failed", err.Error())
		writeError(w, http.StatusBadRequest, err, "")
		return
	}
	c.activity.add("setup_plan", "local", "ok", fmt.Sprintf("setup preview: role=%s backend=%s %s on %s", req.Role, p.Backend, p.Address, p.Interface), "")
	writeJSON(w, http.StatusOK, p)
}

func (c *Console) handleRollbackPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile string `json:"profile"`
		Restore string `json:"restore"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := link.BuildRollbackPlan(req.Profile, "auto", req.Restore)
	if err != nil {
		c.activity.add("rollback_plan", "local", "error", "rollback preview failed", err.Error())
		writeError(w, http.StatusBadRequest, err, "")
		return
	}
	c.activity.add("rollback_plan", "local", "ok", "rollback preview on "+p.Interface, "")
	writeJSON(w, http.StatusOK, p)
}

func (c *Console) handleDoctor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Peer string `json:"peer"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Peer == "" {
		writeError(w, http.StatusBadRequest, errors.New("peer is required"), "")
		return
	}
	i, localIP, err := selectIPv4("auto")
	if err != nil {
		c.activity.add("doctor", req.Peer, "error", "no configured USB4 interface", err.Error())
		writeError(w, http.StatusInternalServerError, err, "run setup --apply on this peer first")
		return
	}
	route, err := link.RouteTo(req.Peer, i.Name)
	if err != nil {
		c.activity.add("doctor", req.Peer, "error", "route lookup failed", err.Error())
		writeError(w, http.StatusInternalServerError, err, "")
		return
	}
	mtuOK, mtuDetail := link.VerifyPathMTU(req.Peer, i.Name, i.MTU)
	status := "ok"
	if !route.InterfaceMatch || !mtuOK {
		status = "error"
	}
	c.activity.add("doctor", req.Peer, status, fmt.Sprintf("route_match=%t path_mtu=%t", route.InterfaceMatch, mtuOK), mtuDetail)
	writeJSON(w, http.StatusOK, struct {
		Interface     string     `json:"interface"`
		LocalIP       string     `json:"local_ip"`
		Route         link.Route `json:"route"`
		PathMTUPassed bool       `json:"path_mtu_passed"`
		PathMTUDetail string     `json:"path_mtu_detail"`
	}{i.Name, localIP, route, mtuOK, mtuDetail})
}

func (c *Console) handleEndpointPlan(w http.ResponseWriter, r *http.Request) {
	var req endpointPlanRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Action != "prepare" && req.Action != "cleanup" {
		writeError(w, http.StatusBadRequest, errors.New("action must be prepare or cleanup"), "")
		return
	}
	peerAddr := req.Peer
	if peerAddr == "" {
		peerAddr = c.cfg.Peer
	}
	unlock, ok := c.tryCollect(w)
	if !ok {
		return
	}
	defer unlock()
	local := c.collectLocal()
	var peerRaw json.RawMessage
	var peerState string
	if c.cfg.Peer != "" && !c.filesMode() {
		peerRaw, peerState, _ = c.fetchPeerHost(r)
	}
	pairEnv := c.reconcileReports(local, peerRaw, peerState)
	if pairEnv.State != "ok" || !pairEnv.Pair.PairIdentityValid {
		detail := pairEnv.Reason
		if detail == "" {
			detail = pairEnv.Pair.Summary
		}
		c.activity.add("endpoint_plan", "pair", "error", "apply-type preview disabled: pair identity is not valid", detail)
		writeError(w, http.StatusConflict, errors.New("pair identity is not valid; apply-type previews are disabled"), detail)
		return
	}
	localPlan, err := transport.BuildEndpointPlan(endpointOptions(c.cfg.Version, "auto", endpointPlanRequest{Action: req.Action, Peer: peerAddr, Name: req.Name, Ring: req.Ring, ThrottlingNS: req.ThrottlingNS, Adopt: req.Adopt}))
	if err != nil {
		c.activity.add("endpoint_plan", "local", "error", "endpoint preview failed", err.Error())
		writeError(w, http.StatusBadRequest, err, "")
		return
	}
	resp := endpointPlanResponse{PairIdentityValid: true, Local: localPlan, PeerState: "no_peer"}
	if c.cfg.Peer != "" {
		peerPlan, state, errDetail := c.fetchPeerEndpointPlan(r, req)
		resp.Peer, resp.PeerState, resp.PeerError = peerPlan, state, errDetail
	}
	c.activity.add("endpoint_plan", "pair", "ok", fmt.Sprintf("%s preview: can_apply=%t", req.Action, localPlan.CanApply), "")
	writeJSON(w, http.StatusOK, resp)
}

// fetchPeerEndpointPlan asks the peer agent for its dry-run plan. The
// forwarded peer address is this console's own USB4 address, so the peer
// plan's network gate points back here.
func (c *Console) fetchPeerEndpointPlan(r *http.Request, req endpointPlanRequest) (*transport.EndpointPlan, string, string) {
	fwd := req
	if _, ip, err := selectIPv4("auto"); err == nil {
		fwd.Peer = ip
	} else {
		fwd.Peer = ""
	}
	status, body, err := c.peerRequest(r, http.MethodPost, "/api/agent/endpoint/plan", fwd, nil)
	switch {
	case err != nil:
		return nil, "unreachable", err.Error()
	case status == http.StatusNotFound:
		return nil, "no_agent", strings.TrimSpace(string(body))
	case status != http.StatusOK:
		return nil, "unreachable", fmt.Sprintf("peer agent returned status %d: %s", status, strings.TrimSpace(string(body)))
	}
	var p transport.EndpointPlan
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, "unreachable", "cannot parse peer endpoint plan: " + err.Error()
	}
	return &p, "ok", ""
}

func (c *Console) handleBench(w http.ResponseWriter, r *http.Request) {
	var req benchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.DurationS == 0 {
		req.DurationS = 5
	}
	if req.Streams == 0 {
		req.Streams = 4
	}
	if req.RTTSamples == 0 {
		req.RTTSamples = 100
	}
	if req.Port == 0 {
		req.Port = bench.DefaultPort
	}
	if req.DurationS < 1 || req.DurationS > 30 {
		writeError(w, http.StatusBadRequest, errors.New("duration_s must be between 1 and 30"), "the peer listener is time-boxed to at most 120s, which bounds the benchmark duration")
		return
	}
	if req.Streams < 1 || req.Streams > 32 {
		writeError(w, http.StatusBadRequest, errors.New("streams must be between 1 and 32"), "")
		return
	}
	if req.RTTSamples < 1 || req.RTTSamples > 10000 {
		writeError(w, http.StatusBadRequest, errors.New("rtt_samples must be between 1 and 10000"), "")
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, errors.New("port must be between 1 and 65535"), "")
		return
	}
	if c.cfg.Peer == "" {
		writeError(w, http.StatusBadRequest, errors.New("benchmark requires a peer"), "start the console with --peer PEER_USB4_ADDRESS and run ciru-strixlink agent on the peer")
		return
	}
	if !c.benchMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("a benchmark is already in progress"), "wait for it to finish before starting another")
		return
	}
	defer c.benchMu.Unlock()
	i, localIP, err := selectIPv4("auto")
	if err != nil {
		c.activity.add("bench", c.cfg.Peer, "error", "no configured USB4 interface", err.Error())
		writeError(w, http.StatusInternalServerError, err, "run setup --apply on this peer first")
		return
	}
	route, err := link.RouteTo(c.cfg.Peer, i.Name)
	if err != nil {
		c.activity.add("bench", c.cfg.Peer, "error", "route lookup failed", err.Error())
		writeError(w, http.StatusInternalServerError, err, "")
		return
	}
	if !route.InterfaceMatch {
		err := fmt.Errorf("route to %s uses %s, not %s; refusing to benchmark the wrong network", c.cfg.Peer, route.Device, i.Name)
		c.activity.add("bench", c.cfg.Peer, "error", "route interface mismatch", err.Error())
		writeError(w, http.StatusConflict, err, route.Raw)
		return
	}
	if route.Source != "" && route.Source != localIP {
		err := fmt.Errorf("route source %s does not match USB4 address %s", route.Source, localIP)
		c.activity.add("bench", c.cfg.Peer, "error", "route source mismatch", err.Error())
		writeError(w, http.StatusConflict, err, route.Raw)
		return
	}
	mtuOK, mtuDetail := link.VerifyPathMTU(c.cfg.Peer, i.Name, i.MTU)
	seconds := clampServeSeconds(int(2*req.DurationS) + 45)
	status, body, err := c.peerRequest(r, http.MethodPost, "/api/agent/serve", serveRequest{Port: req.Port, Seconds: seconds}, nil)
	if err != nil {
		c.activity.add("bench", c.cfg.Peer, "error", "peer agent unreachable", err.Error())
		writeError(w, http.StatusBadGateway, fmt.Errorf("cannot reach the peer agent: %w", err), "verify ciru-strixlink agent is running on the peer")
		return
	}
	if status != http.StatusOK {
		c.activity.add("bench", c.cfg.Peer, "error", fmt.Sprintf("peer agent refused the listener (status %d)", status), strings.TrimSpace(string(body)))
		writeError(w, http.StatusBadGateway, fmt.Errorf("peer agent refused the benchmark listener (status %d)", status), strings.TrimSpace(string(body)))
		return
	}
	result, err := bench.Run(bench.Config{LocalIP: localIP, PeerIP: c.cfg.Peer, Port: req.Port, Streams: req.Streams, Duration: time.Duration(req.DurationS * float64(time.Second)), RTTSamples: req.RTTSamples, Token: c.cfg.Token})
	if err != nil {
		c.activity.add("bench", c.cfg.Peer, "error", "benchmark failed", err.Error())
		writeError(w, http.StatusBadGateway, fmt.Errorf("benchmark failed: %w", err), fmt.Sprintf("the peer listener self-terminates within its %ds time box", seconds))
		return
	}
	host, _ := os.Hostname()
	report := TestReport{
		Version: c.cfg.Version, TimestampUTC: time.Now().UTC(), Hostname: host,
		Interface: i.Name, LocalIP: localIP, PeerIP: c.cfg.Peer, Route: route,
		PathMTUPassed: mtuOK, PathMTUDetail: mtuDetail, Benchmark: result,
	}
	c.activity.add("bench", c.cfg.Peer, "ok", fmt.Sprintf("quality=%s local->peer=%.3fGb/s peer->local=%.3fGb/s", result.Policy.Class, result.Upload.Gbps, result.Download.Gbps), "")
	writeJSON(w, http.StatusOK, report)
}

func (c *Console) handleEnv(w http.ResponseWriter, r *http.Request) {
	var req envRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	unlock, ok := c.tryCollect(w)
	if !ok {
		return
	}
	defer unlock()
	local := c.collectLocal()
	var peerRaw json.RawMessage
	var peerState string
	if c.cfg.Peer != "" && !c.filesMode() {
		peerRaw, peerState, _ = c.fetchPeerHost(r)
	}
	pairEnv := c.reconcileReports(local, peerRaw, peerState)
	if pairEnv.State != "ok" {
		c.activity.add("env", "pair", "error", "no reconciled pair available", pairEnv.Reason)
		writeError(w, http.StatusConflict, errors.New("no reconciled pair available"), pairEnv.Reason)
		return
	}
	// The envelope's A is the local endpoint in both modes: this host's fresh
	// report in live mode, the endpoint A report file in report-file mode.
	env, err := transport.GenerateEnvironment(*pairEnv.A, pairEnv.Pair, req.Mode, req.Runtime)
	if err != nil {
		c.activity.add("env", "pair", "error", "environment generation failed", err.Error())
		writeError(w, http.StatusBadRequest, err, "")
		return
	}
	c.activity.add("env", "pair", "ok", fmt.Sprintf("environment preview: mode=%s runtime=%s", env.Mode, env.Runtime), "")
	writeJSON(w, http.StatusOK, envResponse{Environment: env, DotEnv: env.DotEnv()})
}

func (c *Console) handleActivity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, c.activity.list())
}
