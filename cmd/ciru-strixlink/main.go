package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/bench"
	installpkg "github.com/ciru-ai/CiruStrixLink/internal/install"
	"github.com/ciru-ai/CiruStrixLink/internal/link"
	"github.com/ciru-ai/CiruStrixLink/internal/prereq"
	transportstate "github.com/ciru-ai/CiruStrixLink/internal/transport"
)

var version = "0.1.0"

type testReport struct {
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

func usage() {
	fmt.Fprintf(os.Stderr, `CiruStrixLink %s - configure and qualify Linux USB4NET links

Usage:
  ciru-strixlink prerequisites [--json]
  ciru-strixlink install [--component ID] [--include-optional] [--self] [--apply]
  ciru-strixlink transport status [--peer ADDRESS] [--json]
  ciru-strixlink transport reconcile --a REPORT.json --b REPORT.json [--json]
  ciru-strixlink transport endpoint prepare|cleanup [--peer ADDRESS] [--apply]
  ciru-strixlink transport env --peer ADDRESS [--mode auto|portable|nhi] [--runtime generic|vllm|pytorch]
  ciru-strixlink probe [--json]
  ciru-strixlink setup --role a|b [--subnet 10.77.77.0/30] [--mtu 1500|9000] [--apply]
  ciru-strixlink rollback [--profile ciru-strixlink-usb4] [--restore OLD_PROFILE] [--apply]
  ciru-strixlink doctor --peer ADDRESS
  ciru-strixlink serve [--interface thunderbolt0] [--port 55321]
  ciru-strixlink test --peer ADDRESS [--duration 5s] [--streams 4] [--json]
  ciru-strixlink version

Setup is a dry run unless --apply is provided. Run "serve" on one peer, then
"test" from the other. Both bind to the dedicated USB4 address.
`, version)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ciru-strixlink:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "prerequisites", "requirements":
		runPrerequisites(os.Args[2:])
	case "install":
		runInstall(os.Args[2:])
	case "transport", "transports":
		runTransport(os.Args[2:])
	case "probe":
		runProbe(os.Args[2:])
	case "setup":
		runSetup(os.Args[2:])
	case "rollback":
		runRollback(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "test":
		runTest(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

type repeatedString []string

func (r *repeatedString) String() string { return strings.Join(*r, ",") }
func (r *repeatedString) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("component ID cannot be empty")
	}
	*r = append(*r, strings.TrimSpace(value))
	return nil
}

func runInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	var components repeatedString
	fs.Var(&components, "component", "component ID to install; may be repeated")
	includeOptional := fs.Bool("include-optional", false, "include missing optional user-space tools")
	self := fs.Bool("self", false, "install this CiruStrixLink binary")
	prefix := fs.String("prefix", "/usr/local", "absolute installation prefix for --self")
	apply := fs.Bool("apply", false, "execute allowlisted actions after printing the plan")
	force := fs.Bool("force", false, "allow --self to replace an existing binary")
	asJSON := fs.Bool("json", false, "emit the installation plan as JSON")
	_ = fs.Parse(args)
	p, err := installpkg.Build(installpkg.Options{ToolVersion: version, Components: components, IncludeOptional: *includeOptional, InstallSelf: *self, Prefix: *prefix, Apply: *apply, Force: *force})
	if err != nil {
		fail(err)
	}
	if *asJSON {
		printJSON(p)
	} else {
		fmt.Printf("CiruStrixLink installation plan (package manager: %s)\n", valueOr(p.PackageManager, "unrecognized"))
		for _, id := range p.AlreadyReady {
			fmt.Printf("[AVAILABLE] %s\n", id)
		}
		for _, a := range p.Actions {
			fmt.Printf("[%s] %s: %s\n", strings.ToUpper(a.Type), strings.Join(a.Components, ","), a.Summary)
			if a.Command != "" {
				fmt.Printf("  Command: %s %s\n", a.Command, strings.Join(a.Args, " "))
			}
			if a.Target != "" {
				fmt.Printf("  Copy: %s -> %s\n", a.Source, a.Target)
			}
			if a.HelpURL != "" {
				fmt.Printf("  Help: %s\n", a.HelpURL)
			}
		}
		for _, warning := range p.Warnings {
			fmt.Println("Warning:", warning)
		}
		if !*apply {
			fmt.Println("Dry run only. Rerun the reviewed command with sudo and --apply to execute allowlisted actions.")
		}
	}
	if *apply {
		if !p.CanApply {
			fail(errors.New("the selected actions require the linked manual instructions; there is nothing safe to apply automatically"))
		}
		if err := installpkg.Apply(p, *force); err != nil {
			fail(err)
		}
		fmt.Println("Installation actions completed. Rerun: ciru-strixlink prerequisites")
	}
}

func runTransport(args []string) {
	if len(args) == 0 {
		fail(errors.New("transport requires status or reconcile"))
	}
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("transport status", flag.ExitOnError)
		iface := fs.String("interface", "auto", "USB4NET interface or auto")
		peer := fs.String("peer", "", "peer USB4 address; enables the network reachability gate")
		asJSON := fs.Bool("json", false, "emit the versioned transport schema as JSON")
		output := fs.String("output", "", "also write the JSON report to this path")
		_ = fs.Parse(args[1:])
		r := transportstate.Inspect(version, *iface, *peer)
		if *output != "" {
			if err := writeJSON(*output, r); err != nil {
				fail(err)
			}
		}
		if *asJSON {
			printJSON(r)
		} else {
			printTransportReport(r)
		}
		if !r.Portable.Ready {
			os.Exit(2)
		}
	case "reconcile":
		fs := flag.NewFlagSet("transport reconcile", flag.ExitOnError)
		aPath := fs.String("a", "", "endpoint A transport report")
		bPath := fs.String("b", "", "endpoint B transport report")
		asJSON := fs.Bool("json", false, "emit JSON")
		output := fs.String("output", "", "also write the pair JSON report to this path")
		_ = fs.Parse(args[1:])
		if *aPath == "" || *bPath == "" {
			fail(errors.New("transport reconcile requires --a and --b"))
		}
		a, err := transportstate.LoadReport(*aPath)
		if err != nil {
			fail(fmt.Errorf("load endpoint A report: %w", err))
		}
		b, err := transportstate.LoadReport(*bPath)
		if err != nil {
			fail(fmt.Errorf("load endpoint B report: %w", err))
		}
		p := transportstate.Reconcile(a, b)
		if *output != "" {
			if err := writeJSON(*output, p); err != nil {
				fail(err)
			}
		}
		if *asJSON {
			printJSON(p)
		} else {
			fmt.Printf("Transport pair: %s + %s\n", p.HostA, p.HostB)
			fmt.Printf("Pair identity: reciprocal=%t\n", p.PairIdentityValid)
			fmt.Printf("Portable baseline: ready=%t\n", p.PortableReady)
			fmt.Printf("NHI acceleration: %s (qualified=%t, in_use=%t, lease_available=%t, arm_allowed=%t)\n", p.NHIStatus, p.NHIReady, p.NHIInUse, p.LeaseAvailable, p.ArmAllowed)
			fmt.Printf("Decision: %s\n", p.Summary)
			fmt.Printf("Fallback: %s available=%t required=%t cleanup_required=%t\n", p.Fallback.Mode, p.Fallback.Available, p.Fallback.Required, p.Fallback.CleanupRequired)
		}
		if !p.PortableReady {
			os.Exit(2)
		}
	case "endpoint":
		runEndpoint(args[1:])
	case "env":
		runTransportEnv(args[1:])
	default:
		fail(fmt.Errorf("unknown transport action %q", args[0]))
	}
}

