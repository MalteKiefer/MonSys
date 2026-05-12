//go:build linux

package security

import (
	"context"
	"strings"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// ufwStatus reports the Ubuntu/Debian "uncomplicated firewall" daemon by
// parsing `ufw status verbose`. The function returns (zero, false) when ufw
// is not installed or the call fails — typical on hosts that use nftables or
// iptables directly. Rule count is derived from lines beginning with the
// ALLOW/DENY/REJECT action verbs ufw emits in its verbose mode.
func ufwStatus(ctx context.Context) (apitypes.FirewallStatus, bool) {
	if !safeexec.Available("ufw") {
		return apitypes.FirewallStatus{}, false
	}
	out, err := safeexec.RunWithTimeout(ctx, ufwCmdTimeout, "ufw", "status", "verbose")
	if err != nil {
		logger.Debug("ufw status failed", "err", err)
		return apitypes.FirewallStatus{}, false
	}
	fs := apitypes.FirewallStatus{Engine: "ufw", SnapshotExcerpt: capExcerpt(out)}
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "Status:"):
			fs.Active = strings.Contains(l, "active")
		case strings.HasPrefix(l, "Default:"):
			parseUfwDefaults(strings.TrimSpace(strings.TrimPrefix(l, "Default:")), &fs)
		}
	}
	// Rule count = lines that start with "ALLOW" / "DENY" / "REJECT".
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "ALLOW") || strings.HasPrefix(l, "DENY") || strings.HasPrefix(l, "REJECT") {
			fs.RuleCount++
		}
	}
	return fs, true
}

// parseUfwDefaults reads the ufw "Default: deny (incoming), allow (outgoing),
// disabled (routed)" header line and stamps the chain policies onto fs. It is
// extracted from ufwStatus so the line format can be unit-tested in
// isolation later if needed.
func parseUfwDefaults(rest string, fs *apitypes.FirewallStatus) {
	for _, p := range strings.Split(rest, ",") {
		p = strings.TrimSpace(p)
		switch {
		case strings.Contains(p, "(incoming)"):
			fs.DefaultInput = strings.Fields(p)[0]
		case strings.Contains(p, "(outgoing)"):
			fs.DefaultOutput = strings.Fields(p)[0]
		case strings.Contains(p, "(routed)"):
			fs.DefaultForward = strings.Fields(p)[0]
		}
	}
}
