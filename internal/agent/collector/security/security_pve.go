//go:build linux

package security

import (
	"context"
	"strings"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// pveFirewallStatus reports the Proxmox VE firewall daemon. It runs as its
// own pve-firewall stack on top of iptables/nftables and exposes status via
// `pve-firewall status` (text). The binary lives under /usr/sbin and is
// usually only readable by root, but we still try — agents on Proxmox nodes
// often run with CAP_NET_ADMIN and can read the status without sudo.
//
// Output shape: "Status: enabled/running" on a configured node, or
// "Status: disabled/running" when the cluster firewall is off. The function
// flags Active based on whether "enabled" appears in that line.
func pveFirewallStatus(ctx context.Context) (apitypes.FirewallStatus, bool) {
	if !safeexec.Available("pve-firewall") {
		return apitypes.FirewallStatus{}, false
	}
	out, err := safeexec.RunWithTimeout(ctx, pveFirewallCmdTimeout, "pve-firewall", "status")
	if err != nil || len(out) == 0 {
		if err != nil {
			logger.Debug("pve-firewall status failed", "err", err)
		}
		return apitypes.FirewallStatus{}, false
	}
	fs := apitypes.FirewallStatus{
		Engine:          "pve-firewall",
		SnapshotExcerpt: capExcerpt(out),
	}
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "Status:") {
			fs.Active = strings.Contains(l, "enabled")
		}
	}
	return fs, true
}