func runTransportEnv(args []string) {
	fs := flag.NewFlagSet("transport env", flag.ExitOnError)
	iface := fs.String("interface", "auto", "USB4NET interface or auto")
	peer := fs.String("peer", "", "peer USB4 address")
	mode := fs.String("mode", "auto", "auto, portable, or nhi")
	runtimeName := fs.String("runtime", "generic", "generic, vllm, or pytorch")
	pairPath := fs.String("pair-report", "", "reconciled pair report; required for NHI")
	output := fs.String("output", "", "write dotenv output to this path")
	asJSON := fs.Bool("json", false, "emit JSON instead of dotenv")
	_ = fs.Parse(args)
	if *peer == "" {
		fail(errors.New("transport env requires --peer"))
	}
	local := transportstate.Inspect(version, *iface, *peer)
	var pair *transportstate.PairReport
	if *pairPath != "" {
		loaded, err := transportstate.LoadPairReport(*pairPath)
		if err != nil {
			fail(err)
		}
		pair = &loaded
	}
	env, err := transportstate.GenerateEnvironment(local, pair, *mode, *runtimeName)
	if err != nil {
		fail(err)
	}
	if *output != "" {
		dir := filepath.Dir(*output)
		if dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fail(err)
			}
		}
		if err := os.WriteFile(*output, []byte(env.DotEnv()), 0o644); err != nil {
			fail(err)
		}
	}
	if *asJSON {
		printJSON(env)
	} else if *output == "" {
		fmt.Print(env.DotEnv())
	} else {
		fmt.Printf("Wrote %s transport environment to %s\n", env.Mode, *output)
	}
}

