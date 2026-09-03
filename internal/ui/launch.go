package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/transport"
)

const (
	launchModelName     = "GLM5.3-Flash-CIRU-STRIX-IU4"
	launchHelperPath    = "/usr/local/bin/ciru-strixlink"
	launchNixHelperPath = "/run/current-system/sw/bin/glm53-nhi-service-control"
	launchNixSudoPath   = "/run/wrappers/bin/sudo"
)

type launchProfile struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	Context        int    `json:"context_window"`
	KVBytes        int64  `json:"kv_cache_bytes"`
	PrefixCPUBytes int64  `json:"prefix_cache_cpu_bytes"`
	Note           string `json:"note"`
	Recommended    bool   `json:"recommended,omitempty"`
	Experimental   bool   `json:"experimental,omitempty"`
}

var launchProfiles = []launchProfile{
	{ID: 1, Name: "64K", Context: 65664, KVBytes: 6 << 30, PrefixCPUBytes: 8 << 30, Note: "Lower-memory fallback"},
	{ID: 2, Name: "128K", Context: 131200, KVBytes: 12 << 30, PrefixCPUBytes: 8 << 30, Note: "Validated production profile", Recommended: true},
	{ID: 3, Name: "256K", Context: 262272, KVBytes: 8 << 30, PrefixCPUBytes: 8 << 30, Note: "Single-request high-context profile", Experimental: true},
}

type launchModelInfo struct {
	Name             string `json:"name"`
	Topology         string `json:"topology"`
	Transport        string `json:"transport"`
	Speculation      string `json:"speculation"`
	MaxSequences     int    `json:"max_sequences"`
	MaxBatchedTokens int    `json:"max_batched_tokens"`
	PrefixCache      bool   `json:"prefix_cache"`
}

type launchNodeStatus struct {
	Hostname       string `json:"hostname"`
	Username       string `json:"username"`
	Rank           int    `json:"rank"`
	Installed      bool   `json:"installed"`
	State          string `json:"state"`
	Detail         string `json:"detail,omitempty"`
	Unit           string `json:"unit,omitempty"`
	PID            int    `json:"pid,omitempty"`
	Profile        int    `json:"profile,omitempty"`
	ProfileName    string `json:"profile_name,omitempty"`
	Context        int    `json:"context_window,omitempty"`
	KVBytes        int64  `json:"kv_cache_bytes,omitempty"`
	PrefixCPUBytes int64  `json:"prefix_cache_cpu_bytes,omitempty"`
	ControlEnabled bool   `json:"control_enabled"`
	CompetingModel string `json:"competing_model,omitempty"`
	RAMTotalBytes  int64  `json:"ram_total_bytes,omitempty"`
	RAMUsedBytes   int64  `json:"ram_used_bytes,omitempty"`
	RAMAvailBytes  int64  `json:"ram_available_bytes,omitempty"`
}

type launchStatus struct {
	State           string            `json:"state"`
	Summary         string            `json:"summary"`
	Model           launchModelInfo   `json:"model"`
	Profiles        []launchProfile   `json:"profiles"`
	Local           launchNodeStatus  `json:"local"`
	Peer            *launchNodeStatus `json:"peer,omitempty"`
	PeerState       string            `json:"peer_state,omitempty"`
	PeerError       string            `json:"peer_error,omitempty"`
	FastLinkReady   bool              `json:"fast_link_ready"`
	FastLinkState   string            `json:"fast_link_state,omitempty"`
	FastLinkSummary string            `json:"fast_link_summary,omitempty"`
	SelectedProfile int               `json:"selected_profile,omitempty"`
	CanLoad         bool              `json:"can_load"`
	CanUnload       bool              `json:"can_unload"`
	ControlEnabled  bool              `json:"control_enabled"`
	Blockers        []string          `json:"blockers,omitempty"`
	CollectedAt     time.Time         `json:"collected_at"`
}

type launchActionRequest struct {
	Action    string `json:"action"`
	Profile   int    `json:"profile"`
	Confirmed bool   `json:"confirmed"`
}

type launchNodeRequest struct {
	Action  string `json:"action"`
	Profile int    `json:"profile,omitempty"`
}

