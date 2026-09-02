package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/bench"
	"github.com/ciru-ai/CiruStrixLink/internal/transport"
)

// AgentConfig configures the USB4-bound peer agent. LocalIP must be the
// selected USB4 interface address; the agent never binds LAN or Wi-Fi.
type AgentConfig struct {
	Version   string
	Interface string
	LocalIP   string
	Port      int
	Token     string
}

// Agent is the read-only peer agent. It is an http.Handler. The agent never
// applies anything; its only listener is the time-boxed benchmark listener.
type Agent struct {
	cfg       AgentConfig
	startedAt time.Time
	hostname  string
	supported bool
	collectMu sync.Mutex
	serveMu   sync.Mutex
	activity  activityLog
	handler   http.Handler
}

// NewAgent builds the agent and wraps every endpoint in bearer-token auth
// when a token is configured.
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.Port == 0 {
		cfg.Port = DefaultAgentPort
	}
	a := &Agent{cfg: cfg, startedAt: time.Now().UTC(), supported: hostSupported(cfg.Version)}
	a.hostname, _ = os.Hostname()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/agent/host", a.handleHost)
	mux.HandleFunc("POST /api/agent/endpoint/plan", a.handleEndpointPlan)
	mux.HandleFunc("POST /api/agent/serve", a.handleServe)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, fmt.Errorf("unknown API path %s", r.URL.Path), "")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, errors.New("this is the CiruStrixLink peer agent; open the console UI on the other host"), "")
	})
	a.handler = bearerAuth(cfg.Token, mux)
	return a
}

// ServeHTTP implements http.Handler.
func (a *Agent) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }

func (a *Agent) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Version       string    `json:"version"`
		SchemaVersion int       `json:"schema_version"`
		Mode          string    `json:"mode"`
		Hostname      string    `json:"hostname"`
		OS            string    `json:"os"`
		Supported     bool      `json:"supported"`
		StartedAt     time.Time `json:"started_at"`
		Interface     string    `json:"interface"`
		LocalIP       string    `json:"local_ip"`
	}{a.cfg.Version, SchemaVersion, "agent", a.hostname, runtime.GOOS, a.supported, a.startedAt, a.cfg.Interface, a.cfg.LocalIP})
}

func (a *Agent) handleHost(w http.ResponseWriter, r *http.Request) {
	if !a.collectMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("collection already in progress"), "retry in a few seconds")
		return
	}
	defer a.collectMu.Unlock()
	writeJSON(w, http.StatusOK, collectHost(a.cfg.Version, a.cfg.Interface, r.URL.Query().Get("peer")))
}

func (a *Agent) handleEndpointPlan(w http.ResponseWriter, r *http.Request) {
	var req endpointPlanRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Action != "prepare" && req.Action != "cleanup" {
		writeError(w, http.StatusBadRequest, errors.New("action must be prepare or cleanup"), "")
		return
	}
	p, err := transport.BuildEndpointPlan(endpointOptions(a.cfg.Version, a.cfg.Interface, req))
	if err != nil {
		a.activity.add("endpoint_plan", "local", "error", "endpoint preview failed", err.Error())
		writeError(w, http.StatusBadRequest, err, "")
		return
	}
	a.activity.add("endpoint_plan", "local", "ok", fmt.Sprintf("%s preview: can_apply=%t", req.Action, p.CanApply), "")
	writeJSON(w, http.StatusOK, p)
}

// handleServe starts the bench listener on the USB4 address for at most
// seconds (hard cap 120) and responds once it is listening. The listener
// self-terminates when the time box expires.
func (a *Agent) handleServe(w http.ResponseWriter, r *http.Request) {
	var req serveRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, errors.New("port must be between 1 and 65535"), "")
		return
	}
	if req.Seconds <= 0 {
		writeError(w, http.StatusBadRequest, errors.New("seconds must be positive"), fmt.Sprintf("the listener is time-boxed; the hard cap is %ds", maxServeSeconds))
		return
	}
	seconds := clampServeSeconds(req.Seconds)
	if !a.serveMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("a benchmark listener is already active"), "it self-terminates at the end of its time box; retry shortly")
		return
	}
	l, err := net.Listen("tcp4", net.JoinHostPort(a.cfg.LocalIP, fmt.Sprint(req.Port)))
	if err != nil {
		a.serveMu.Unlock()
		writeError(w, http.StatusInternalServerError, fmt.Errorf("bind %s:%d: %w", a.cfg.LocalIP, req.Port, err), "the bench port may be in use or the USB4 address is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	go func() {
		defer cancel()
		defer a.serveMu.Unlock()
		// Serve returns nil when the time box closes the listener.
		_ = bench.Serve(ctx, l, a.cfg.Token)
	}()
	a.activity.add("serve", "local", "ok", fmt.Sprintf("benchmark listener on %s:%d for %ds", a.cfg.LocalIP, req.Port, seconds), "")
	writeJSON(w, http.StatusOK, serveResponse{
		Listening: true, Address: a.cfg.LocalIP, Port: req.Port, Seconds: seconds,
		ExpiresAt: time.Now().UTC().Add(time.Duration(seconds) * time.Second),
	})
}
