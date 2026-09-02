package transport

import "testing"

func readyLocal() Report {
	return Report{Interface: "thunderbolt0", Peer: "10.77.77.2", Portable: Mode{Ready: true}}
}

func TestGeneratePortableEnvironmentIsModelNeutral(t *testing.T) {
	e, err := GenerateEnvironment(readyLocal(), nil, "portable", "generic")
	if err != nil {
		t.Fatal(err)
	}
	if e.Variables["NCCL_SOCKET_IFNAME"] != "=thunderbolt0" || e.Variables["CIRU_STRIXLINK_MODE"] != "portable" {
		t.Fatalf("got %#v", e)
	}
	if _, exists := e.Variables["VLLM_NHI_DEVICE"]; exists {
		t.Fatal("generic portable environment leaked a vLLM-specific variable")
	}
}

func TestNHIRequiresPairReconciliation(t *testing.T) {
	r := readyLocal()
	e := exactEndpoint()
	e.Device = "/dev/tbstream0"
	r.Endpoints = []Endpoint{e}
	if _, err := GenerateEnvironment(r, nil, "nhi", "generic"); err == nil {
		t.Fatal("expected pair reconciliation error")
	}
	p := PairReport{PortableReady: true, NHIReady: true, LeaseAvailable: true}
	env, err := GenerateEnvironment(r, &p, "nhi", "vllm")
	if err != nil {
		t.Fatal(err)
	}
	if env.Variables["VLLM_NHI_DEVICE"] != "/dev/tbstream0" {
		t.Fatalf("got %#v", env)
	}
}

func TestEnvironmentRefusesHalfPairBeforeFallback(t *testing.T) {
	p := PairReport{PortableReady: true, Fallback: Fallback{CleanupRequired: true}}
	if _, err := GenerateEnvironment(readyLocal(), &p, "portable", "generic"); err == nil {
		t.Fatal("expected partial-pair cleanup error")
	}
}