type launchActionResponse struct {
	Action  string       `json:"action"`
	Summary string       `json:"summary"`
	Status  launchStatus `json:"status"`
}

type launchCommandRunner func(context.Context, string, ...string) ([]byte, error)

type launchController struct {
	enabled        bool
	username       string
	hostname       string
	helper         string
	helperKind     string
	sudo           string
	peer           string
	run            launchCommandRunner
	readFile       func(string) ([]byte, error)
	helperOK       func(string) bool
	controlOK      func(context.Context) bool
	configuredRank *int
}

func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(b))
		if msg != "" {
			return b, fmt.Errorf("%s: %s", filepath.Base(name), msg)
		}
	}
	return b, err
}

func newLaunchController(enabled bool, configuredRank *int, peer string) *launchController {
	u, _ := user.Current()
	username := ""
	if u != nil {
		username = u.Username
	}
	hostname, _ := os.Hostname()
	helper := os.Getenv("CIRU_STRIXLINK_MODEL_HELPER")
	helperKind := "ciru-strixlink"
	if helper == "" && fileOwnerIsRoot(launchNixHelperPath) {
		helper = launchNixHelperPath
		helperKind = "nixos"
	} else if helper == "" {
		helper = launchHelperPath
	}
	sudo := "sudo"
	if _, err := os.Stat(launchNixSudoPath); err == nil {
		sudo = launchNixSudoPath
	}
	c := &launchController{enabled: enabled, username: username, hostname: hostname, helper: helper, helperKind: helperKind, sudo: sudo, peer: peer, run: commandOutput, readFile: os.ReadFile, helperOK: fileOwnerIsRoot}
	if configuredRank != nil && (*configuredRank == 0 || *configuredRank == 1) {
		rank := *configuredRank
		c.configuredRank = &rank
	}
	c.controlOK = c.hasControlPermission
	return c
}

func (c *launchController) hasControlPermission(ctx context.Context) bool {
	if !c.enabled || c.username == "" || c.helperOK == nil || !c.helperOK(c.helper) {
		return false
	}
	// Execute a no-op helper action so this verifies both the installed helper
	// version and passwordless policy without changing model or service state.
	args := []string{"-n", c.helper, "model-node", "status", "--user", c.username}
	if c.helperKind == "nixos" {
		args = []string{"-n", c.helper, "probe"}
	}
	_, err := c.run(ctx, c.sudoCommand(), args...)
	return err == nil
}

func (c *launchController) sudoCommand() string {
	if c.sudo != "" {
		return c.sudo
	}
	return "sudo"
}

func keyValues(body []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			out[k] = v
		}
	}
	return out
}

func intValue(values map[string]string, key string) int {
	v, _ := strconv.Atoi(values[key])
	return v
}

func int64Value(values map[string]string, key string) int64 {
	v, _ := strconv.ParseInt(values[key], 10, 64)
	return v
}

func systemMemory(readFile func(string) ([]byte, error)) (total, available int64) {
	body, err := readFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 2 && strings.EqualFold(fields[2], "kB") {
			value *= 1024
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value
		case "MemAvailable":
			available = value
		}
	}
	return total, available
}

func profileByID(id int) (launchProfile, bool) {
	for _, p := range launchProfiles {
		if p.ID == id {
			return p, true
		}
	}
	return launchProfile{}, false
}

func profileFromValues(values map[string]string) (launchProfile, bool) {
	ctx := intValue(values, "MAX_MODEL_LEN")
	for _, p := range launchProfiles {
		if p.Context == ctx || strings.EqualFold(values["CONTEXT_PROFILE"], strings.ToLower(p.Name)) {
			return p, true
		}
	}
	return launchProfile{}, false
}