func runEndpoint(args []string) {
	if len(args) == 0 || (args[0] != "prepare" && args[0] != "cleanup") {
		fail(errors.New("transport endpoint requires prepare or cleanup"))
	}
	action := args[0]
	fs := flag.NewFlagSet("transport endpoint "+action, flag.ExitOnError)
	iface := fs.String("interface", "auto", "USB4NET interface or auto")
	peer := fs.String("peer", "", "peer USB4 address; required for prepare")
	name := fs.String("name", "ciru-nhi", "configfs endpoint name")
	ring := fs.Int("ring", transportstate.ExpectedRing, "NHI ring size")
	throttle := fs.Int("throttling", transportstate.ExpectedThrottle, "interrupt throttling in ns")
	stateDir := fs.String("state-dir", "/run/ciru-strixlink-nhi", "exact runtime state directory")
	group := fs.String("group", "render", "device group")
	timeout := fs.Duration("timeout", 60*time.Second, "endpoint negotiation timeout")
	adopt := fs.Bool("adopt", false, "explicitly adopt or clean a reviewed legacy endpoint")
	apply := fs.Bool("apply", false, "execute the reviewed local endpoint transaction")
	asJSON := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args[1:])
	p, err := transportstate.BuildEndpointPlan(transportstate.EndpointOptions{ToolVersion: version, Action: action, Peer: *peer, Interface: *iface, Name: *name, Ring: *ring, Throttling: *throttle, StateDir: *stateDir, DeviceGroup: *group, Timeout: *timeout, Adopt: *adopt, Apply: *apply})
	if err != nil {
		fail(err)
	}
	if !*apply {
		if *asJSON {
			printJSON(p)
		} else {
			printEndpointPlan(p)
		}
		if !p.CanApply {
			os.Exit(2)
		}
		return
	}
	if !*asJSON {
		printEndpointPlan(p)
	}
	if err := transportstate.ApplyEndpoint(p); err != nil {
		fail(err)
	}
	post := transportstate.Inspect(version, *iface, *peer)
	if *asJSON {
		printJSON(post)
	} else {
		fmt.Printf("Endpoint %s completed. Run transport status on both peers and reconcile their reports before selecting NHI.\n", action)
	}
}

func printEndpointPlan(p transportstate.EndpointPlan) {
	fmt.Printf("CiruStrixLink NHI endpoint %s plan: can_apply=%t\n", p.Action, p.CanApply)
	fmt.Println(p.Summary)
	for i, step := range p.Steps {
		fmt.Printf("  %d. %s\n", i+1, step)
	}
	for _, blocker := range p.Blockers {
		fmt.Println("  BLOCKER:", blocker)
	}
	if !p.ApplyRequested {
		if p.Action == "prepare" {
			fmt.Println("Dry run only. Endpoint prepare must be launched concurrently on both peers; use sudo and --apply only after both plans pass.")
		} else {
			fmt.Println("Dry run only. Coordinate cleanup on both peers; use sudo and --apply only after holders and exact ownership pass.")
		}
	}
}

