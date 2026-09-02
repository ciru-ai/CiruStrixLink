package install

import "testing"

func TestPackagesAreAllowlistedAndDeduplicated(t *testing.T) {
	packages, supported, unsupported := packagesFor("apt", []string{"ping", "iproute2", "ping", "thunderbolt_net"})
	if len(packages) != 2 || packages[0] != "iproute2" || packages[1] != "iputils-ping" {
		t.Fatalf("packages=%v", packages)
	}
	if len(supported) != 3 || len(unsupported) != 1 || unsupported[0] != "thunderbolt_net" {
		t.Fatalf("supported=%v unsupported=%v", supported, unsupported)
	}
}

func TestPackageCommands(t *testing.T) {
	cmd, args := packageCommand("dnf", []string{"iproute"})
	if cmd != "dnf" || len(args) != 3 || args[0] != "install" {
		t.Fatalf("got %s %v", cmd, args)
	}
}