func (c *launchController) inspect(ctx context.Context) launchNodeStatus {
	controlEnabled := c.enabled && c.controlOK != nil && c.controlOK(ctx)
	s := launchNodeStatus{Hostname: c.hostname, Username: c.username, Rank: -1, State: "unavailable", ControlEnabled: controlEnabled}
	if c.configuredRank != nil {
		s.Rank = *c.configuredRank
	}
	if c.username == "" {
		s.Detail = "cannot determine the model service user"
		return s
	}
	s.Unit = fmt.Sprintf("%s-nhi@%s.service", launchModelName, c.username)
	b, err := c.run(ctx, "systemctl", "show", s.Unit, "--no-pager", "--property=LoadState,ActiveState,SubState,MainPID")
	if err != nil {
		s.Detail = err.Error()
		return s
	}
	unit := keyValues(b)
	if unit["LoadState"] != "loaded" {
		s.State = "not_installed"
		s.Detail = "the GLM 5.3 NHI service is not installed"
		return s
	}
	s.Installed = true
	s.PID = intValue(unit, "MainPID")
	s.State = "stopped"
	s.Detail = unit["SubState"]
	if unit["ActiveState"] == "active" {
		s.State = "loaded"
	} else if unit["ActiveState"] == "activating" || unit["ActiveState"] == "deactivating" {
		s.State = unit["ActiveState"]
	} else if unit["ActiveState"] == "failed" {
		s.State = "failed"
	}
	if s.Rank == -1 {
		node, _ := c.readFile(fmt.Sprintf("/etc/ciru-glm53-iu4/node-%s.env", c.username))
		nodeValues := keyValues(node)
		if _, ok := nodeValues["NODE_RANK"]; ok {
			s.Rank = intValue(nodeValues, "NODE_RANK")
		}
	}
	if s.Rank == -1 && s.PID > 0 {
		cmdline, _ := c.readFile(fmt.Sprintf("/proc/%d/cmdline", s.PID))
		if match := launchNodeRank.FindSubmatch(cmdline); len(match) == 2 {
			s.Rank = int(match[1][0] - '0')
		}
	}
	if mainState, err := c.run(ctx, "systemctl", "--user", "show", "qwen-main.service", "--no-pager", "--property=ActiveState"); err == nil && keyValues(mainState)["ActiveState"] == "active" {
		s.CompetingModel = "qwen-main.service"
	}
	contextBody, _ := c.readFile(fmt.Sprintf("/etc/ciru-glm53-iu4/context-%s.env", c.username))
	contextValues := keyValues(contextBody)
	if p, ok := profileFromValues(contextValues); ok {
		s.Profile, s.ProfileName = p.ID, p.Name
		s.Context, s.KVBytes, s.PrefixCPUBytes = p.Context, p.KVBytes, p.PrefixCPUBytes
	} else {
		s.Context = intValue(contextValues, "MAX_MODEL_LEN")
		s.KVBytes = int64Value(contextValues, "KV_CACHE_BYTES")
		s.PrefixCPUBytes = int64Value(contextValues, "PREFIX_CACHE_CPU_BYTES")
	}
	s.RAMTotalBytes, s.RAMAvailBytes = systemMemory(c.readFile)
	if s.RAMTotalBytes >= s.RAMAvailBytes {
		s.RAMUsedBytes = s.RAMTotalBytes - s.RAMAvailBytes
	}
	return s
}

func (c *launchController) apply(ctx context.Context, req launchNodeRequest) error {
	if !c.enabled {
		return errors.New("model control is not enabled on this host")
	}
	if c.username == "" {
		return errors.New("cannot determine the model service user")
	}
	if c.helperOK == nil || !c.helperOK(c.helper) {
		return fmt.Errorf("privileged helper %s is missing, not root-owned, or writable by another user", c.helper)
	}
	if req.Action != "configure" && req.Action != "load" && req.Action != "unload" {
		return errors.New("node action must be configure, load, or unload")
	}
	if req.Action == "configure" {
		if _, ok := profileByID(req.Profile); !ok {
			return errors.New("profile must be 1, 2, or 3")
		}
	}
	args := []string{"-n", c.helper, "model-node", req.Action, "--user", c.username}
	if c.helperKind == "nixos" {
		action := map[string]string{"load": "start", "unload": "stop"}[req.Action]
		if req.Action == "configure" {
			action = "context-" + strings.ToLower(launchProfiles[req.Profile-1].Name)
		}
		args = []string{"-n", c.helper, action}
	} else if req.Action == "configure" {
		args = append(args, "--profile", strconv.Itoa(req.Profile))
	}
	_, err := c.run(ctx, c.sudoCommand(), args...)
	return err
}

