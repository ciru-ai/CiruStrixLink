package ui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func fakeLaunchController(host, username string, rank int, active bool, actions *[]string, mu *sync.Mutex) *launchController {
	state, sub, pid := "inactive", "dead", "0"
	if active {
		state, sub, pid = "active", "running", "123"
	}
	return &launchController{
		enabled: true, hostname: host, username: username, homeDir: "/home/" + username, helper: "/usr/bin/true",
		helperOK:  func(string) bool { return true },
		controlOK: func(context.Context) bool { return true },
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "systemctl" {
				if len(args) > 0 && args[0] == "--user" {
					return []byte("ActiveState=inactive\n"), nil
				}
				return []byte(fmt.Sprintf("LoadState=loaded\nActiveState=%s\nSubState=%s\nMainPID=%s\n", state, sub, pid)), nil
			}
			if name == "sudo" {
				mu.Lock()
				*actions = append(*actions, host+":"+args[3])
				mu.Unlock()
				return nil, nil
			}
			return nil, fmt.Errorf("unexpected command %s", name)
		},
		readFile: func(path string) ([]byte, error) {
			switch {
			case path == "/proc/meminfo":
				return []byte("MemTotal:       131072000 kB\nMemAvailable:  16777216 kB\n"), nil
			case strings.HasSuffix(path, "/dflash-tokens"):
				return []byte("5\n"), nil
			case strings.HasSuffix(path, "/prefix-cache-enabled"):
				return []byte("0\n"), nil
			case strings.Contains(path, "/node-"):
				return []byte(fmt.Sprintf("NODE_RANK=%d\n", rank)), nil
			case strings.Contains(path, "/context-"):
				return []byte("CONTEXT_PROFILE=128k\nMAX_MODEL_LEN=131200\nKV_CACHE_BYTES=12884901888\nPREFIX_CACHE_CPU_BYTES=8589934592\n"), nil
			default:
				return nil, os.ErrNotExist
			}
		},
	}
}

func TestLaunchControllerInspectsFixedNHIService(t *testing.T) {
	var actions []string
	var mu sync.Mutex
	c := fakeLaunchController("sozo", "halo", 0, true, &actions, &mu)
	s := c.inspect(context.Background())
	if !s.Installed || s.State != "loaded" || s.Rank != 0 || s.Profile != 2 || s.Context != 131200 || s.KVBytes != 12<<30 || s.RAMTotalBytes != 125<<30 || s.RAMAvailBytes != 16<<30 || s.RAMUsedBytes != 109<<30 || !s.DFlashKnown || s.DFlashTokens != 5 || !s.PrefixKnown || s.PrefixCache {
		t.Fatalf("unexpected status: %#v", s)
	}
}

func TestLaunchRuntimeRequiresMatchingRankSettings(t *testing.T) {
	local := launchNodeStatus{DFlashKnown: true, DFlashTokens: 5, PrefixKnown: true, PrefixCache: false}
	peer := launchNodeStatus{DFlashKnown: true, DFlashTokens: 5, PrefixKnown: true, PrefixCache: false}
	model := launchModel()
	applyLaunchRuntime(&model, local, peer)
	if !model.SpeculationKnown || model.Speculation != "DFlash2 · 5 draft tokens" || !model.PrefixCacheKnown || model.PrefixCache {
		t.Fatalf("unexpected matched runtime: %#v", model)
	}

	peer.DFlashTokens = 3
	peer.PrefixCache = true
	model = launchModel()
	applyLaunchRuntime(&model, local, peer)
	if model.SpeculationKnown || model.Speculation != "Ranks disagree" || model.PrefixCacheKnown {
		t.Fatalf("unexpected mismatched runtime: %#v", model)
	}

	local.DFlashTokens, peer.DFlashTokens = 0, 0
	model = launchModel()
	applyLaunchRuntime(&model, local, peer)
	if !model.SpeculationKnown || model.Speculation != "Disabled · target-only" {
		t.Fatalf("unexpected target-only runtime: %#v", model)
	}
}

func TestSystemMemoryIgnoresMalformedValues(t *testing.T) {
	total, available := systemMemory(func(string) ([]byte, error) {
		return []byte("MemTotal: 1000 kB\nNoise: nope\nMemAvailable: 250 kB\n"), nil
	})
	if total != 1000*1024 || available != 250*1024 {
		t.Fatalf("total=%d available=%d", total, available)
	}
}

