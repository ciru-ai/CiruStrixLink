// Package transport discovers the portable USB4NET baseline and optional
// USB4STREAM/NHI acceleration without changing host state.
package transport

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/link"
)

const (
	SchemaVersion    = 1
	ExpectedRing     = 4095
	ExpectedThrottle = 8192
	ExpectedNetHopID = 8
	ExpectedNHIHopID = 9
	DocsURL          = "https://github.com/ciru-ai/CiruStrixLink/blob/main/docs/transports.md"
)

type Check struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Detected string `json:"detected,omitempty"`
	Expected string `json:"expected,omitempty"`
	HelpURL  string `json:"help_url,omitempty"`
}

type Holder struct {
	PID         int    `json:"pid"`
	Command     string `json:"command"`
	HasRawIOCap bool   `json:"has_cap_sys_rawio"`
	FD          string `json:"fd"`
}

type Endpoint struct {
	Service            string   `json:"service"`
	Name               string   `json:"name"`
	ConfigPath         string   `json:"config_path"`
	RingSize           int      `json:"ring_size"`
	ThrottlingNS       int      `json:"throttling_ns"`
	InHopID            int      `json:"in_hopid"`
	OutHopID           int      `json:"out_hopid"`
	Index              int      `json:"index"`
	Device             string   `json:"device"`
	DevicePresent      bool     `json:"device_present"`
	ProductionFit      bool     `json:"production_profile_match"`
	HolderScanComplete bool     `json:"holder_scan_complete"`
	Holders            []Holder `json:"holders,omitempty"`
}

type Mode struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Status  string  `json:"status"`
	Ready   bool    `json:"ready"`
	Summary string  `json:"summary"`
	Checks  []Check `json:"checks"`
}