func (c *launchController) transport(ctx context.Context) (transport.Report, error) {
	if !c.enabled {
		return transport.Report{}, errors.New("privileged Fast Link status is unavailable")
	}
	if c.helperOK == nil || !c.helperOK(c.helper) {
		return transport.Report{}, fmt.Errorf("privileged helper %s is missing or unsafe", c.helper)
	}
	args := []string{"-n", c.helper, "transport-status"}
	if c.helperKind != "nixos" {
		if ip := net.ParseIP(c.peer); ip == nil || ip.To4() == nil {
			return transport.Report{}, errors.New("privileged Fast Link status requires a fixed peer IP")
		}
		args = []string{"-n", c.helper, "model-node", "transport-status", "--user", c.username, "--peer", c.peer}
	}
	body, err := c.run(ctx, c.sudoCommand(), args...)
	if err != nil {
		return transport.Report{}, err
	}
	var report transport.Report
	if err := json.Unmarshal(body, &report); err != nil {
		return transport.Report{}, fmt.Errorf("decode privileged Fast Link status: %w", err)
	}
	return report, nil
}

func launchModel() launchModelInfo {
	return launchModelInfo{Name: launchModelName, Topology: "2 machines · tensor parallel 2", Transport: "Direct USB4 · NHI", Speculation: "DFlash2 · 7 draft tokens", MaxSequences: 1, MaxBatchedTokens: 2304, PrefixCache: true}
}

func combineLaunchStatus(local launchNodeStatus, peer *launchNodeStatus, peerState, peerErr string, control bool) launchStatus {
	s := launchStatus{State: "unavailable", Summary: "Model state is unavailable.", Model: launchModel(), Profiles: append([]launchProfile(nil), launchProfiles...), Local: local, Peer: peer, PeerState: peerState, PeerError: peerErr, ControlEnabled: control, CollectedAt: time.Now().UTC()}
	if peer == nil {
		s.Blockers = append(s.Blockers, "The second machine is not reporting model status.")
		return s
	}
	if !local.Installed || !peer.Installed {
		s.State, s.Summary = "not_installed", "GLM 5.3 is not installed for paired launch on both machines."
		s.Blockers = append(s.Blockers, "Install the fixed GLM 5.3 NHI service on both machines.")
		return s
	}
	if local.Rank == peer.Rank || (local.Rank != 0 && local.Rank != 1) || (peer.Rank != 0 && peer.Rank != 1) {
		s.State, s.Summary = "misconfigured", "The two machines do not report complementary TP ranks."
		s.Blockers = append(s.Blockers, "One machine must be rank 0 and the other rank 1.")
		return s
	}
	if local.Profile != 0 && local.Profile == peer.Profile {
		s.SelectedProfile = local.Profile
	} else {
		s.Blockers = append(s.Blockers, "The machines have different or unknown context profiles.")
	}
	loaded := 0
	if local.State == "loaded" {
		loaded++
	}
	if peer.State == "loaded" {
		loaded++
	}
	switch loaded {
	case 2:
		s.State, s.Summary, s.CanUnload = "loaded", "GLM 5.3 is loaded across both machines.", true
	case 1:
		s.State, s.Summary, s.CanUnload = "partial", "Only one model rank is loaded. Unload the pair before trying again.", true
		s.Blockers = append(s.Blockers, "The model is in a one-rank state.")
	default:
		s.State, s.Summary = "unloaded", "GLM 5.3 is ready to load on both machines."
		s.CanLoad = true
	}
	if s.State == "unloaded" && (local.CompetingModel != "" || peer.CompetingModel != "") {
		s.CanLoad = false
		var hosts []string
		if local.CompetingModel != "" {
			hosts = append(hosts, local.Hostname)
		}
		if peer.CompetingModel != "" {
			hosts = append(hosts, peer.Hostname)
		}
		s.Blockers = append(s.Blockers, "Stop the currently selected main model on "+strings.Join(hosts, " and ")+" before loading GLM 5.3.")
	}
	if !control || !local.ControlEnabled || !peer.ControlEnabled {
		s.CanLoad, s.CanUnload = false, false
		s.Blockers = append(s.Blockers, "Paired model control is locked. Install the current root-owned helper and its exact passwordless policy on both machines, then use --model-control with a shared token.")
	}
	return s
}