func TestLaunchControllerMapsNixOSHelperActions(t *testing.T) {
	var calls []string
	c := &launchController{
		enabled: true, username: "crown", helper: launchNixHelperPath, helperKind: "nixos",
		helperOK: func(string) bool { return true },
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return nil, nil
		},
	}
	if err := c.apply(context.Background(), launchNodeRequest{Action: "configure", Profile: 3}); err != nil {
		t.Fatal(err)
	}
	if err := c.apply(context.Background(), launchNodeRequest{Action: "unload"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.HasSuffix(calls[0], "glm53-nhi-service-control context-256k") || !strings.HasSuffix(calls[1], "glm53-nhi-service-control stop") {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestLaunchControllerReadsPrivilegedFastLinkStatus(t *testing.T) {
	var call string
	c := &launchController{
		enabled: true, helper: launchNixHelperPath, helperKind: "nixos",
		helperOK: func(string) bool { return true },
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			call = name + " " + strings.Join(args, " ")
			return []byte(`{"hostname":"sozo","local_address":"10.77.77.2","peer":"10.77.77.1"}`), nil
		},
	}
	report, err := c.transport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if call != "sudo -n /run/current-system/sw/bin/glm53-nhi-service-control transport-status" {
		t.Fatalf("call = %q", call)
	}
	if report.Hostname != "sozo" || report.LocalAddress != "10.77.77.2" || report.Peer != "10.77.77.1" {
		t.Fatalf("report = %#v", report)
	}
}

func TestLaunchStatusRequiresTwoComplementaryRanks(t *testing.T) {
	local := launchNodeStatus{Hostname: "sozo", Rank: 0, Installed: true, State: "stopped", Profile: 2, ControlEnabled: true}
	peer := launchNodeStatus{Hostname: "ciru", Rank: 1, Installed: true, State: "stopped", Profile: 2, ControlEnabled: true}
	s := combineLaunchStatus(local, &peer, "ok", "", true)
	if s.State != "unloaded" || !s.CanLoad || s.CanUnload || s.SelectedProfile != 2 {
		t.Fatalf("unexpected unloaded status: %#v", s)
	}
	peer.State = "loaded"
	s = combineLaunchStatus(local, &peer, "ok", "", true)
	if s.State != "partial" || s.CanLoad || !s.CanUnload {
		t.Fatalf("unexpected partial status: %#v", s)
	}
	local.State = "loaded"
	s = combineLaunchStatus(local, &peer, "ok", "", true)
	if s.State != "loaded" || !s.CanUnload {
		t.Fatalf("unexpected loaded status: %#v", s)
	}
}

func TestLaunchStatusBlocksLoadWhileMainModelIsActive(t *testing.T) {
	local := launchNodeStatus{Hostname: "sozo", Rank: 0, Installed: true, State: "stopped", Profile: 3, ControlEnabled: true, CompetingModel: "qwen-main.service"}
	peer := launchNodeStatus{Hostname: "ciru", Rank: 1, Installed: true, State: "stopped", Profile: 3, ControlEnabled: true}
	s := combineLaunchStatus(local, &peer, "ok", "", true)
	if s.CanLoad || len(s.Blockers) == 0 || !strings.Contains(s.Blockers[len(s.Blockers)-1], "sozo") {
		t.Fatalf("unexpected competing-model status: %#v", s)
	}
}

func TestLaunchUnloadStopsRankOneBeforeRankZero(t *testing.T) {
	var actions []string
	var mu sync.Mutex
	agent := NewAgent(AgentConfig{Version: "test", Interface: "auto", LocalIP: "127.0.0.1", Token: "secret", ModelControl: true})
	agent.launch = fakeLaunchController("ciru", "crown", 1, true, &actions, &mu)
	peerServer := httptest.NewServer(agent)
	defer peerServer.Close()
	host, port := splitHostPort(t, peerServer.URL)

	console, err := NewConsole(ConsoleConfig{Version: "test", Peer: host, AgentPort: port, Token: "secret", ModelControl: true})
	if err != nil {
		t.Fatal(err)
	}
	console.launch = fakeLaunchController("sozo", "halo", 0, true, &actions, &mu)
	server := httptest.NewServer(console)
	defer server.Close()

	postJSON(t, server, "/api/launch", `{"action":"unload","confirmed":true}`, http.StatusOK)
	mu.Lock()
	defer mu.Unlock()
	if len(actions) != 2 || actions[0] != "ciru:unload" || actions[1] != "sozo:unload" {
		t.Fatalf("node actions = %#v, want rank 1 then rank 0", actions)
	}
}

func TestLaunchMutationRequiresConfirmationAndToken(t *testing.T) {
	c, err := NewConsole(ConsoleConfig{Version: "test", ModelControl: true})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(c)
	defer srv.Close()
	postJSON(t, srv, "/api/launch", `{"action":"unload"}`, http.StatusBadRequest)
	postJSON(t, srv, "/api/launch", `{"action":"unload","confirmed":true}`, http.StatusForbidden)

	a := httptest.NewServer(NewAgent(AgentConfig{Version: "test", Interface: "auto", LocalIP: "127.0.0.1", ModelControl: true}))
	defer a.Close()
	postJSON(t, a, "/api/agent/launch/node", `{"action":"unload"}`, http.StatusForbidden)
}

func TestModelNodeRejectsUnprivilegedExecution(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test is meaningful only for an unprivileged test user")
	}
	if err := RunModelNode("unload", "crown", 0, ""); err == nil || !strings.Contains(err.Error(), "must run as root") {
		t.Fatalf("got %v", err)
	}
}

func TestLaunchControllerMapsGenericTransportHelper(t *testing.T) {
	var call string
	c := &launchController{
		enabled: true, username: "operator", helper: launchHelperPath, helperKind: "ciru-strixlink", peer: "10.77.77.2",
		helperOK: func(string) bool { return true },
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			call = name + " " + strings.Join(args, " ")
			return []byte(`{"hostname":"peer","local_address":"10.77.77.1","peer":"10.77.77.2"}`), nil
		},
	}
	if _, err := c.transport(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "sudo -n /usr/local/bin/ciru-strixlink model-node transport-status --user operator --peer 10.77.77.2"
	if call != want {
		t.Fatalf("call = %q, want %q", call, want)
	}
}