type LifecycleStep struct {
	Order   int    `json:"order"`
	ID      string `json:"id"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type CleanupPlan struct {
	Required         bool     `json:"required"`
	BlockedByHolders bool     `json:"blocked_by_holders"`
	ConfigPaths      []string `json:"config_paths,omitempty"`
	DevicePaths      []string `json:"device_paths,omitempty"`
	Steps            []string `json:"steps,omitempty"`
}

type Fallback struct {
	Mode            string `json:"mode"`
	Available       bool   `json:"available"`
	Required        bool   `json:"required"`
	Reason          string `json:"reason"`
	CleanupRequired bool   `json:"cleanup_required"`
}

type Report struct {
	SchemaVersion int             `json:"schema_version"`
	ToolVersion   string          `json:"tool_version"`
	GeneratedAt   time.Time       `json:"generated_at"`
	Hostname      string          `json:"hostname"`
	Kernel        string          `json:"kernel"`
	Interface     string          `json:"interface,omitempty"`
	LocalAddress  string          `json:"local_address,omitempty"`
	Peer          string          `json:"peer,omitempty"`
	Portable      Mode            `json:"portable"`
	NHI           Mode            `json:"nhi"`
	Endpoints     []Endpoint      `json:"endpoints,omitempty"`
	Lifecycle     []LifecycleStep `json:"lifecycle"`
	LocalArmReady bool            `json:"local_arm_ready"`
	Cleanup       CleanupPlan     `json:"cleanup"`
	Fallback      Fallback        `json:"fallback"`
}

type PairReport struct {
	SchemaVersion     int       `json:"schema_version"`
	GeneratedAt       time.Time `json:"generated_at"`
	HostA             string    `json:"host_a"`
	HostB             string    `json:"host_b"`
	PairIdentityValid bool      `json:"pair_identity_valid"`
	PortableReady     bool      `json:"portable_ready"`
	NHIStatus         string    `json:"nhi_status"`
	NHIReady          bool      `json:"nhi_ready"`
	NHIInUse          bool      `json:"nhi_in_use"`
	LeaseAvailable    bool      `json:"lease_available"`
	ArmAllowed        bool      `json:"arm_allowed"`
	Summary           string    `json:"summary"`
	Fallback          Fallback  `json:"fallback"`
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(path string) int {
	n, _ := strconv.Atoi(readTrim(path))
	return n
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func commandOK(name string, args ...string) bool {
	return exec.Command(name, args...).Run() == nil
}

func kernelRelease() string {
	b, _ := exec.Command("uname", "-r").Output()
	return strings.TrimSpace(string(b))
}

func moduleAvailable(name string) bool {
	underscore := strings.ReplaceAll(name, "-", "_")
	if _, err := os.Stat(filepath.Join("/sys/module", underscore)); err == nil {
		return true
	}
	if commandExists("modinfo") && (commandOK("modinfo", name) || commandOK("modinfo", underscore)) {
		return true
	}
	return false
}

func driverName(deviceDir string) string {
	p, err := filepath.EvalSymlinks(filepath.Join(deviceDir, "driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(p)
}

type service struct {
	Name   string
	Key    string
	Driver string
}

func services() []service {
	keys, _ := filepath.Glob("/sys/bus/thunderbolt/devices/*/key")
	var out []service
	for _, keyPath := range keys {
		dir := filepath.Dir(keyPath)
		out = append(out, service{Name: filepath.Base(dir), Key: readTrim(keyPath), Driver: driverName(dir)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func servicesWith(key, driver string) []service {
	var out []service
	for _, s := range services() {
		if s.Key == key && s.Driver == driver {
			out = append(out, s)
		}
	}
	return out
}

func hasRawIO(pid int) bool {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if !strings.HasPrefix(s.Text(), "CapEff:") {
			continue
		}
		fields := strings.Fields(s.Text())
		if len(fields) != 2 {
			return false
		}
		value, err := strconv.ParseUint(fields[1], 16, 64)
		return err == nil && value&(uint64(1)<<17) != 0
	}
	return false
}

func holders(device string) ([]Holder, bool) {
	pids, _ := filepath.Glob("/proc/[0-9]*")
	var out []Holder
	complete := true
	for _, proc := range pids {
		pid, err := strconv.Atoi(filepath.Base(proc))
		if err != nil {
			continue
		}
		fdDir := filepath.Join(proc, "fd")
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			if os.IsPermission(err) {
				complete = false
			}
			continue
		}
		for _, entry := range entries {
			fd := filepath.Join(fdDir, entry.Name())
			target, err := os.Readlink(fd)
			if err != nil {
				if os.IsPermission(err) {
					complete = false
				}
				continue
			}
			if target != device {
				continue
			}
			command := readTrim(filepath.Join(proc, "comm"))
			out = append(out, Holder{PID: pid, Command: command, HasRawIOCap: hasRawIO(pid), FD: fd})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PID == out[j].PID {
			return out[i].FD < out[j].FD
		}
		return out[i].PID < out[j].PID
	})
	return out, complete
}

func charDevice(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func endpoints() []Endpoint {
	configs, _ := filepath.Glob("/sys/kernel/config/thunderbolt/stream/*/*")
	var out []Endpoint
	for _, cfg := range configs {
		info, err := os.Stat(cfg)
		if err != nil || !info.IsDir() {
			continue
		}
		e := Endpoint{
			Service: filepath.Base(filepath.Dir(cfg)), Name: filepath.Base(cfg), ConfigPath: cfg,
			RingSize: readInt(filepath.Join(cfg, "ring_size")), ThrottlingNS: readInt(filepath.Join(cfg, "throttling")),
			InHopID: readInt(filepath.Join(cfg, "in_hopid")), OutHopID: readInt(filepath.Join(cfg, "out_hopid")),
			Index: readInt(filepath.Join(cfg, "index")),
		}
		e.Device = fmt.Sprintf("/dev/tbstream%d", e.Index)
		e.DevicePresent = charDevice(e.Device)
		e.ProductionFit = e.RingSize == ExpectedRing && e.ThrottlingNS == ExpectedThrottle && e.InHopID == ExpectedNHIHopID && e.OutHopID == ExpectedNHIHopID && e.DevicePresent
		e.Holders, e.HolderScanComplete = holders(e.Device)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConfigPath < out[j].ConfigPath })
	return out
}

func portableStatus(ifaceName, peer string) (Mode, string) {
	m := Mode{ID: "portable", Label: "Portable USB4NET socket/RCCL baseline"}
	iface, err := link.SelectInterface(ifaceName)
	if err != nil {
		m.Status, m.Summary = "unavailable", err.Error()
		m.Checks = append(m.Checks, Check{ID: "usb4net_interface", Status: "failed", Summary: err.Error(), HelpURL: DocsURL})
		return m, ""
	}
	carrier := readTrim(filepath.Join("/sys/class/net", iface.Name, "carrier"))
	linkOK := iface.State == "up" && carrier == "1"
	m.Checks = append(m.Checks, Check{ID: "carrier", Status: boolStatus(linkOK), Summary: "thunderbolt-net must have carrier before NHI is considered", Detected: "state=" + iface.State + " carrier=" + carrier, Expected: "state=up carrier=1", HelpURL: DocsURL})
	networkServices := servicesWith("network", "thunderbolt-net")
	networkOK := len(networkServices) == 1
	detectedService := ""
	if networkOK {
		detectedService = networkServices[0].Name
	}
	m.Checks = append(m.Checks, Check{ID: "network_service", Status: boolStatus(networkOK), Summary: "exactly one service with key=network must remain bound to thunderbolt-net", Detected: detectedService, Expected: fmt.Sprintf("one network service; network reserves HopID %d", ExpectedNetHopID), HelpURL: DocsURL})

	peerOK := peer == ""
	if peer != "" {
		route, routeErr := link.RouteTo(peer, iface.Name)
		routeOK := routeErr == nil && route.InterfaceMatch
		m.Checks = append(m.Checks, Check{ID: "peer_route", Status: boolStatus(routeOK), Summary: "peer route must stay on the USB4NET interface", Detected: route.Device, Expected: iface.Name, HelpURL: DocsURL})
		pingOK := commandOK("ping", "-I", iface.Name, "-c", "1", "-W", "1", peer)
		m.Checks = append(m.Checks, Check{ID: "peer_reachability", Status: boolStatus(pingOK), Summary: "peer must respond before thunderbolt_stream is loaded or armed", Detected: peer, Expected: "reachable", HelpURL: DocsURL})
		peerOK = routeOK && pingOK
	}
	if linkOK && networkOK && peerOK {
		m.Ready = true
		if peer == "" {
			m.Status, m.Summary = "local_ready", "local USB4NET baseline is ready; peer reachability was not requested"
		} else {
			m.Status, m.Summary = "ready", "USB4NET carrier, network service, route, and peer reachability passed"
		}
	} else {
		m.Status, m.Summary = "blocked", "portable baseline is not ready; NHI arming is forbidden"
	}
	return m, iface.Name
}

func boolStatus(ok bool) string {
	if ok {
		return "passed"
	}
	return "failed"
}

func nhiStatus(portable Mode) (Mode, []Endpoint, CleanupPlan, bool) {
	m := Mode{ID: "nhi", Label: "Accelerated USB4STREAM/NHI mode"}
	loaded := false
	if _, err := os.Stat("/sys/module/thunderbolt_stream"); err == nil {
		loaded = true
	}
	present := loaded || moduleAvailable("thunderbolt_stream")
	m.Checks = append(m.Checks, Check{ID: "stream_module", Status: boolStatus(present), Summary: "thunderbolt_stream must be supported by the running kernel and loaded only after the network gate", Detected: mapBool(loaded, "loaded", mapBool(present, "available", "absent")), HelpURL: DocsURL})

	baseOK := false
	if info, err := os.Stat("/sys/kernel/config/thunderbolt/stream"); err == nil && info.IsDir() {
		baseOK = true
	}
	m.Checks = append(m.Checks, Check{ID: "stream_configfs", Status: boolStatus(baseOK), Summary: "USB4STREAM configfs must be mounted and available", Detected: mapBool(baseOK, "/sys/kernel/config/thunderbolt/stream", "absent"), HelpURL: DocsURL})

	streamServices := servicesWith("stream", "thunderbolt_stream")
	serviceOK := len(streamServices) == 1
	serviceName := ""
	if serviceOK {
		serviceName = streamServices[0].Name
	}
	m.Checks = append(m.Checks, Check{ID: "stream_service", Status: mapBool(!loaded, "not_applicable", boolStatus(serviceOK)), Summary: "discover dynamically the single service whose key is stream and driver is thunderbolt_stream", Detected: serviceName, Expected: "exactly one dynamic stream service", HelpURL: DocsURL})

	dmabuf, kick := "", ""
	paramsReadable := !loaded
	if loaded {
		dmabufBytes, dmabufErr := os.ReadFile("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_dmabuf")
		kickBytes, kickErr := os.ReadFile("/sys/module/thunderbolt_stream/parameters/zc_diagnostic_kick")
		paramsReadable = dmabufErr == nil && kickErr == nil
		dmabuf = strings.TrimSpace(string(dmabufBytes))
		kick = strings.TrimSpace(string(kickBytes))
	}
	paramsOK := !loaded || (paramsReadable && dmabuf == "Y" && kick == "N")
	paramsStatus := boolStatus(paramsOK)
	paramsDetected := "zc_diagnostic_dmabuf=" + dmabuf + " zc_diagnostic_kick=" + kick
	if loaded && !paramsReadable {
		paramsStatus = "unknown"
		paramsDetected = "root-readable only; rerun status with sudo to qualify NHI"
	}
	m.Checks = append(m.Checks, Check{ID: "module_parameters", Status: paramsStatus, Summary: "production NHI requires DMA-BUF mode and diagnostic kick disabled", Detected: paramsDetected, Expected: "Y / N", HelpURL: DocsURL})

	eps := endpoints()
	exact := len(eps) == 1 && eps[0].ProductionFit
	partial := len(eps) > 0 && !exact
	endpointDetected := "absent"
	if len(eps) > 0 {
		parts := make([]string, 0, len(eps))
		for _, e := range eps {
			parts = append(parts, fmt.Sprintf("%s ring=%d throttle=%d hop=%d/%d device=%s", e.ConfigPath, e.RingSize, e.ThrottlingNS, e.InHopID, e.OutHopID, e.Device))
		}
		endpointDetected = strings.Join(parts, "; ")
	}
	m.Checks = append(m.Checks, Check{ID: "endpoint", Status: mapBool(len(eps) == 0, "not_armed", boolStatus(exact)), Summary: "armed endpoints must use the validated profile and expose a character device", Detected: endpointDetected, Expected: fmt.Sprintf("ring=%d throttle=%d hop=%d/%d device=/dev/tbstreamN", ExpectedRing, ExpectedThrottle, ExpectedNHIHopID, ExpectedNHIHopID), HelpURL: DocsURL})

	cleanup := CleanupPlan{}
	for _, e := range eps {
		cleanup.ConfigPaths = append(cleanup.ConfigPaths, e.ConfigPath)
		cleanup.DevicePaths = append(cleanup.DevicePaths, e.Device)
		if len(e.Holders) > 0 {
			cleanup.BlockedByHolders = true
		}
	}
	cleanup.Required = partial || (loaded && paramsReadable && !paramsOK && len(eps) > 0)
	if cleanup.Required {
		cleanup.Steps = []string{
			"stop the exact processes holding /dev/tbstreamN; never remove a live endpoint",
			"on both peers, write 0 to in_hopid and out_hopid for only the discovered endpoint",
			"remove the endpoint directory, then its now-empty dynamic service directory",
			"verify /dev/tbstreamN is gone on both peers before falling back or retrying",
		}
	}

	localArmReady := portable.Ready && present && len(eps) == 0 && paramsOK
	switch {
	case !present:
		m.Status, m.Summary = "unavailable", "thunderbolt_stream is not available; use the portable baseline"
	case loaded && !paramsReadable:
		m.Status, m.Summary = "needs_privilege", "NHI state is present but root access is required to qualify module parameters and holders"
	case partial || !paramsOK:
		m.Status, m.Summary = "partial", "partial or mismatched NHI state requires coordinated cleanup on both peers"
	case exact:
		m.Status, m.Summary, m.Ready = "local_ready", "local endpoint matches the validated production profile; peer reconciliation is still required", true
	case localArmReady:
		m.Status, m.Summary = "available", "NHI capability is available and unarmed; pair reconciliation must authorize arming"
	case !portable.Ready:
		m.Status, m.Summary = "blocked", "portable network gate has not passed; thunderbolt_stream must not be armed"
	default:
		m.Status, m.Summary = "inactive", "NHI capability is present but not ready to arm"
	}
	return m, eps, cleanup, localArmReady
}

func mapBool(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

// Inspect performs a bounded, read-only local inspection.
func Inspect(toolVersion, iface, peer string) Report {
	host, _ := os.Hostname()
	portable, selected := portableStatus(iface, peer)
	nhi, eps, cleanup, armReady := nhiStatus(portable)
	r := Report{
		SchemaVersion: SchemaVersion, ToolVersion: toolVersion, GeneratedAt: time.Now().UTC(), Hostname: host,
		Kernel: kernelRelease(), Interface: selected, LocalAddress: InterfaceIPv4(selected), Peer: peer, Portable: portable, NHI: nhi,
		Endpoints: eps, LocalArmReady: armReady, Cleanup: cleanup,
	}
	r.Lifecycle = []LifecycleStep{
		{Order: 1, ID: "network_first", Status: mapBool(portable.Ready, "passed", "blocked"), Summary: fmt.Sprintf("initialize thunderbolt-net first; it must retain HopID %d, carrier, route, and peer reachability", ExpectedNetHopID)},
		{Order: 2, ID: "load_stream_after_gate", Status: mapBool(portable.Ready, "eligible", "forbidden"), Summary: "only after step 1 may thunderbolt_stream be loaded with detected supported parameters"},
		{Order: 3, ID: "discover_stream_service", Status: checkStatus(nhi.Checks, "stream_service"), Summary: "discover the dynamic service whose key is stream; never hardcode its service ID"},
		{Order: 4, ID: "coordinate_pair_arm", Status: "peer_required", Summary: fmt.Sprintf("arm both peers as one transaction and require HopID %d/%d on both", ExpectedNHIHopID, ExpectedNHIHopID)},
		{Order: 5, ID: "runtime_capability", Status: "required", Summary: "grant only CAP_SYS_RAWIO to the model runtime that imports the DMA-BUF"},
		{Order: 6, ID: "fallback", Status: mapBool(portable.Ready, "available", "blocked"), Summary: "on any one-sided or partial failure, clean both endpoints and select portable RCCL/socket mode"},
	}
	r.Fallback = Fallback{Mode: "portable", Available: portable.Ready, Required: cleanup.Required || nhi.Status == "unavailable" || nhi.Status == "blocked" || nhi.Status == "partial", CleanupRequired: cleanup.Required}
	if r.Fallback.Required {
		r.Fallback.Reason = nhi.Summary
	} else {
		r.Fallback.Reason = "portable mode remains the safe baseline"
	}
	return r
}

func checkStatus(checks []Check, id string) string {
	for _, c := range checks {
		if c.ID == id {
			return c.Status
		}
	}
	return "unknown"
}

func endpointExact(r Report) bool {
	return len(r.Endpoints) == 1 && r.Endpoints[0].ProductionFit
}

func hasPartial(r Report) bool {
	return r.Cleanup.Required || (len(r.Endpoints) > 0 && !endpointExact(r))
}

func endpointInUse(r Report) bool {
	for _, e := range r.Endpoints {
		if len(e.Holders) > 0 {
			return true
		}
	}
	return false
}

// Reconcile makes the two-sided decision. No single-host report can authorize
// NHI arming or claim pair readiness.
func Reconcile(a, b Report) PairReport {
	p := PairReport{SchemaVersion: SchemaVersion, GeneratedAt: time.Now().UTC(), HostA: a.Hostname, HostB: b.Hostname}
	p.PairIdentityValid = a.Hostname != "" && b.Hostname != "" && a.Hostname != b.Hostname && a.LocalAddress != "" && b.LocalAddress != "" && a.Peer == b.LocalAddress && b.Peer == a.LocalAddress
	p.PortableReady = p.PairIdentityValid && a.Portable.Ready && b.Portable.Ready
	aExact, bExact := endpointExact(a), endpointExact(b)
	partial := hasPartial(a) || hasPartial(b) || aExact != bExact
	bothUnarmed := len(a.Endpoints) == 0 && len(b.Endpoints) == 0
	p.ArmAllowed = p.PortableReady && a.LocalArmReady && b.LocalArmReady && bothUnarmed
	switch {
	case !p.PairIdentityValid:
		p.NHIStatus, p.Summary = "invalid_pair", "reports are not reciprocal endpoints; refuse arming, cleanup, or fallback decisions for the wrong pair"
	case partial:
		p.NHIStatus, p.Summary = "partial", "the peers do not have one matching endpoint each; clean both sides before retry or fallback"
	case aExact && bExact:
		ae, be := a.Endpoints[0], b.Endpoints[0]
		matched := a.NHI.Ready && b.NHI.Ready && ae.RingSize == be.RingSize && ae.ThrottlingNS == be.ThrottlingNS && ae.InHopID == ExpectedNHIHopID && ae.OutHopID == ExpectedNHIHopID && be.InHopID == ExpectedNHIHopID && be.OutHopID == ExpectedNHIHopID
		if matched && p.PortableReady {
			p.NHIReady = true
			p.NHIInUse = endpointInUse(a) || endpointInUse(b)
			p.LeaseAvailable = !p.NHIInUse
			if p.NHIInUse {
				p.NHIStatus, p.Summary = "in_use", "both endpoints are qualified at HopID 9/9, but an existing process holds the exclusive NHI pair"
			} else {
				p.NHIStatus, p.Summary = "ready", "both endpoints match at HopID 9/9 after the portable network gate and the exclusive lease is available"
			}
		} else {
			p.NHIStatus, p.Summary = "blocked", "endpoint parameters or portable network readiness do not match"
		}
	case p.ArmAllowed:
		p.NHIStatus, p.Summary = "arm_allowed", "both peers passed the portable gate and report unarmed NHI capability; a coordinator may arm them as one transaction"
	case bothUnarmed:
		p.NHIStatus, p.Summary = "unavailable", "one or both peers cannot arm NHI; use the portable baseline"
	default:
		p.NHIStatus, p.Summary = "blocked", "pair state is not safe for NHI"
	}
	p.Fallback = Fallback{Mode: "portable", Available: p.PortableReady, Required: !p.NHIReady || p.NHIInUse, CleanupRequired: partial, Reason: p.Summary}
	return p
}

func LoadReport(path string) (Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return Report{}, err
	}
	if r.SchemaVersion != SchemaVersion {
		return Report{}, fmt.Errorf("unsupported transport schema %d", r.SchemaVersion)
	}
	return r, nil
}

// InterfaceIPv4 returns the first non-link-local IPv4 address for display.
func InterfaceIPv4(name string) string {
	i, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, _ := i.Addrs()
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		if err == nil && ip.To4() != nil && !ip.IsLinkLocalUnicast() {
			return ip.String()
		}
	}
	return ""
}
