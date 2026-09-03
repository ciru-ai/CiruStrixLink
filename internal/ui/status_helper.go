package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/ciru-ai/CiruStrixLink/internal/transport"
)

// The optional helper is configured by the local operator, never by an HTTP
// request. Its only invoked action is transport-status; the console stays an
// ordinary user process and sudo retains control of the existing allowlist.
func inspectTransportForUI(version, iface, peer string) (transport.Report, error) {
	local := transport.Inspect(version, iface, peer)
	helper := os.Getenv("CIRU_STRIXLINK_STATUS_HELPER")
	if helper == "" || local.NHI.Status != "needs_privilege" {
		return local, nil
	}
	if !filepath.IsAbs(helper) {
		return local, fmt.Errorf("CIRU_STRIXLINK_STATUS_HELPER must be an absolute path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "sudo", "-n", helper, "transport-status").Output()
	if err != nil {
		return local, fmt.Errorf("read-only privileged transport inspection: %w", err)
	}
	var privileged transport.Report
	if err := json.Unmarshal(output, &privileged); err != nil {
		return local, fmt.Errorf("decode privileged transport report: %w", err)
	}
	if privileged.SchemaVersion != local.SchemaVersion || privileged.Hostname != local.Hostname ||
		privileged.Interface != local.Interface || privileged.LocalAddress != local.LocalAddress ||
		privileged.Peer != local.Peer {
		return local, fmt.Errorf("privileged transport report does not match this host and peer")
	}
	return privileged, nil
}