func (c *Console) fetchPeerLaunchStatus(r *http.Request) (*launchNodeStatus, string, string) {
	status, body, err := c.peerRequest(r, http.MethodGet, "/api/agent/launch", nil, nil)
	switch {
	case err != nil:
		return nil, "unreachable", err.Error()
	case status == http.StatusNotFound:
		return nil, "unsupported", "update the CiruStrixLink peer agent to use paired model controls"
	case status != http.StatusOK:
		return nil, "unavailable", fmt.Sprintf("peer agent returned status %d: %s", status, strings.TrimSpace(string(body)))
	}
	var s launchNodeStatus
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, "unavailable", "cannot parse peer model status: " + err.Error()
	}
	return &s, "ok", ""
}

func (c *Console) collectLaunchStatus(r *http.Request) launchStatus {
	local := c.launch.inspect(r.Context())
	if c.cfg.Peer == "" || c.filesMode() {
		return combineLaunchStatus(local, nil, "no_peer", "paired live status requires a peer agent", false)
	}
	peer, state, detail := c.fetchPeerLaunchStatus(r)
	s := combineLaunchStatus(local, peer, state, detail, c.cfg.ModelControl && c.cfg.Token != "")
	if peer == nil || !c.cfg.ModelControl || c.cfg.Token == "" {
		return s
	}
	localTransport, err := c.launch.transport(r.Context())
	if err != nil {
		s.FastLinkState, s.FastLinkSummary = "unavailable", err.Error()
		if s.State == "unloaded" {
			s.CanLoad = false
			s.Blockers = append(s.Blockers, "Fast Link readiness could not be verified with the privileged helper.")
		}
		return s
	}
	peerTransport, peerTransportState, peerTransportErr := c.fetchPeerLaunchTransport(r)
	if peerTransportState != "ok" {
		s.FastLinkState, s.FastLinkSummary = peerTransportState, peerTransportErr
		if s.State == "unloaded" {
			s.CanLoad = false
			s.Blockers = append(s.Blockers, "Fast Link readiness could not be verified on the second machine.")
		}
		return s
	}
	pair := transport.Reconcile(localTransport, peerTransport)
	s.FastLinkReady, s.FastLinkState, s.FastLinkSummary = pair.NHIReady, pair.NHIStatus, pair.Summary
	if s.State == "unloaded" && (!pair.PairIdentityValid || !pair.NHIReady || !pair.LeaseAvailable || pair.Fallback.CleanupRequired) {
		s.CanLoad = false
		s.Blockers = append(s.Blockers, "Fast USB4 must be ready and available on both machines before loading.")
	}
	return s
}

func (c *Console) handleLaunchStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, c.collectLaunchStatus(r))
}