func printTransportReport(r transportstate.Report) {
	fmt.Printf("CiruStrixLink transport status on %s (schema %d)\n", r.Hostname, r.SchemaVersion)
	fmt.Printf("Kernel: %s; interface: %s; local: %s; peer: %s\n", valueOr(r.Kernel, "unknown"), valueOr(r.Interface, "not detected"), valueOr(r.LocalAddress, "not detected"), valueOr(r.Peer, "not checked"))
	for _, mode := range []transportstate.Mode{r.Portable, r.NHI} {
		fmt.Printf("\n%s: %s (ready=%t)\n", mode.Label, strings.ToUpper(mode.Status), mode.Ready)
		fmt.Printf("  %s\n", mode.Summary)
		for _, check := range mode.Checks {
			fmt.Printf("  [%-14s] %-24s %s\n", strings.ToUpper(check.Status), check.ID, check.Summary)
			if check.Detected != "" {
				fmt.Printf("                   Detected: %s\n", check.Detected)
			}
			if check.Status == "failed" || check.Status == "not_armed" || check.Status == "unknown" {
				fmt.Printf("                   Help: %s\n", check.HelpURL)
			}
		}
	}
	for _, e := range r.Endpoints {
		fmt.Printf("\nEndpoint %s: ring=%d throttle=%d hop=%d/%d device=%s exact=%t holder_scan_complete=%t\n", e.ConfigPath, e.RingSize, e.ThrottlingNS, e.InHopID, e.OutHopID, e.Device, e.ProductionFit, e.HolderScanComplete)
		for _, h := range e.Holders {
			fmt.Printf("  holder pid=%d command=%s cap_sys_rawio=%t fd=%s\n", h.PID, h.Command, h.HasRawIOCap, h.FD)
		}
	}
	fmt.Println("\nLifecycle:")
	for _, step := range r.Lifecycle {
		fmt.Printf("  %d. [%-12s] %s\n", step.Order, strings.ToUpper(step.Status), step.Summary)
	}
	fmt.Printf("\nLocal NHI arm readiness: %t (pair reconciliation is always required)\n", r.LocalArmReady)
	fmt.Printf("Fallback: %s available=%t required=%t cleanup_required=%t\n", r.Fallback.Mode, r.Fallback.Available, r.Fallback.Required, r.Fallback.CleanupRequired)
	if r.Cleanup.Required {
		for _, step := range r.Cleanup.Steps {
			fmt.Printf("  cleanup: %s\n", step)
		}
	}
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func runPrerequisites(args []string) {
	fs := flag.NewFlagSet("prerequisites", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit the versioned UI schema as JSON")
	_ = fs.Parse(args)
	r := prereq.Check(version)
	if *asJSON {
		printJSON(r)
	} else {
		fmt.Printf("CiruStrixLink prerequisites (schema %d)\n", r.SchemaVersion)
		fmt.Printf("System: %s; kernel %s; %s\n", valueOr(r.System.OSName, r.System.OSID), valueOr(r.System.Kernel, "unknown"), r.System.Architecture)
		fmt.Printf("Overall: %s\n\n", strings.ToUpper(r.OverallStatus))
		for _, c := range r.Components {
			requirement := "optional"
			if c.Required {
				requirement = "required"
			}
			fmt.Printf("[%-12s] %-38s (%s)\n", strings.ToUpper(string(c.Status)), c.Label, requirement)
			fmt.Printf("               %s\n", c.Summary)
			if c.Detected != "" {
				fmt.Printf("               Detected: %s\n", c.Detected)
			}
			if c.SuggestedCommand != "" {
				fmt.Printf("               Suggested: %s\n", c.SuggestedCommand)
			}
			if c.Status != prereq.Available {
				fmt.Printf("               Help: %s\n", c.HelpURL)
			}
		}
		fmt.Printf("\nNext: %s\n", r.NextCommand)
	}
	switch r.OverallStatus {
	case "unsupported":
		os.Exit(3)
	case "needs_action":
		os.Exit(2)
	}
}

func runRollback(args []string) {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	iface := fs.String("interface", "auto", "expected USB4NET interface or auto")
	profile := fs.String("profile", "ciru-strixlink-usb4", "exact CiruStrixLink NetworkManager profile to delete")
	restore := fs.String("restore", "", "optional preserved NetworkManager profile to reactivate")
	apply := fs.Bool("apply", false, "apply the displayed rollback")
	asJSON := fs.Bool("json", false, "emit plan as JSON")
	_ = fs.Parse(args)
	p, err := link.BuildRollbackPlan(*profile, *iface, *restore)
	if err != nil {
		fail(err)
	}
	if *asJSON {
		printJSON(p)
	} else {
		fmt.Printf("Rollback profile: %s on %s\n", *profile, p.Interface)
		for _, c := range p.Commands {
			fmt.Println("  " + c.String())
		}
		for _, w := range p.Warnings {
			fmt.Println("Warning:", w)
		}
		if !*apply {
			fmt.Println("Dry run only. Rerun with --apply (and sudo) to execute this rollback.")
		}
	}
	if *apply {
		if err := link.Apply(p); err != nil {
			fail(err)
		}
		fmt.Println("Rollback complete.")
	}
}

func runProbe(args []string) {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)
	p, err := link.ProbeLocal()
	if err != nil {
		fail(err)
	}
	if *asJSON {
		printJSON(p)
		return
	}
	fmt.Printf("Host: %s\nKernel: %s\nCPU: %s\n", p.Hostname, p.Kernel, valueOr(p.CPUModel, "unknown"))
	if p.StrixHaloLikely {
		fmt.Println("Platform: AMD Strix Halo detected")
	} else {
		fmt.Println("Platform: Strix Halo not confirmed")
	}
	if len(p.Interfaces) == 0 {
		fmt.Println("USB4NET interfaces: none")
	}
	for _, i := range p.Interfaces {
		fmt.Printf("USB4NET: %s driver=%s state=%s mtu=%d speed=%s addresses=%s\n", i.Name, valueOr(i.Driver, "unknown"), i.State, i.MTU, valueOr(i.Speed, "unknown"), strings.Join(i.Addresses, ","))
	}
	for _, w := range p.Warnings {
		fmt.Println("Warning:", w)
	}
}

func runSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	iface := fs.String("interface", "auto", "USB4NET interface or auto")
	role := fs.String("role", "", "endpoint a (.1) or b (.2)")
	subnet := fs.String("subnet", "10.77.77.0/30", "private point-to-point /30")
	mtu := fs.Int("mtu", 1500, "MTU: 1500 safe default, or 9000 after both peers agree")
	profile := fs.String("profile", "ciru-strixlink-usb4", "NetworkManager profile name")
	backend := fs.String("backend", "auto", "auto, networkmanager, or iproute2")
	takeOver := fs.Bool("take-over", false, "switch from an existing active NetworkManager profile without deleting it")
	apply := fs.Bool("apply", false, "apply the displayed plan")
	asJSON := fs.Bool("json", false, "emit plan as JSON")
	_ = fs.Parse(args)
	if *role == "" {
		fail(errors.New("--role is required"))
	}
	if *apply {
		if err := link.LoadDriver(); err != nil {
			fail(err)
		}
	}
	p, err := link.BuildSetupPlan(link.SetupOptions{Interface: *iface, Role: *role, Subnet: *subnet, MTU: *mtu, Profile: *profile, Backend: *backend, Replace: *takeOver})
	if err != nil {
		fail(err)
	}
	if *asJSON {
		printJSON(p)
	} else {
		fmt.Printf("Backend: %s\nInterface: %s\nAddress: %s\nPeer: %s\nMTU: %d\n", p.Backend, p.Interface, p.Address, p.Peer, p.MTU)
		for _, c := range p.Commands {
			fmt.Println("  " + c.String())
		}
		for _, w := range p.Warnings {
			fmt.Println("Warning:", w)
		}
		if !*apply {
			fmt.Println("Dry run only. Rerun with --apply (and sudo) to make these changes.")
		}
	}
	if *apply {
		if err := link.Apply(p); err != nil {
			fail(err)
		}
		fmt.Printf("Configured %s. Configure peer %s, then run doctor and the built-in link test.\n", p.Interface, p.Peer)
	}
}

