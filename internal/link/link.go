// Package link discovers and configures Linux USB4NET interfaces.
package link

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

type Interface struct {
	Name      string   `json:"name"`
	Driver    string   `json:"driver"`
	State     string   `json:"state"`
	MTU       int      `json:"mtu"`
	MAC       string   `json:"mac"`
	Addresses []string `json:"addresses"`
	Speed     string   `json:"speed,omitempty"`
}

type ThunderboltPath struct {
	Device  string `json:"device"`
	RXSpeed string `json:"rx_speed,omitempty"`
	TXSpeed string `json:"tx_speed,omitempty"`
	RXLanes string `json:"rx_lanes,omitempty"`
	TXLanes string `json:"tx_lanes,omitempty"`
	PeerID  string `json:"peer_id,omitempty"`
}

type Probe struct {
	Hostname         string            `json:"hostname"`
	Kernel           string            `json:"kernel"`
	CPUModel         string            `json:"cpu_model,omitempty"`
	StrixHaloLikely  bool              `json:"strix_halo_likely"`
	Interfaces       []Interface       `json:"interfaces"`
	ThunderboltPaths []ThunderboltPath `json:"thunderbolt_paths,omitempty"`
	Warnings         []string          `json:"warnings,omitempty"`
}

type Route struct {
	Peer           string `json:"peer"`
	Device         string `json:"device"`
	Source         string `json:"source"`
	Raw            string `json:"raw"`
	InterfaceMatch bool   `json:"interface_match"`
}

type Command struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

func (c Command) String() string {
	parts := append([]string{c.Name}, c.Args...)
	for i, part := range parts {
		if strings.ContainsAny(part, " \t\n\"'") {
			parts[i] = strconv.Quote(part)
		}
	}
	return strings.Join(parts, " ")
}

type SetupOptions struct {
	Interface string
	Role      string
	Subnet    string
	MTU       int
	Profile   string
	Backend   string
	Replace   bool
}

type SetupPlan struct {
	Backend   string    `json:"backend"`
	Interface string    `json:"interface"`
	Address   string    `json:"address"`
	Peer      string    `json:"peer"`
	MTU       int       `json:"mtu"`
	Commands  []Command `json:"commands"`
	Warnings  []string  `json:"warnings,omitempty"`
}

