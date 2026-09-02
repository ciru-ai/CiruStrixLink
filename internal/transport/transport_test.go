package transport

import "testing"

func report(host string, portable, localArm bool, endpoint *Endpoint) Report {
	local, peer := "10.77.77.1", "10.77.77.2"
	if host == "b" {
		local, peer = peer, local
	}
	r := Report{Hostname: host, LocalAddress: local, Peer: peer, Portable: Mode{Ready: portable}, NHI: Mode{Ready: endpoint != nil}, LocalArmReady: localArm}
	if endpoint != nil {
		r.Endpoints = []Endpoint{*endpoint}
	}
	return r
}

func TestReconcileRejectsDuplicatedOrWrongPeerReport(t *testing.T) {
	a := report("a", true, true, nil)
	p := Reconcile(a, a)
	if p.PairIdentityValid || p.PortableReady || p.ArmAllowed || p.NHIStatus != "invalid_pair" {
		t.Fatalf("got %#v", p)
	}
}

func TestReconcileDoesNotPromoteUnqualifiedExactPair(t *testing.T) {
	e := exactEndpoint()
	a := report("a", true, false, &e)
	b := report("b", true, false, &e)
	a.NHI.Ready = false
	b.NHI.Ready = false
	p := Reconcile(a, b)
	if p.NHIReady || p.NHIStatus != "blocked" {
		t.Fatalf("got %#v", p)
	}
}

func exactEndpoint() Endpoint {
	return Endpoint{RingSize: ExpectedRing, ThrottlingNS: ExpectedThrottle, InHopID: ExpectedNHIHopID, OutHopID: ExpectedNHIHopID, DevicePresent: true, ProductionFit: true, HolderScanComplete: true}
}

func TestReconcileAllowsOnlyTwoUnarmedReadyPeers(t *testing.T) {
	p := Reconcile(report("a", true, true, nil), report("b", true, true, nil))
	if !p.ArmAllowed || p.NHIStatus != "arm_allowed" {
		t.Fatalf("got %#v", p)
	}
}

func TestReconcileRejectsHalfPair(t *testing.T) {
	e := exactEndpoint()
	p := Reconcile(report("a", true, false, &e), report("b", true, false, nil))
	if p.NHIReady || !p.Fallback.CleanupRequired || p.NHIStatus != "partial" {
		t.Fatalf("got %#v", p)
	}
}

func TestReconcileAcceptsMatchingPair(t *testing.T) {
	e := exactEndpoint()
	p := Reconcile(report("a", true, false, &e), report("b", true, false, &e))
	if !p.NHIReady || !p.LeaseAvailable || p.NHIStatus != "ready" {
		t.Fatalf("got %#v", p)
	}
}

func TestReconcileReportsQualifiedPairInUse(t *testing.T) {
	e := exactEndpoint()
	e.Holders = []Holder{{PID: 42, Command: "runtime"}}
	p := Reconcile(report("a", true, false, &e), report("b", true, false, &e))
	if !p.NHIReady || !p.NHIInUse || p.LeaseAvailable || p.NHIStatus != "in_use" {
		t.Fatalf("got %#v", p)
	}
}