func selectIP(requested string) (link.Interface, string, error) {
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

func runDoctor(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	ifaceName := fs.String("interface", "auto", "USB4NET interface or auto")
	peer := fs.String("peer", "", "peer USB4 address")
	asJSON := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)
	if *peer == "" {
		fail(errors.New("--peer is required"))
	}
	i, localIP, err := selectIP(*ifaceName)
	if err != nil {
		fail(err)
	}
	r, err := link.RouteTo(*peer, i.Name)
	if err != nil {
		fail(err)
	}
	mtuOK, detail := link.VerifyPathMTU(*peer, i.Name, i.MTU)
	out := struct {
		Interface     string     `json:"interface"`
		LocalIP       string     `json:"local_ip"`
		Route         link.Route `json:"route"`
		PathMTUPassed bool       `json:"path_mtu_passed"`
		PathMTUDetail string     `json:"path_mtu_detail"`
	}{i.Name, localIP, r, mtuOK, detail}
	if *asJSON {
		printJSON(out)
	} else {
		fmt.Printf("Interface: %s local=%s peer=%s mtu=%d\nRoute: dev=%s src=%s match=%t\nPath MTU: %t (%s)\n", i.Name, localIP, *peer, i.MTU, r.Device, r.Source, r.InterfaceMatch, mtuOK, detail)
	}
	if !r.InterfaceMatch || !mtuOK {
		os.Exit(2)
	}
}