func activeNMProfile(iface string) string {
	out, err := exec.Command("nmcli", "-t", "-f", "NAME,DEVICE", "connection", "show", "--active").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		idx := strings.LastIndex(line, ":")
		if idx > 0 && line[idx+1:] == iface {
			return strings.ReplaceAll(line[:idx], "\\:", ":")
		}
	}
	return ""
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func driverFor(name string) string {
	p, err := filepath.EvalSymlinks(filepath.Join("/sys/class/net", name, "device/driver"))
	if err == nil {
		return filepath.Base(p)
	}
	if out, err := exec.Command("ethtool", "-i", name).Output(); err == nil {
		s := bufio.NewScanner(strings.NewReader(string(out)))
		for s.Scan() {
			if k, v, ok := strings.Cut(s.Text(), ":"); ok && strings.TrimSpace(k) == "driver" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func speedFor(name string) string {
	out, err := exec.Command("ethtool", name).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "Speed" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func Discover() ([]Interface, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("USB4NET discovery is supported on Linux only")
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var found []Interface
	for _, n := range ifs {
		driver := driverFor(n.Name)
		if driver != "thunderbolt-net" && !strings.HasPrefix(n.Name, "thunderbolt") {
			continue
		}
		item := Interface{Name: n.Name, Driver: driver, State: readTrim(filepath.Join("/sys/class/net", n.Name, "operstate")), MTU: n.MTU, MAC: n.HardwareAddr.String(), Speed: speedFor(n.Name)}
		addrs, _ := n.Addrs()
		for _, a := range addrs {
			item.Addresses = append(item.Addresses, a.String())
		}
		sort.Strings(item.Addresses)
		found = append(found, item)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, nil
}

func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if k, v, ok := strings.Cut(s.Text(), ":"); ok && strings.TrimSpace(k) == "model name" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func tbPaths() []ThunderboltPath {
	entries, _ := filepath.Glob("/sys/bus/thunderbolt/devices/*")
	var paths []ThunderboltPath
	for _, p := range entries {
		rx := readTrim(filepath.Join(p, "rx_speed"))
		tx := readTrim(filepath.Join(p, "tx_speed"))
		peer := readTrim(filepath.Join(p, "unique_id"))
		if rx == "" && tx == "" && peer == "" {
			continue
		}
		paths = append(paths, ThunderboltPath{Device: filepath.Base(p), RXSpeed: rx, TXSpeed: tx, RXLanes: readTrim(filepath.Join(p, "rx_lanes")), TXLanes: readTrim(filepath.Join(p, "tx_lanes")), PeerID: peer})
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Device < paths[j].Device })
	return paths
}

func ProbeLocal() (Probe, error) {
	host, _ := os.Hostname()
	kernel := ""
	if out, err := exec.Command("uname", "-sr").Output(); err == nil {
		kernel = strings.TrimSpace(string(out))
	}
	ifs, err := Discover()
	if err != nil {
		return Probe{}, err
	}
	cpu := cpuModel()
	p := Probe{Hostname: host, Kernel: kernel, CPUModel: cpu, StrixHaloLikely: strings.Contains(strings.ToLower(cpu), "ryzen ai max"), Interfaces: ifs, ThunderboltPaths: tbPaths()}
	if len(ifs) == 0 {
		p.Warnings = append(p.Warnings, "no thunderbolt-net interface found; connect the cable and load the thunderbolt-net module")
	}
	if cpu != "" && !p.StrixHaloLikely {
		p.Warnings = append(p.Warnings, "CPU model does not look like AMD Ryzen AI Max; USB4NET may still work")
	}
	return p, nil
}

func SelectInterface(requested string) (Interface, error) {
	ifs, err := Discover()
	if err != nil {
		return Interface{}, err
	}
	if requested != "" && requested != "auto" {
		for _, i := range ifs {
			if i.Name == requested {
				return i, nil
			}
		}
		return Interface{}, fmt.Errorf("%s is not a discovered thunderbolt-net interface", requested)
	}
	if len(ifs) == 0 {
		return Interface{}, errors.New("no thunderbolt-net interface found")
	}
	if len(ifs) > 1 {
		return Interface{}, errors.New("multiple thunderbolt-net interfaces found; select one with --interface")
	}
	return ifs[0], nil
}

func RouteTo(peer, wantInterface string) (Route, error) {
	out, err := exec.Command("ip", "-j", "route", "get", peer).Output()
	if err != nil {
		return Route{}, fmt.Errorf("ip route get %s: %w", peer, err)
	}
	var rows []struct {
		Dev     string `json:"dev"`
		Prefsrc string `json:"prefsrc"`
		Src     string `json:"src"`
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) == 0 {
		return Route{}, fmt.Errorf("cannot parse route to %s", peer)
	}
	src := rows[0].Prefsrc
	if src == "" {
		src = rows[0].Src
	}
	return Route{Peer: peer, Device: rows[0].Dev, Source: src, Raw: strings.TrimSpace(string(out)), InterfaceMatch: rows[0].Dev == wantInterface}, nil
}

func RoleAddresses(subnet, role string) (address, peer string, err error) {
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil || !prefix.Addr().Is4() {
		return "", "", fmt.Errorf("--subnet must be an IPv4 prefix: %q", subnet)
	}
	prefix = prefix.Masked()
	if prefix.Bits() != 30 {
		return "", "", errors.New("--subnet must be /30 so the two host addresses are unambiguous")
	}
	base := prefix.Addr().As4()
	a := netip.AddrFrom4([4]byte{base[0], base[1], base[2], base[3] + 1})
	b := netip.AddrFrom4([4]byte{base[0], base[1], base[2], base[3] + 2})
	switch strings.ToLower(role) {
	case "a", "1", "stage1":
		return a.String() + "/30", b.String(), nil
	case "b", "2", "stage0":
		return b.String() + "/30", a.String(), nil
	default:
		return "", "", errors.New("--role must be a or b (stage1 and stage0 are accepted aliases)")
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// LoadDriver is the only setup mutation allowed before interface discovery.
// It is called only when the user explicitly passed setup --apply.
func LoadDriver() error {
	if runtime.GOOS != "linux" {
		return errors.New("driver loading is supported on Linux only")
	}
	if !isRoot() {
		return errors.New("loading thunderbolt-net requires root; rerun with sudo")
	}
	if !commandExists("modprobe") {
		return errors.New("modprobe is unavailable; load thunderbolt-net through your distribution and retry")
	}
	cmd := exec.Command("modprobe", "thunderbolt-net")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("modprobe thunderbolt-net: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func BuildSetupPlan(o SetupOptions) (SetupPlan, error) {
	if o.MTU != 1500 && o.MTU != 9000 {
		return SetupPlan{}, errors.New("--mtu must be 1500 or 9000")
	}
	if o.Profile == "" {
		o.Profile = "ciru-strixlink-usb4"
	}
	i, err := SelectInterface(o.Interface)
	if err != nil {
		return SetupPlan{}, err
	}
	address, peer, err := RoleAddresses(o.Subnet, o.Role)
	if err != nil {
		return SetupPlan{}, err
	}
	backend := strings.ToLower(o.Backend)
	if backend == "" || backend == "auto" {
		if commandExists("nmcli") {
			backend = "networkmanager"
		} else {
			backend = "iproute2"
		}
	}
	p := SetupPlan{Backend: backend, Interface: i.Name, Address: address, Peer: peer, MTU: o.MTU}
	if commandExists("modprobe") {
		p.Commands = append(p.Commands, Command{Name: "modprobe", Args: []string{"thunderbolt-net"}})
	} else {
		p.Warnings = append(p.Warnings, "modprobe is unavailable; continuing because the USB4NET interface already exists")
	}
	switch backend {
	case "networkmanager", "nm":
		if !commandExists("nmcli") {
			return SetupPlan{}, errors.New("NetworkManager backend selected but nmcli is not installed")
		}
		backend = "networkmanager"
		p.Backend = backend
		if active := activeNMProfile(i.Name); active != "" && active != o.Profile {
			if !o.Replace {
				return SetupPlan{}, fmt.Errorf("%s is managed by active NetworkManager profile %q; use --take-over to switch profiles without deleting it", i.Name, active)
			}
			p.Warnings = append(p.Warnings, fmt.Sprintf("activating %q will replace active profile %q on %s; the old profile will not be deleted", o.Profile, active, i.Name))
		}
		exists := exec.Command("nmcli", "-g", "connection.id", "connection", "show", o.Profile).Run() == nil
		settings := []string{"connection.interface-name", i.Name, "connection.autoconnect", "yes", "802-3-ethernet.mtu", strconv.Itoa(o.MTU), "ipv4.method", "manual", "ipv4.addresses", address, "ipv4.gateway", "", "ipv4.never-default", "yes", "ipv4.route-metric", "50", "ipv6.method", "link-local", "ipv6.never-default", "yes", "ipv6.route-metric", "50"}
		if exists {
			args := append([]string{"connection", "modify", o.Profile}, settings...)
			p.Commands = append(p.Commands, Command{Name: "nmcli", Args: args})
		} else {
			args := []string{"connection", "add", "type", "ethernet", "con-name", o.Profile, "ifname", i.Name}
			args = append(args, settings...)
			p.Commands = append(p.Commands, Command{Name: "nmcli", Args: args})
		}
		p.Commands = append(p.Commands, Command{Name: "nmcli", Args: []string{"connection", "up", o.Profile}})
	case "iproute2", "ip":
		if !commandExists("ip") {
			return SetupPlan{}, errors.New("iproute2 backend selected but ip is not installed")
		}
		p.Backend = "iproute2"
		p.Warnings = append(p.Warnings, "iproute2 setup is temporary and will not survive reboot")
		if commandExists("nmcli") {
			if active := activeNMProfile(i.Name); active != "" {
				p.Warnings = append(p.Warnings, fmt.Sprintf("NetworkManager profile %q is active on %s and may revert raw iproute2 changes", active, i.Name))
			}
		}
		p.Commands = append(p.Commands,
			Command{Name: "ip", Args: []string{"link", "set", "dev", i.Name, "mtu", strconv.Itoa(o.MTU), "up"}},
			Command{Name: "ip", Args: []string{"address", "replace", address, "dev", i.Name}},
		)
	default:
		return SetupPlan{}, fmt.Errorf("unknown backend %q", o.Backend)
	}
	return p, nil
}

// BuildRollbackPlan removes only the named NetworkManager profile. An older
// profile can be reactivated explicitly; setup never deletes it during takeover.
func BuildRollbackPlan(profile, requestedInterface, restore string) (SetupPlan, error) {
	if !commandExists("nmcli") {
		return SetupPlan{}, errors.New("rollback requires nmcli")
	}
	if profile == "" {
		profile = "ciru-strixlink-usb4"
	}
	out, err := exec.Command("nmcli", "-g", "connection.interface-name", "connection", "show", profile).Output()
	if err != nil {
		return SetupPlan{}, fmt.Errorf("NetworkManager profile %q does not exist", profile)
	}
	iface := strings.TrimSpace(string(out))
	if iface == "" {
		return SetupPlan{}, fmt.Errorf("NetworkManager profile %q has no fixed interface", profile)
	}
	if requestedInterface != "" && requestedInterface != "auto" && requestedInterface != iface {
		return SetupPlan{}, fmt.Errorf("profile %q belongs to %s, not requested interface %s", profile, iface, requestedInterface)
	}
	p := SetupPlan{Backend: "networkmanager", Interface: iface, Commands: []Command{{Name: "nmcli", Args: []string{"connection", "delete", profile}}}}
	p.Warnings = append(p.Warnings, fmt.Sprintf("applying rollback deletes NetworkManager profile %q and may interrupt the USB4 link", profile))
	if restore != "" {
		if exec.Command("nmcli", "-g", "connection.id", "connection", "show", restore).Run() != nil {
			return SetupPlan{}, fmt.Errorf("restore profile %q does not exist", restore)
		}
		p.Commands = append(p.Commands, Command{Name: "nmcli", Args: []string{"connection", "up", restore}})
	}
	return p, nil
}

func Apply(plan SetupPlan) error {
	if !isRoot() {
		return errors.New("setup changes require root; rerun with sudo")
	}
	for _, c := range plan.Commands {
		cmd := exec.Command(c.Name, c.Args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", c.String(), err)
		}
	}
	return nil
}

func VerifyPathMTU(peer, iface string, mtu int) (bool, string) {
	if mtu < 1280 {
		return false, "interface MTU is below 1280"
	}
	payload := mtu - 28
	cmd := exec.Command("ping", "-I", iface, "-M", "do", "-c", "2", "-W", "1", "-s", strconv.Itoa(payload), peer)
	out, err := cmd.CombinedOutput()
	if errors.Is(err, exec.ErrNotFound) {
		return false, "ping is not installed"
	}
	if err != nil {
		return false, strings.TrimSpace(string(out))
	}
	return true, fmt.Sprintf("%d-byte path MTU passed", mtu)
}