func (c *Console) peerLaunchAction(r *http.Request, req launchNodeRequest) error {
	status, body, err := c.peerRequest(r, http.MethodPost, "/api/agent/launch/node", req, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("peer returned status %d: %s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Console) fetchPeerLaunchTransport(r *http.Request) (transport.Report, string, string) {
	status, body, err := c.peerRequest(r, http.MethodGet, "/api/agent/launch/transport", nil, nil)
	switch {
	case err != nil:
		return transport.Report{}, "unreachable", err.Error()
	case status == http.StatusNotFound:
		return transport.Report{}, "unsupported", "update the CiruStrixLink peer agent to expose privileged Fast Link status"
	case status != http.StatusOK:
		return transport.Report{}, "unavailable", fmt.Sprintf("peer agent returned status %d: %s", status, strings.TrimSpace(string(body)))
	}
	var report transport.Report
	if err := json.Unmarshal(body, &report); err != nil {
		return transport.Report{}, "unavailable", "cannot parse peer Fast Link status: " + err.Error()
	}
	return report, "ok", ""
}

func (c *Console) nodeAction(r *http.Request, node launchNodeStatus, req launchNodeRequest) error {
	if node.Hostname == c.launch.hostname && node.Username == c.launch.username {
		return c.launch.apply(r.Context(), req)
	}
	return c.peerLaunchAction(r, req)
}

func (c *Console) waitForModelReady(r *http.Request, expectedContext int) error {
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	for {
		probeRequest := r.Clone(ctx)
		local := c.launch.inspect(ctx)
		peer, _, peerErr := c.fetchPeerLaunchStatus(probeRequest)
		if local.State == "failed" || (peer != nil && peer.State == "failed") {
			return errors.New("one or more GLM ranks failed during initialization")
		}
		if peer == nil && peerErr != "" {
			return errors.New("the second rank stopped reporting during initialization: " + peerErr)
		}
		if c.model != nil {
			model := c.model.collect(ctx)
			if model.State == "connected" {
				if model.Context != expectedContext {
					return fmt.Errorf("model frontend reported context %d instead of %d", model.Context, expectedContext)
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("model frontend did not become ready within 150 seconds")
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *Console) handleLaunchAction(w http.ResponseWriter, r *http.Request) {
	var req launchActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Action != "load" && req.Action != "unload" {
		writeError(w, http.StatusBadRequest, errors.New("action must be load or unload"), "")
		return
	}
	if !req.Confirmed {
		writeError(w, http.StatusBadRequest, errors.New("paired action was not confirmed"), "review the two-machine action and confirm it in the Launch page")
		return
	}
	if req.Action == "load" {
		if _, ok := profileByID(req.Profile); !ok {
			writeError(w, http.StatusBadRequest, errors.New("profile must be 1, 2, or 3"), "")
			return
		}
	}
	if !c.cfg.ModelControl || c.cfg.Token == "" || c.filesMode() {
		writeError(w, http.StatusForbidden, errors.New("paired model control is locked"), "start the loopback console and USB4-only agent with --model-control and the same --token-file")
		return
	}
	if !c.launchMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("a paired model action is already running"), "wait for it to finish")
		return
	}
	defer c.launchMu.Unlock()
	// A browser refresh or closed tab must not interrupt a two-rank action and
	// leave the pair half-configured. Keep the bounded operation alive after the
	// request disconnects, while the handler still reports the result when the
	// client remains connected.
	opCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	opRequest := r.Clone(opCtx)
	before := c.collectLaunchStatus(opRequest)
	if before.Peer == nil || (!before.CanLoad && req.Action == "load") || (!before.CanUnload && req.Action == "unload") {
		writeError(w, http.StatusConflict, errors.New("the model pair is not ready for this action"), strings.Join(before.Blockers, " "))
		return
	}
	local, peer := before.Local, *before.Peer
	rank0, rank1 := local, peer
	if peer.Rank == 0 {
		rank0, rank1 = peer, local
	}
	if req.Action == "load" {
		oldLocal, oldPeer := local.Profile, peer.Profile
		if err := c.nodeAction(opRequest, local, launchNodeRequest{Action: "configure", Profile: req.Profile}); err != nil {
			c.activity.add("model_load", "pair", "error", "local profile update failed", err.Error())
			writeError(w, http.StatusBadGateway, fmt.Errorf("configure %s: %w", local.Hostname, err), "no rank was started")
			return
		}
		if err := c.nodeAction(opRequest, peer, launchNodeRequest{Action: "configure", Profile: req.Profile}); err != nil {
			if oldLocal != 0 {
				_ = c.nodeAction(opRequest, local, launchNodeRequest{Action: "configure", Profile: oldLocal})
			}
			c.activity.add("model_load", "pair", "error", "peer profile update failed", err.Error())
			writeError(w, http.StatusBadGateway, fmt.Errorf("configure %s: %w", peer.Hostname, err), "no rank was started; the local profile was restored when possible")
			return
		}
		if err := c.nodeAction(opRequest, rank0, launchNodeRequest{Action: "load"}); err != nil {
			if oldLocal != 0 {
				_ = c.nodeAction(opRequest, local, launchNodeRequest{Action: "configure", Profile: oldLocal})
			}
			if oldPeer != 0 {
				_ = c.nodeAction(opRequest, peer, launchNodeRequest{Action: "configure", Profile: oldPeer})
			}
			c.activity.add("model_load", "pair", "error", "rank 0 failed to start", err.Error())
			writeError(w, http.StatusBadGateway, fmt.Errorf("start rank 0 on %s: %w", rank0.Hostname, err), "rank 1 was not started")
			return
		}
		if err := c.nodeAction(opRequest, rank1, launchNodeRequest{Action: "load"}); err != nil {
			rollbackErr := c.nodeAction(opRequest, rank0, launchNodeRequest{Action: "unload"})
			detail := "rank 0 was stopped again"
			if rollbackErr != nil {
				detail = "rank 0 rollback also failed: " + rollbackErr.Error()
			}
			c.activity.add("model_load", "pair", "error", "rank 1 failed to start", err.Error()+"; "+detail)
			writeError(w, http.StatusBadGateway, fmt.Errorf("start rank 1 on %s: %w", rank1.Hostname, err), detail)
			return
		}
		if err := c.waitForModelReady(opRequest, launchProfiles[req.Profile-1].Context); err != nil {
			err1 := c.nodeAction(opRequest, rank1, launchNodeRequest{Action: "unload"})
			err0 := c.nodeAction(opRequest, rank0, launchNodeRequest{Action: "unload"})
			detail := err.Error()
			if err1 != nil || err0 != nil {
				detail += fmt.Sprintf("; rollback rank 1: %v; rank 0: %v", err1, err0)
			}
			c.activity.add("model_load", "pair", "error", "GLM 5.3 failed to become ready", detail)
			writeError(w, http.StatusBadGateway, errors.New("GLM 5.3 did not finish loading"), detail)
			return
		}
		c.activity.add("model_load", "pair", "ok", fmt.Sprintf("started GLM 5.3 with the %s profile", launchProfiles[req.Profile-1].Name), "rank 0 followed by rank 1")
	} else {
		err1 := c.nodeAction(opRequest, rank1, launchNodeRequest{Action: "unload"})
		err0 := c.nodeAction(opRequest, rank0, launchNodeRequest{Action: "unload"})
		if err1 != nil || err0 != nil {
			detail := fmt.Sprintf("rank 1: %v; rank 0: %v", err1, err0)
			c.activity.add("model_unload", "pair", "error", "one or more ranks failed to stop", detail)
			writeError(w, http.StatusBadGateway, errors.New("could not stop both model ranks"), detail)
			return
		}
		c.activity.add("model_unload", "pair", "ok", "stopped both GLM 5.3 ranks", "rank 1 followed by rank 0")
	}
	status := c.collectLaunchStatus(opRequest)
	summary := "GLM 5.3 load requested on both machines."
	if req.Action == "unload" {
		summary = "GLM 5.3 unloaded from both machines."
	}
	writeJSON(w, http.StatusOK, launchActionResponse{Action: req.Action, Summary: summary, Status: status})
}

func (a *Agent) handleLaunchStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, a.launch.inspect(r.Context()))
}

func (a *Agent) handleLaunchTransport(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Token == "" || !a.cfg.ModelControl {
		writeError(w, http.StatusForbidden, errors.New("privileged Fast Link status is locked on this agent"), "restart the USB4-only agent with --model-control and a shared token")
		return
	}
	report, err := a.launch.transport(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, "install and authorize the packaged model-control helper")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, report)
}

func (a *Agent) handleLaunchNode(w http.ResponseWriter, r *http.Request) {
	if a.cfg.Token == "" || !a.cfg.ModelControl {
		writeError(w, http.StatusForbidden, errors.New("model control is locked on this agent"), "restart the USB4-only agent with --model-control and a shared token")
		return
	}
	var req launchNodeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !a.modelMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("a model action is already running on this host"), "wait for it to finish")
		return
	}
	defer a.modelMu.Unlock()
	if err := a.launch.apply(r.Context(), req); err != nil {
		a.activity.add("model_"+req.Action, "local", "error", "model node action failed", err.Error())
		writeError(w, http.StatusInternalServerError, err, "the privileged helper must be installed and explicitly allowed for this service user")
		return
	}
	a.activity.add("model_"+req.Action, "local", "ok", "model node action completed", "")
	writeJSON(w, http.StatusOK, a.launch.inspect(r.Context()))
}