func tokenFrom(path string) (string, error) {
	if path == "" {
		return os.Getenv("CIRU_STRIXLINK_TOKEN"), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", errors.New("token file is empty")
	}
	return t, nil
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	ifaceName := fs.String("interface", "auto", "USB4NET interface or auto")
	port := fs.Int("port", bench.DefaultPort, "test agent TCP port")
	tokenFile := fs.String("token-file", "", "optional shared token file (or CIRU_STRIXLINK_TOKEN)")
	_ = fs.Parse(args)
	if runtime.GOOS != "linux" {
		fail(errors.New("serve is intended for Linux USB4NET peers"))
	}
	i, localIP, err := selectIP(*ifaceName)
	if err != nil {
		fail(err)
	}
	token, err := tokenFrom(*tokenFile)
	if err != nil {
		fail(err)
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "Warning: test agent has no token; it remains bound only to the dedicated USB4 address")
	}
	l, err := net.Listen("tcp4", net.JoinHostPort(localIP, fmt.Sprint(*port)))
	if err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("CiruStrixLink test agent listening on %s:%d via %s; press Ctrl-C to stop\n", localIP, *port, i.Name)
	if err := bench.Serve(ctx, l, token); err != nil {
		fail(err)
	}
}

func runTest(args []string) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	ifaceName := fs.String("interface", "auto", "USB4NET interface or auto")
	peer := fs.String("peer", "", "peer USB4 address")
	port := fs.Int("port", bench.DefaultPort, "peer test agent TCP port")
	duration := fs.Duration("duration", 5*time.Second, "duration per throughput direction")
	streams := fs.Int("streams", 4, "parallel TCP streams")
	samples := fs.Int("rtt-samples", 100, "application RTT samples")
	tokenFile := fs.String("token-file", "", "optional shared token file (or CIRU_STRIXLINK_TOKEN)")
	asJSON := fs.Bool("json", false, "emit JSON")
	output := fs.String("output", "", "also write the JSON report to this path")
	envFile := fs.String("env-file", "", "write conservative runtime environment to this path")
	_ = fs.Parse(args)
	if *peer == "" {
		fail(errors.New("--peer is required"))
	}
	i, localIP, err := selectIP(*ifaceName)
	if err != nil {
		fail(err)
	}
	route, err := link.RouteTo(*peer, i.Name)
	if err != nil {
		fail(err)
	}
	if !route.InterfaceMatch {
		fail(fmt.Errorf("route to %s uses %s, not %s; refusing to benchmark the wrong network", *peer, route.Device, i.Name))
	}
	if route.Source != "" && route.Source != localIP {
		fail(fmt.Errorf("route source %s does not match USB4 address %s", route.Source, localIP))
	}
	token, err := tokenFrom(*tokenFile)
	if err != nil {
		fail(err)
	}
	mtuOK, mtuDetail := link.VerifyPathMTU(*peer, i.Name, i.MTU)
	host, _ := os.Hostname()
	result, err := bench.Run(bench.Config{LocalIP: localIP, PeerIP: *peer, Port: *port, Streams: *streams, Duration: *duration, RTTSamples: *samples, Token: token})
	if err != nil {
		fail(err)
	}
	report := testReport{Version: version, TimestampUTC: time.Now().UTC(), Hostname: host, Interface: i.Name, LocalIP: localIP, PeerIP: *peer, Route: route, PathMTUPassed: mtuOK, PathMTUDetail: mtuDetail, Benchmark: result}
	b, _ := json.MarshalIndent(report, "", "  ")
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil && filepath.Dir(*output) != "." {
			fail(err)
		}
		if err := os.WriteFile(*output, append(b, '\n'), 0o644); err != nil {
			fail(err)
		}
	}
	if *envFile != "" {
		if err := writeEnv(*envFile, report); err != nil {
			fail(err)
		}
	}
	if *asJSON {
		fmt.Println(string(b))
	} else {
		printHumanReport(report)
	}
	if !result.Policy.Ready || !mtuOK {
		os.Exit(2)
	}
}

