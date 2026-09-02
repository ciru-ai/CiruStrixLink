package prereq

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	got := parseOSRelease("ID=nixos\nPRETTY_NAME=\"NixOS 26.05\"\n# ignored\n")
	if got["ID"] != "nixos" || got["PRETTY_NAME"] != "NixOS 26.05" {
		t.Fatalf("got %#v", got)
	}
}

func TestSummarizeReady(t *testing.T) {
	r := Report{Components: []Component{{Required: true, Status: Available}, {Required: false, Status: Missing}}}
	summarize(&r)
	if !r.Ready || r.OverallStatus != "ready" {
		t.Fatalf("got %#v", r)
	}
}

func TestSummarizeNeedsAction(t *testing.T) {
	r := Report{Components: []Component{{Required: true, Status: Inactive}}}
	summarize(&r)
	if r.Ready || r.OverallStatus != "needs_action" {
		t.Fatalf("got %#v", r)
	}
}

func TestSummarizeUnsupportedWins(t *testing.T) {
	r := Report{Components: []Component{{Required: true, Status: Missing}, {Required: true, Status: Unsupported}}}
	summarize(&r)
	if r.Ready || r.OverallStatus != "unsupported" {
		t.Fatalf("got %#v", r)
	}
}

func TestComponentHelpDocumentsExist(t *testing.T) {
	files := []string{
		"linux.md", "strix-halo.md", "usb4.md", "thunderbolt-net.md",
		"iproute2.md", "iputils.md", "networkmanager.md", "ethtool.md",
		"permissions.md", "ubuntu-debian.md", "fedora-rhel.md", "arch.md",
		"nixos.md", "package-managers.md",
		"nhi.md",
	}
	for _, name := range files {
		if _, err := os.Stat(filepath.Join("..", "..", "docs", "install", name)); err != nil {
			t.Errorf("help document %s: %v", name, err)
		}
	}
}
