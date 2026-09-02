package link

import "testing"

func TestRoleAddresses(t *testing.T) {
	a, peer, err := RoleAddresses("10.77.77.0/30", "a")
	if err != nil || a != "10.77.77.1/30" || peer != "10.77.77.2" {
		t.Fatalf("got %q %q %v", a, peer, err)
	}
	b, peer, err := RoleAddresses("10.77.77.0/30", "stage0")
	if err != nil || b != "10.77.77.2/30" || peer != "10.77.77.1" {
		t.Fatalf("got %q %q %v", b, peer, err)
	}
}

func TestRoleAddressesRejectsWideSubnet(t *testing.T) {
	if _, _, err := RoleAddresses("10.77.77.0/24", "a"); err == nil {
		t.Fatal("expected error")
	}
}