func writeEnv(path string, r testReport) error {
	stage0 := r.PeerIP
	if r.Benchmark.FasterSender == "local" {
		stage0 = r.LocalIP
	}
	p := r.Benchmark.Policy
	transportReport := transportstate.Inspect(version, r.Interface, r.PeerIP)
	env, err := transportstate.GenerateEnvironment(transportReport, nil, "portable", "generic")
	if err != nil {
		return err
	}
	env.Variables["CIRU_STRIXLINK_RECOMMENDED_STAGE0"] = stage0
	env.Variables["CIRU_STRIXLINK_QUALITY"] = p.Class
	env.Variables["CIRU_STRIXLINK_HEARTBEAT_MS"] = fmt.Sprint(p.HeartbeatIntervalMs)
	env.Variables["CIRU_STRIXLINK_PEER_TIMEOUT_MS"] = fmt.Sprint(p.PeerTimeoutMs)
	env.Variables["CIRU_STRIXLINK_RECONNECT_ATTEMPTS"] = fmt.Sprint(p.ReconnectAttempts)
	env.Variables["CIRU_STRIXLINK_MAX_IN_FLIGHT"] = fmt.Sprint(p.MaxInFlight)
	env.Variables["CIRU_STRIXLINK_CHUNK_BYTES"] = fmt.Sprint(p.SuggestedChunkBytes)
	content := env.DotEnv()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func printHumanReport(r testReport) {
	b := r.Benchmark
	stage0 := "peer (" + r.PeerIP + ")"
	if b.FasterSender == "local" {
		stage0 = "local (" + r.LocalIP + ")"
	}
	fmt.Printf("USB4 route: %s -> %s via %s (verified)\n", r.LocalIP, r.PeerIP, r.Interface)
	fmt.Printf("Path MTU: %t (%s)\n", r.PathMTUPassed, r.PathMTUDetail)
	fmt.Printf("RTT: p50 %.3f ms, p95 %.3f ms, p99 %.3f ms (%d samples)\n", b.RTT.P50Ms, b.RTT.P95Ms, b.RTT.P99Ms, b.RTT.Samples)
	fmt.Printf("Integrity: local->peer=%t peer->local=%t (%d MiB each)\n", b.Integrity.UploadOK, b.Integrity.DownloadOK, b.Integrity.BytesEach>>20)
	fmt.Printf("Reconnect: %d/%d\n", b.ReconnectPassed, b.ReconnectTotal)
	fmt.Printf("Throughput: local->peer %.3f Gb/s, peer->local %.3f Gb/s, asymmetry %.2fx\n", b.Upload.Gbps, b.Download.Gbps, b.AsymmetryRatio)
	fmt.Printf("Quality: %s (ready=%t) - %s\n", b.Policy.Class, b.Policy.Ready, b.Policy.Reason)
	fmt.Printf("Recommended stage 0 / bulk sender: %s\n", stage0)
	fmt.Printf("Runtime policy: heartbeat=%dms timeout=%dms retries=%d in-flight=%d chunk=%dKiB\n", b.Policy.HeartbeatIntervalMs, b.Policy.PeerTimeoutMs, b.Policy.ReconnectAttempts, b.Policy.MaxInFlight, b.Policy.SuggestedChunkBytes>>10)
}

func printJSON(v any) {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	if err := e.Encode(v); err != nil {
		fail(err)
	}
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
