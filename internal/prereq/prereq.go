// Package prereq exposes a versioned, UI-safe view of host prerequisites.
package prereq

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/link"
)

const (
	SchemaVersion = 1
	DocsBase      = "https://github.com/ciru-ai/CiruStrixLink/blob/main/docs/install/"
)

type Status string

const (
	Available   Status = "available"
	Missing     Status = "missing"
	Inactive    Status = "inactive"
	NotDetected Status = "not_detected"
	Unsupported Status = "unsupported"
	Unknown     Status = "unknown"
)

type Component struct {
	ID               string `json:"id"`
	Label            string `json:"label"`
	Required         bool   `json:"required"`
	Status           Status `json:"status"`
	Summary          string `json:"summary"`
	Detected         string `json:"detected,omitempty"`
	CanAutoFix       bool   `json:"can_auto_fix"`
	SuggestedCommand string `json:"suggested_command,omitempty"`
	HelpURL          string `json:"help_url"`
}

type System struct {
	Hostname       string `json:"hostname"`
	OSID           string `json:"os_id,omitempty"`
	OSName         string `json:"os_name,omitempty"`
	OSVersion      string `json:"os_version,omitempty"`
	Kernel         string `json:"kernel,omitempty"`
	Architecture   string `json:"architecture"`
	PackageManager string `json:"package_manager,omitempty"`
}