var launchUsername = regexp.MustCompile(`^[a-z_][a-z0-9_-]*\$?$`)
var launchNodeRank = regexp.MustCompile(`(?:^|\x00)--node-rank(?:=|\x00)([01])(?:\x00|$)`)

// RunModelNode is the narrow privileged helper used by the token-protected
// model-control channel. It can only configure or start/stop the fixed GLM 5.3
// NHI unit for the named service user; it never enables, disables, or replaces
// qwen-main.service.
func RunModelNode(action, username string, profileID int, peer string) error {
	if os.Geteuid() != 0 {
		return errors.New("model-node must run as root through an explicit sudoers rule")
	}
	if !launchUsername.MatchString(username) {
		return errors.New("unsupported service user")
	}
	serviceUser, err := user.Lookup(username)
	if err != nil {
		return errors.New("unknown service user")
	}
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != username {
		return errors.New("the requested service user does not match SUDO_USER")
	}
	unit := fmt.Sprintf("%s-nhi@%s.service", launchModelName, username)
	switch action {
	case "status":
		return nil
	case "transport-status":
		if ip := net.ParseIP(peer); ip == nil || ip.To4() == nil {
			return errors.New("transport-status requires an IPv4 --peer")
		}
		report := transport.Inspect("model-control", "auto", peer)
		return json.NewEncoder(os.Stdout).Encode(report)
	case "configure":
		p, ok := profileByID(profileID)
		if !ok {
			return errors.New("profile must be 1, 2, or 3")
		}
		if err := exec.Command("systemctl", "is-active", "--quiet", unit).Run(); err == nil {
			return errors.New("the context profile can only change while the model is stopped")
		}
		dir := "/etc/ciru-glm53-iu4"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".context.*")
		if err != nil {
			return err
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		body := fmt.Sprintf("CONTEXT_PROFILE=%s\nMAX_MODEL_LEN=%d\nKV_CACHE_BYTES=%d\nPREFIX_CACHE_CPU_BYTES=%d\n", strings.ToLower(p.Name), p.Context, p.KVBytes, p.PrefixCPUBytes)
		if _, err = tmp.WriteString(body); err == nil {
			err = tmp.Chmod(0o644)
		}
		if closeErr := tmp.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		return os.Rename(tmpName, filepath.Join(dir, "context-"+username+".env"))
	case "load":
		mainActive, err := userUnitActive(serviceUser, "qwen-main.service")
		if err != nil {
			return fmt.Errorf("check qwen-main.service: %w", err)
		}
		if mainActive {
			return errors.New("qwen-main.service is active; select or stop the current main model before loading GLM 5.3")
		}
		portableActive, err := userUnitActive(serviceUser, launchModelName+".service")
		if err != nil {
			return fmt.Errorf("check portable GLM service: %w", err)
		}
		if portableActive {
			return errors.New("the portable GLM 5.3 user service is active; stop that paired deployment before loading the NHI service")
		}
		return runFixedSystemctl("start", unit)
	case "unload":
		return runFixedSystemctl("stop", unit)
	default:
		return errors.New("model-node action must be status, transport-status, configure, load, or unload")
	}
}

func userUnitActive(serviceUser *user.User, unit string) (bool, error) {
	uid, err := strconv.Atoi(serviceUser.Uid)
	if err != nil || uid < 0 {
		return false, errors.New("service user has an invalid uid")
	}
	runuser, err := exec.LookPath("runuser")
	if err != nil {
		return false, errors.New("runuser is unavailable")
	}
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)
	cmd := exec.Command(runuser, "-u", serviceUser.Username, "--", "env", "XDG_RUNTIME_DIR="+runtimeDir, "DBUS_SESSION_BUS_ADDRESS=unix:path="+runtimeDir+"/bus", "systemctl", "--user", "is-active", "--quiet", unit)
	err = cmd.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && (exitErr.ExitCode() == 3 || exitErr.ExitCode() == 4) {
		return false, nil
	}
	return false, err
}

func runFixedSystemctl(action, unit string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "systemctl", action, unit).CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("systemctl %s timed out", action)
		}
		return fmt.Errorf("systemctl %s: %s", action, strings.TrimSpace(string(b)))
	}
	return nil
}

func fileOwnerIsRoot(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == 0
}
