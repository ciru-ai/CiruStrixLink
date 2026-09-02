package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultStateDir = "/run/ciru-strixlink-nhi"

type EndpointOptions struct {
	ToolVersion string        `json:"tool_version"`
	Action      string        `json:"action"`
	Peer        string        `json:"peer,omitempty"`
	Interface   string        `json:"interface"`
	Name        string        `json:"name"`
	Ring        int           `json:"ring"`
	Throttling  int           `json:"throttling_ns"`
	StateDir    string        `json:"state_dir"`
	DeviceGroup string        `json:"device_group"`
	Timeout     time.Duration `json:"timeout_ns"`
	Adopt       bool          `json:"adopt"`
	Apply       bool          `json:"apply"`
}

type EndpointPlan struct {
	SchemaVersion  int             `json:"schema_version"`
	Action         string          `json:"action"`
	CanApply       bool            `json:"can_apply"`
	ApplyRequested bool            `json:"apply_requested"`
	Summary        string          `json:"summary"`
	Options        EndpointOptions `json:"options"`
	Transport      Report          `json:"transport"`
	Steps          []string        `json:"steps"`
	Blockers       []string        `json:"blockers,omitempty"`
}

type endpointState struct {
	SchemaVersion   int       `json:"schema_version"`
	CreatedAt       time.Time `json:"created_at"`
	Name            string    `json:"name"`
	Service         string    `json:"service"`
	ConfigPath      string    `json:"config_path"`
	Device          string    `json:"device"`
	Ring            int       `json:"ring"`
	Throttling      int       `json:"throttling"`
	ModuleWasLoaded bool      `json:"module_was_loaded"`
	PreviousDMABuf  string    `json:"previous_dmabuf"`
	PreviousKick    string    `json:"previous_kick"`
	Adopted         bool      `json:"adopted"`
}