type Report struct {
	SchemaVersion int         `json:"schema_version"`
	ToolVersion   string      `json:"tool_version"`
	GeneratedAt   time.Time   `json:"generated_at"`
	OverallStatus string      `json:"overall_status"`
	Ready         bool        `json:"ready"`
	System        System      `json:"system"`
	Components    []Component `json:"components"`
	NextCommand   string      `json:"next_command,omitempty"`
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func command(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

func output(name string, args ...string) string {
	b, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func parseOSRelease(data string) map[string]string {
	out := make(map[string]string)
	s := bufio.NewScanner(strings.NewReader(data))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		out[strings.TrimSpace(key)] = value
	}
	return out
}

func osRelease() map[string]string {
	b, _ := os.ReadFile("/etc/os-release")
	return parseOSRelease(string(b))
}

func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if key, value, ok := strings.Cut(s.Text(), ":"); ok && strings.TrimSpace(key) == "model name" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func packageManager(osID string) (string, string, bool) {
	candidates := []struct{ command, label, doc string }{
		{"nixos-rebuild", "nixos", "nixos.md"},
		{"apt-get", "apt", "ubuntu-debian.md"},
		{"dnf", "dnf", "fedora-rhel.md"},
		{"pacman", "pacman", "arch.md"},
		{"zypper", "zypper", "package-managers.md"},
	}
	for _, c := range candidates {
		if _, ok := command(c.command); ok {
			return c.label, DocsBase + c.doc, true
		}
	}
	switch strings.ToLower(osID) {
	case "nixos":
		return "nixos", DocsBase + "nixos.md", false
	case "ubuntu", "debian":
		return "apt", DocsBase + "ubuntu-debian.md", false
	case "fedora", "rhel", "centos":
		return "dnf", DocsBase + "fedora-rhel.md", false
	case "arch", "manjaro":
		return "pacman", DocsBase + "arch.md", false
	default:
		return "", DocsBase + "package-managers.md", false
	}
}

func moduleBuiltinName(kernel, name string) bool {
	if kernel == "" {
		return false
	}
	paths := []string{
		filepath.Join("/lib/modules", kernel, "modules.builtin"),
		filepath.Join("/usr/lib/modules", kernel, "modules.builtin"),
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		underscore := strings.ReplaceAll(name, "-", "_")
		hyphen := strings.ReplaceAll(name, "_", "-")
		if err == nil && (strings.Contains(string(b), underscore+".ko") || strings.Contains(string(b), hyphen+".ko")) {
			return true
		}
	}
	return false
}

func moduleAvailable(kernel string) bool {
	return moduleAvailableName(kernel, "thunderbolt_net")
}

func moduleAvailableName(kernel, name string) bool {
	underscore := strings.ReplaceAll(name, "-", "_")
	if _, ok := command("modinfo"); ok {
		if exec.Command("modinfo", name).Run() == nil || exec.Command("modinfo", underscore).Run() == nil {
			return true
		}
	}
	return moduleBuiltinName(kernel, name)
}

func addCommandComponent(items *[]Component, id, label, executable, versionArg, help string, required bool) {
	path, ok := command(executable)
	c := Component{ID: id, Label: label, Required: required, HelpURL: DocsBase + help}
	if !ok {
		c.Status = Missing
		c.Summary = executable + " is not installed or not on PATH"
	} else {
		c.Status = Available
		c.Summary = executable + " is available"
		c.Detected = path
		if versionArg != "" {
			if v := firstLine(output(executable, versionArg)); v != "" {
				c.Detected = v
			}
		}
	}
	*items = append(*items, c)
}

func summarize(r *Report) {
	r.Ready = true
	r.OverallStatus = "ready"
	for _, c := range r.Components {
		if !c.Required || c.Status == Available {
			continue
		}
		r.Ready = false
		if c.Status == Unsupported {
			r.OverallStatus = "unsupported"
		} else if r.OverallStatus != "unsupported" {
			r.OverallStatus = "needs_action"
		}
	}
	if r.Ready {
		r.NextCommand = "choose this peer's endpoint role, then run: ciru-strixlink setup --role a|b"
	} else {
		r.NextCommand = "resolve required components, then rerun: ciru-strixlink prerequisites"
	}
}

// Check inspects capabilities without changing the host.
func Check(toolVersion string) Report {
	host, _ := os.Hostname()
	osr := osRelease()
	kernel := firstLine(output("uname", "-r"))
	pm, pmDoc, pmAvailable := packageManager(osr["ID"])
	r := Report{
		SchemaVersion: SchemaVersion,
		ToolVersion:   toolVersion,
		GeneratedAt:   time.Now().UTC(),
		System: System{
			Hostname: host, OSID: osr["ID"], OSName: osr["PRETTY_NAME"], OSVersion: osr["VERSION_ID"],
			Kernel: kernel, Architecture: runtime.GOARCH, PackageManager: pm,
		},
	}

	linux := Component{ID: "linux", Label: "Linux operating system", Required: true, HelpURL: DocsBase + "linux.md"}
	if runtime.GOOS == "linux" {
		linux.Status, linux.Summary, linux.Detected = Available, "Linux is supported", valueOr(osr["PRETTY_NAME"], runtime.GOOS)
	} else {
		linux.Status, linux.Summary, linux.Detected = Unsupported, "CiruStrixLink setup and testing require Linux", runtime.GOOS
	}
	r.Components = append(r.Components, linux)

	cpu := cpuModel()
	halo := Component{ID: "strix_halo", Label: "AMD Strix Halo platform", Required: true, HelpURL: DocsBase + "strix-halo.md", Detected: cpu}
	switch {
	case strings.Contains(strings.ToLower(cpu), "ryzen ai max"):
		halo.Status, halo.Summary = Available, "AMD Ryzen AI Max / Strix Halo detected"
	case cpu == "":
		halo.Status, halo.Summary = Unknown, "CPU model could not be read"
	default:
		halo.Status, halo.Summary = Unsupported, "CPU does not identify as AMD Ryzen AI Max"
	}
	r.Components = append(r.Components, halo)

	usb4 := Component{ID: "usb4_controller", Label: "USB4/Thunderbolt controller", Required: true, HelpURL: DocsBase + "usb4.md"}
	if entries, err := os.ReadDir("/sys/bus/thunderbolt/devices"); err == nil && len(entries) > 0 {
		usb4.Status, usb4.Summary, usb4.Detected = Available, "the Linux Thunderbolt/USB4 bus is present", strconv.Itoa(len(entries))+" sysfs device(s)"
	} else if err == nil {
		usb4.Status, usb4.Summary = NotDetected, "USB4 bus exists but no controller or peer is visible"
	} else {
		usb4.Status, usb4.Summary = NotDetected, "Linux did not expose the Thunderbolt/USB4 bus"
	}
	r.Components = append(r.Components, usb4)

	interfaces, _ := link.Discover()
	driverLoaded := len(interfaces) > 0
	if _, err := os.Stat("/sys/module/thunderbolt_net"); err == nil {
		driverLoaded = true
	}
	driverPresent := driverLoaded || moduleAvailable(kernel)
	driver := Component{ID: "thunderbolt_net", Label: "USB4NET kernel driver", Required: true, HelpURL: DocsBase + "thunderbolt-net.md"}
	switch {
	case driverLoaded:
		driver.Status, driver.Summary, driver.Detected = Available, "thunderbolt-net is loaded or built in", kernel
	case driverPresent:
		driver.Status, driver.Summary, driver.Detected = Inactive, "thunderbolt-net is available but not loaded", kernel
		driver.CanAutoFix, driver.SuggestedCommand = true, "sudo modprobe thunderbolt-net"
	default:
		driver.Status, driver.Summary = Missing, "thunderbolt-net was not found for the running kernel"
	}
	r.Components = append(r.Components, driver)

	streamLoaded := false
	if _, err := os.Stat("/sys/module/thunderbolt_stream"); err == nil {
		streamLoaded = true
	}
	streamPresent := streamLoaded || moduleAvailableName(kernel, "thunderbolt_stream")
	stream := Component{ID: "thunderbolt_stream", Label: "Optional USB4STREAM/NHI acceleration", Required: false, HelpURL: DocsBase + "nhi.md", SuggestedCommand: "ciru-strixlink transport status --peer PEER_USB4_ADDRESS"}
	switch {
	case streamLoaded:
		stream.Status, stream.Summary, stream.Detected = Available, "thunderbolt_stream is loaded; transport status will verify lifecycle order and endpoint state", kernel
	case streamPresent:
		stream.Status, stream.Summary, stream.Detected = Inactive, "thunderbolt_stream is available but correctly remains unarmed until the USB4NET gate passes", kernel
	default:
		stream.Status, stream.Summary = Missing, "optional NHI acceleration is unavailable; the portable RCCL/socket baseline remains supported"
	}
	r.Components = append(r.Components, stream)

	modprobe := Component{ID: "modprobe", Label: "Kernel module loader", Required: !driverLoaded, HelpURL: DocsBase + "thunderbolt-net.md"}
	if path, ok := command("modprobe"); ok {
		modprobe.Status, modprobe.Summary, modprobe.Detected = Available, "modprobe is available", path
	} else {
		modprobe.Status, modprobe.Summary = Missing, "modprobe is not installed or not on PATH"
	}
	r.Components = append(r.Components, modprobe)

	netif := Component{ID: "usb4net_interface", Label: "USB4NET network interface", Required: true, HelpURL: DocsBase + "usb4.md"}
	if len(interfaces) > 0 {
		names := make([]string, 0, len(interfaces))
		for _, i := range interfaces {
			names = append(names, i.Name)
		}
		sort.Strings(names)
		netif.Status, netif.Summary, netif.Detected = Available, "USB4NET interface is ready for configuration", strings.Join(names, ",")
	} else {
		netif.Status, netif.Summary = NotDetected, "no USB4NET interface is visible; connect both peers with a USB4 cable"
		netif.SuggestedCommand = "sudo modprobe thunderbolt-net"
	}
	r.Components = append(r.Components, netif)

	addCommandComponent(&r.Components, "iproute2", "iproute2 networking tools", "ip", "-V", "iproute2.md", true)
	addCommandComponent(&r.Components, "ping", "MTU probe utility", "ping", "-V", "iputils.md", true)

	nm := Component{ID: "networkmanager", Label: "NetworkManager persistent backend", Required: false, HelpURL: DocsBase + "networkmanager.md"}
	if path, ok := command("nmcli"); ok {
		nm.Detected = valueOr(firstLine(output("nmcli", "--version")), path)
		if strings.EqualFold(strings.TrimSpace(output("nmcli", "-t", "-f", "RUNNING", "general")), "running") {
			nm.Status, nm.Summary = Available, "NetworkManager is installed and running"
		} else {
			nm.Status, nm.Summary = Inactive, "nmcli is installed but NetworkManager is not running; iproute2 fallback remains available"
		}
	} else {
		nm.Status, nm.Summary = Missing, "NetworkManager is optional; setup can use the temporary iproute2 backend"
	}
	r.Components = append(r.Components, nm)

	addCommandComponent(&r.Components, "ethtool", "Link inventory utility", "ethtool", "--version", "ethtool.md", false)

	priv := Component{ID: "privilege_escalation", Label: "Administrative privileges", Required: true, HelpURL: DocsBase + "permissions.md"}
	if strings.TrimSpace(output("id", "-u")) == "0" {
		priv.Status, priv.Summary, priv.Detected = Available, "running as root", "uid 0"
	} else if path, ok := command("sudo"); ok {
		priv.Status, priv.Summary, priv.Detected = Available, "sudo is available for setup --apply", path
	} else {
		priv.Status, priv.Summary = Missing, "root access or sudo is required to configure the link"
	}
	r.Components = append(r.Components, priv)

	pmc := Component{ID: "package_manager", Label: "Distribution package manager", Required: false, HelpURL: pmDoc, Detected: pm}
	if pmAvailable {
		pmc.Status, pmc.Summary = Available, pm+" detected; CiruStrixLink will provide instructions but will not change kernel packages automatically"
	} else if pm != "" {
		pmc.Status, pmc.Summary = NotDetected, pm+" is expected for this distribution but its command was not found on PATH"
	} else {
		pmc.Status, pmc.Summary = Unknown, "no recognized package manager was detected"
	}
	r.Components = append(r.Components, pmc)

	summarize(&r)
	return r
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
