// Package install builds explicit, allowlisted installation plans. It never
// changes kernels, reboots, or edits declarative operating-system state.
package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/ciru-ai/CiruStrixLink/internal/prereq"
)

type Action struct {
	Type       string   `json:"type"`
	Components []string `json:"components"`
	Command    string   `json:"command,omitempty"`
	Args       []string `json:"args,omitempty"`
	Source     string   `json:"source,omitempty"`
	Target     string   `json:"target,omitempty"`
	Summary    string   `json:"summary"`
	HelpURL    string   `json:"help_url,omitempty"`
	CanApply   bool     `json:"can_apply"`
}

type Plan struct {
	SchemaVersion  int      `json:"schema_version"`
	PackageManager string   `json:"package_manager,omitempty"`
	ApplyRequested bool     `json:"apply_requested"`
	CanApply       bool     `json:"can_apply"`
	AlreadyReady   []string `json:"already_ready,omitempty"`
	Actions        []Action `json:"actions"`
	Warnings       []string `json:"warnings,omitempty"`
}

type Options struct {
	ToolVersion     string
	Components      []string
	IncludeOptional bool
	InstallSelf     bool
	Prefix          string
	Apply           bool
	Force           bool
}

var componentPackages = map[string]map[string]string{
	"apt": {
		"iproute2": "iproute2", "ping": "iputils-ping", "ethtool": "ethtool",
		"networkmanager": "network-manager", "modprobe": "kmod",
	},
	"dnf": {
		"iproute2": "iproute", "ping": "iputils", "ethtool": "ethtool",
		"networkmanager": "NetworkManager", "modprobe": "kmod",
	},
	"pacman": {
		"iproute2": "iproute2", "ping": "iputils", "ethtool": "ethtool",
		"networkmanager": "networkmanager", "modprobe": "kmod",
	},
}

func packagesFor(manager string, components []string) (packages, supported, unsupported []string) {
	mapping := componentPackages[manager]
	seenPackages := make(map[string]bool)
	for _, id := range components {
		pkg, ok := mapping[id]
		if !ok {
			unsupported = append(unsupported, id)
			continue
		}
		supported = append(supported, id)
		if !seenPackages[pkg] {
			packages = append(packages, pkg)
			seenPackages[pkg] = true
		}
	}
	sort.Strings(packages)
	sort.Strings(supported)
	sort.Strings(unsupported)
	return
}

func packageCommand(manager string, packages []string) (string, []string) {
	switch manager {
	case "apt":
		return "apt-get", append([]string{"install", "-y"}, packages...)
	case "dnf":
		return "dnf", append([]string{"install", "-y"}, packages...)
	case "pacman":
		return "pacman", append([]string{"-S", "--needed", "--noconfirm"}, packages...)
	default:
		return "", nil
	}
}

func selectedComponents(report prereq.Report, o Options) ([]string, []string, error) {
	byID := make(map[string]prereq.Component)
	for _, c := range report.Components {
		byID[c.ID] = c
	}
	requested := append([]string(nil), o.Components...)
	if len(requested) == 0 {
		for _, c := range report.Components {
			if c.Status == prereq.Available || (!c.Required && !o.IncludeOptional) {
				continue
			}
			requested = append(requested, c.ID)
		}
	}
	seen := make(map[string]bool)
	var selected, ready []string
	for _, id := range requested {
		if seen[id] {
			continue
		}
		seen[id] = true
		c, ok := byID[id]
		if !ok {
			return nil, nil, fmt.Errorf("unknown component %q", id)
		}
		if c.Status == prereq.Available {
			ready = append(ready, id)
		} else {
			selected = append(selected, id)
		}
	}
	sort.Strings(selected)
	sort.Strings(ready)
	return selected, ready, nil
}

func helpFor(report prereq.Report, id string) string {
	for _, c := range report.Components {
		if c.ID == id {
			return c.HelpURL
		}
	}
	return prereq.DocsBase + "index.md"
}

func Build(o Options) (Plan, error) {
	if o.Prefix == "" {
		o.Prefix = "/usr/local"
	}
	if !filepath.IsAbs(o.Prefix) {
		return Plan{}, errors.New("--prefix must be absolute")
	}
	report := prereq.Check(o.ToolVersion)
	selected, ready, err := selectedComponents(report, o)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{SchemaVersion: 1, PackageManager: report.System.PackageManager, ApplyRequested: o.Apply, AlreadyReady: ready}
	packages, supported, unsupported := packagesFor(report.System.PackageManager, selected)
	if len(packages) > 0 {
		cmd, args := packageCommand(report.System.PackageManager, packages)
		p.Actions = append(p.Actions, Action{Type: "packages", Components: supported, Command: cmd, Args: args, Summary: "install allowlisted user-space prerequisites", CanApply: true})
	}
	for _, id := range unsupported {
		p.Actions = append(p.Actions, Action{Type: "manual", Components: []string{id}, Summary: "this component requires distribution, kernel, hardware, or policy-specific work", HelpURL: helpFor(report, id), CanApply: false})
	}
	if o.InstallSelf {
		source, err := os.Executable()
		if err != nil {
			return Plan{}, err
		}
		source, _ = filepath.EvalSymlinks(source)
		target := filepath.Join(o.Prefix, "bin", "ciru-strixlink")
		canApply := runtime.GOOS == "linux"
		p.Actions = append(p.Actions, Action{Type: "self", Components: []string{"ciru_strixlink"}, Source: source, Target: target, Summary: "install the current CiruStrixLink binary", CanApply: canApply})
	}
	if report.System.PackageManager == "nixos" && len(selected) > 0 {
		p.Warnings = append(p.Warnings, "NixOS changes remain declarative; use the linked NixOS configuration instead of an imperative package install")
	}
	for _, a := range p.Actions {
		if a.CanApply {
			p.CanApply = true
		}
	}
	if len(p.Actions) == 0 {
		p.Warnings = append(p.Warnings, "no installation action is needed for the selected components")
	}
	return p, nil
}

func root() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	b, err := exec.Command("id", "-u").Output()
	return err == nil && strings.TrimSpace(string(b)) == "0"
}

func installSelf(source, target string, force bool) error {
	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("%s already exists; use --force after reviewing the plan", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := fmt.Sprintf("%s.tmp.%d", target, os.Getpid())
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	ok = true
	return nil
}

func Apply(p Plan, force bool) error {
	if !root() {
		return errors.New("installation requires root; rerun the reviewed plan with sudo and --apply")
	}
	for _, a := range p.Actions {
		if !a.CanApply {
			continue
		}
		switch a.Type {
		case "packages":
			cmd := exec.Command(a.Command, a.Args...)
			cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("install packages for %s: %w", strings.Join(a.Components, ","), err)
			}
		case "self":
			if err := installSelf(a.Source, a.Target, force); err != nil {
				return err
			}
		}
	}
	return nil
}