func endpointDefaults(o *EndpointOptions) error {
	if o.Interface == "" {
		o.Interface = "auto"
	}
	if o.Name == "" {
		o.Name = "ciru-nhi"
	}
	if o.Ring == 0 {
		o.Ring = ExpectedRing
	}
	if o.Throttling == 0 {
		o.Throttling = ExpectedThrottle
	}
	if o.StateDir == "" {
		o.StateDir = defaultStateDir
	}
	if o.DeviceGroup == "" {
		o.DeviceGroup = "render"
	}
	if o.Timeout == 0 {
		o.Timeout = 60 * time.Second
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(o.Name) {
		return errors.New("endpoint name may contain only letters, digits, dot, underscore, and hyphen")
	}
	cleanState := filepath.Clean(o.StateDir)
	if filepath.Dir(cleanState) != "/run" || filepath.Base(cleanState) == "." {
		return errors.New("state directory must be one direct child of /run")
	}
	o.StateDir = cleanState
	if o.Ring != ExpectedRing || o.Throttling != ExpectedThrottle {
		return fmt.Errorf("public NHI mode currently supports only ring=%d and throttling=%d", ExpectedRing, ExpectedThrottle)
	}
	if o.Timeout < 5*time.Second || o.Timeout > 5*time.Minute {
		return errors.New("timeout must be between 5s and 5m")
	}
	return nil
}

func statePath(o EndpointOptions) string { return filepath.Join(o.StateDir, "state.json") }

func readState(o EndpointOptions) (endpointState, error) {
	b, err := os.ReadFile(statePath(o))
	if err != nil {
		return endpointState{}, err
	}
	var s endpointState
	if err := json.Unmarshal(b, &s); err != nil {
		return endpointState{}, err
	}
	if s.SchemaVersion != 1 {
		return endpointState{}, fmt.Errorf("unsupported endpoint state schema %d", s.SchemaVersion)
	}
	return s, nil
}

func writeState(o EndpointOptions, s endpointState) error {
	if err := os.MkdirAll(o.StateDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(o.StateDir, fmt.Sprintf("state.json.tmp.%d", os.Getpid()))
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, statePath(o)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func isRoot() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	b, err := exec.Command("id", "-u").Output()
	return err == nil && strings.TrimSpace(string(b)) == "0"
}

func matchingEndpoint(r Report, name string) *Endpoint {
	for i := range r.Endpoints {
		if r.Endpoints[i].Name == name {
			return &r.Endpoints[i]
		}
	}
	return nil
}

func BuildEndpointPlan(o EndpointOptions) (EndpointPlan, error) {
	if err := endpointDefaults(&o); err != nil {
		return EndpointPlan{}, err
	}
	if o.Action != "prepare" && o.Action != "cleanup" {
		return EndpointPlan{}, errors.New("endpoint action must be prepare or cleanup")
	}
	r := Inspect(o.ToolVersion, o.Interface, o.Peer)
	p := EndpointPlan{SchemaVersion: 1, Action: o.Action, ApplyRequested: o.Apply, Options: o, Transport: r}
	if o.Action == "prepare" {
		p.Steps = []string{
			"lock the local NHI lifecycle",
			fmt.Sprintf("recheck thunderbolt-net carrier, route, and peer reachability so network HopID %d is established first", ExpectedNetHopID),
			"load thunderbolt_stream with the detected supported DMA-BUF parameters",
			"recheck the portable link after module load",
			"discover exactly one dynamic service whose key is stream",
			fmt.Sprintf("create the endpoint and require ring=%d throttle=%d hop=%d/%d", o.Ring, o.Throttling, ExpectedNHIHopID, ExpectedNHIHopID),
			"record exact state; on any error, remove only the partial local endpoint and restore the prior module state",
		}
		if o.Peer == "" {
			p.Blockers = append(p.Blockers, "--peer is required so the network gate cannot be bypassed")
		}
		if !r.Portable.Ready {
			p.Blockers = append(p.Blockers, "portable USB4NET gate is not ready")
		}
		if len(r.Endpoints) > 0 {
			e := matchingEndpoint(r, o.Name)
			if e == nil || !e.ProductionFit {
				p.Blockers = append(p.Blockers, "an existing endpoint is partial, mismatched, or owned under another name; cleanup is required")
			} else if _, err := readState(o); err != nil && !o.Adopt {
				p.Blockers = append(p.Blockers, "the exact endpoint exists without CiruStrixLink state; use --adopt only after reviewing holders and parameters")
			}
		}
		if r.NHI.Status == "unavailable" {
			p.Blockers = append(p.Blockers, "thunderbolt_stream is unavailable")
		}
		p.CanApply = len(p.Blockers) == 0
		if p.CanApply {
			p.Summary = "local endpoint prepare is safe to start concurrently with the peer"
		} else {
			p.Summary = "local endpoint prepare is blocked; portable mode remains the fallback"
		}
	} else {
		p.Steps = []string{
			"lock the local NHI lifecycle",
			"resolve the exact recorded or explicitly adopted endpoint",
			"refuse cleanup while any process holds its /dev/tbstreamN device",
			"write 0 to the endpoint in_hopid and out_hopid",
			"remove only that endpoint and its now-empty dynamic service directory",
			"verify the device disappeared, restore prior module parameters, and remove exact runtime state",
		}
		if len(r.Endpoints) == 0 {
			p.Summary, p.CanApply = "no local endpoint exists; cleanup is already complete", true
		} else {
			e := matchingEndpoint(r, o.Name)
			if e == nil {
				p.Blockers = append(p.Blockers, "requested endpoint name does not match the discovered endpoint")
			} else {
				if !e.HolderScanComplete {
					p.Blockers = append(p.Blockers, "holder scan was incomplete; rerun the cleanup preview as root")
				}
				for _, h := range e.Holders {
					p.Blockers = append(p.Blockers, fmt.Sprintf("device held by pid=%d command=%s fd=%s", h.PID, h.Command, h.FD))
				}
			}
			if _, err := readState(o); err != nil && !o.Adopt {
				p.Blockers = append(p.Blockers, "endpoint has no CiruStrixLink state; use --adopt to clean a reviewed legacy endpoint")
			}
			p.CanApply = len(p.Blockers) == 0
			if p.CanApply {
				p.Summary = "exact local endpoint cleanup is available; coordinate cleanup on both peers"
			} else {
				p.Summary = "cleanup is blocked until exact holders or ownership are resolved"
			}
		}
	}
	return p, nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func waitUntil(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fn()
}

func writeConfig(path string, value int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(value)+"\n"), 0o644)
}

func removeConfig(cfg, serviceDir, device string, timeout time.Duration) error {
	root := "/sys/kernel/config/thunderbolt/stream"
	cleanCfg := filepath.Clean(cfg)
	cleanService := filepath.Clean(serviceDir)
	if !strings.HasPrefix(cleanCfg, root+string(os.PathSeparator)) || filepath.Dir(cleanCfg) != cleanService || filepath.Dir(cleanService) != root {
		return fmt.Errorf("refusing unsafe config paths %s / %s", cfg, serviceDir)
	}
	if _, err := os.Stat(cleanCfg); err == nil {
		if err := writeConfig(filepath.Join(cleanCfg, "in_hopid"), 0); err != nil {
			return err
		}
		if err := writeConfig(filepath.Join(cleanCfg, "out_hopid"), 0); err != nil {
			return err
		}
		if err := os.Remove(cleanCfg); err != nil {
			return err
		}
	}
	if entries, err := os.ReadDir(cleanService); err == nil && len(entries) == 0 {
		if err := os.Remove(cleanService); err != nil {
			return err
		}
	}
	if device != "" && !waitUntil(timeout, func() bool { _, err := os.Stat(device); return os.IsNotExist(err) }) {
		return fmt.Errorf("device did not disappear: %s", device)
	}
	return nil
}

func restoreModule(s endpointState) error {
	if s.Adopted {
		return nil
	}
	if !s.ModuleWasLoaded {
		return runCommand("modprobe", "-r", "thunderbolt_stream")
	}
	currentDMABuf := readTrim("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_dmabuf")
	currentKick := readTrim("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_kick")
	if currentDMABuf == s.PreviousDMABuf && currentKick == s.PreviousKick {
		return nil
	}
	if readInt("/sys/module/thunderbolt_stream/refcnt") != 0 {
		return errors.New("cannot restore thunderbolt_stream parameters while the module has references")
	}
	if err := runCommand("modprobe", "-r", "thunderbolt_stream"); err != nil {
		return err
	}
	dmabuf := mapBool(s.PreviousDMABuf == "Y", "1", "0")
	kick := mapBool(s.PreviousKick == "Y", "1", "0")
	return runCommand("modprobe", "thunderbolt_stream", "zc_diagnostic_kick="+kick, "zc_diagnostic_dmabuf="+dmabuf)
}

func prepareEndpoint(o EndpointOptions) error {
	initial := Inspect(o.ToolVersion, o.Interface, o.Peer)
	if !initial.Portable.Ready {
		return errors.New("portable network gate failed immediately before NHI prepare")
	}
	if e := matchingEndpoint(initial, o.Name); e != nil && e.ProductionFit {
		if _, err := readState(o); err == nil {
			return nil
		}
		if !o.Adopt {
			return errors.New("exact endpoint exists without state; rerun reviewed plan with --adopt")
		}
		return writeState(o, endpointState{SchemaVersion: 1, CreatedAt: time.Now().UTC(), Name: e.Name, Service: e.Service, ConfigPath: e.ConfigPath, Device: e.Device, Ring: e.RingSize, Throttling: e.ThrottlingNS, ModuleWasLoaded: true, PreviousDMABuf: readTrim("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_dmabuf"), PreviousKick: readTrim("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_kick"), Adopted: true})
	}
	if len(initial.Endpoints) != 0 {
		return errors.New("existing endpoint state must be cleaned before prepare")
	}

	moduleWasLoaded := false
	if _, err := os.Stat("/sys/module/thunderbolt_stream"); err == nil {
		moduleWasLoaded = true
	}
	previousDMABuf, previousKick := "absent", "absent"
	if moduleWasLoaded {
		previousDMABuf = readTrim("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_dmabuf")
		previousKick = readTrim("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_kick")
		if previousDMABuf != "Y" || previousKick != "N" {
			if readInt("/sys/module/thunderbolt_stream/refcnt") != 0 {
				return errors.New("thunderbolt_stream has active references with incompatible parameters")
			}
			if err := runCommand("modprobe", "-r", "thunderbolt_stream"); err != nil {
				return err
			}
		}
	}
	if _, err := os.Stat("/sys/module/thunderbolt_stream"); err != nil {
		if err := runCommand("modprobe", "thunderbolt_stream", "zc_diagnostic_kick=0", "zc_diagnostic_dmabuf=1"); err != nil {
			return err
		}
	}
	stateForRestore := endpointState{ModuleWasLoaded: moduleWasLoaded, PreviousDMABuf: previousDMABuf, PreviousKick: previousKick}
	prepared := false
	var cfg, serviceDir, device string
	defer func() {
		if prepared {
			return
		}
		if cfg != "" {
			_ = removeConfig(cfg, serviceDir, device, 5*time.Second)
		}
		_ = os.Remove(statePath(o))
		if entries, err := os.ReadDir(o.StateDir); err == nil && len(entries) == 0 {
			_ = os.Remove(o.StateDir)
		}
		_ = restoreModule(stateForRestore)
	}()

	afterLoad := Inspect(o.ToolVersion, o.Interface, o.Peer)
	if !afterLoad.Portable.Ready {
		return errors.New("portable network gate failed after loading thunderbolt_stream; module was rolled back")
	}
	var streamService string
	if !waitUntil(o.Timeout, func() bool {
		ss := servicesWith("stream", "thunderbolt_stream")
		if len(ss) == 1 {
			streamService = ss[0].Name
			return true
		}
		return false
	}) {
		return errors.New("exactly one dynamic stream service was not discovered")
	}
	base := "/sys/kernel/config/thunderbolt/stream"
	serviceDir = filepath.Join(base, streamService)
	cfg = filepath.Join(serviceDir, o.Name)
	if err := os.Mkdir(serviceDir, 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(cfg, 0o755); err != nil {
		_ = os.Remove(serviceDir)
		return err
	}
	if err := writeConfig(filepath.Join(cfg, "ring_size"), o.Ring); err != nil {
		return err
	}
	if err := writeConfig(filepath.Join(cfg, "throttling"), o.Throttling); err != nil {
		return err
	}
	if err := writeConfig(filepath.Join(cfg, "in_hopid"), -1); err != nil {
		return err
	}
	if err := writeConfig(filepath.Join(cfg, "out_hopid"), -1); err != nil {
		return err
	}
	if !waitUntil(o.Timeout, func() bool {
		return readInt(filepath.Join(cfg, "in_hopid")) == ExpectedNHIHopID && readInt(filepath.Join(cfg, "out_hopid")) == ExpectedNHIHopID
	}) {
		return errors.New("endpoint did not reach HopID 9/9; partial endpoint was rolled back")
	}
	index := readInt(filepath.Join(cfg, "index"))
	device = fmt.Sprintf("/dev/tbstream%d", index)
	if !waitUntil(10*time.Second, func() bool { return charDevice(device) }) {
		return fmt.Errorf("endpoint character device did not appear: %s", device)
	}
	group, err := user.LookupGroup(o.DeviceGroup)
	if err != nil {
		return fmt.Errorf("device group %q: %w", o.DeviceGroup, err)
	}
	gid, _ := strconv.Atoi(group.Gid)
	if err := os.Chown(device, -1, gid); err != nil {
		return err
	}
	if err := os.Chmod(device, 0o660); err != nil {
		return err
	}
	s := endpointState{SchemaVersion: 1, CreatedAt: time.Now().UTC(), Name: o.Name, Service: streamService, ConfigPath: cfg, Device: device, Ring: o.Ring, Throttling: o.Throttling, ModuleWasLoaded: moduleWasLoaded, PreviousDMABuf: previousDMABuf, PreviousKick: previousKick}
	if err := writeState(o, s); err != nil {
		return err
	}
	final := Inspect(o.ToolVersion, o.Interface, o.Peer)
	e := matchingEndpoint(final, o.Name)
	if !final.Portable.Ready || e == nil || !e.ProductionFit {
		return errors.New("final pair-local validation failed; endpoint was rolled back")
	}
	prepared = true
	return nil
}

func cleanupEndpoint(o EndpointOptions) error {
	r := Inspect(o.ToolVersion, o.Interface, o.Peer)
	if len(r.Endpoints) == 0 {
		if _, err := os.Stat(statePath(o)); err == nil {
			_ = os.Remove(statePath(o))
			_ = os.Remove(o.StateDir)
		}
		return nil
	}
	e := matchingEndpoint(r, o.Name)
	if e == nil {
		return errors.New("requested endpoint name does not match discovered config")
	}
	if !e.HolderScanComplete {
		return errors.New("refusing cleanup because the holder scan was incomplete")
	}
	if len(e.Holders) > 0 {
		return fmt.Errorf("refusing cleanup: %s is held by pid %d (%s)", e.Device, e.Holders[0].PID, e.Holders[0].Command)
	}
	s, stateErr := readState(o)
	if stateErr != nil {
		if !o.Adopt {
			return errors.New("endpoint has no CiruStrixLink state; use --adopt after reviewing the cleanup plan")
		}
		s = endpointState{SchemaVersion: 1, Name: e.Name, Service: e.Service, ConfigPath: e.ConfigPath, Device: e.Device, Ring: e.RingSize, Throttling: e.ThrottlingNS, ModuleWasLoaded: true, PreviousDMABuf: readTrim("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_dmabuf"), PreviousKick: readTrim("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_kick"), Adopted: true}
	}
	if s.ConfigPath != e.ConfigPath || s.Device != e.Device || s.Name != e.Name {
		return errors.New("recorded state does not exactly match the discovered endpoint")
	}
	if err := removeConfig(e.ConfigPath, filepath.Dir(e.ConfigPath), e.Device, o.Timeout); err != nil {
		return err
	}
	if err := restoreModule(s); err != nil {
		return err
	}
	if _, err := os.Stat(statePath(o)); err == nil {
		if err := os.Remove(statePath(o)); err != nil {
			return err
		}
	}
	if entries, err := os.ReadDir(o.StateDir); err == nil && len(entries) == 0 {
		if err := os.Remove(o.StateDir); err != nil {
			return err
		}
	}
	return nil
}

func ApplyEndpoint(p EndpointPlan) error {
	if !p.CanApply {
		return errors.New("endpoint plan has blockers")
	}
	if !isRoot() {
		return errors.New("endpoint lifecycle changes require root; rerun the reviewed plan with sudo and --apply")
	}
	lock, err := acquireFileLock("/run/lock/ciru-strixlink-nhi.lock")
	if err != nil {
		return err
	}
	defer releaseFileLock(lock)
	switch p.Action {
	case "prepare":
		return prepareEndpoint(p.Options)
	case "cleanup":
		return cleanupEndpoint(p.Options)
	default:
		return fmt.Errorf("unknown endpoint action %q", p.Action)
	}
}
