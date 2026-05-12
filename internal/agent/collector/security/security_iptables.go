//go:build linux

package security

import (
	"context"
	"strings"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// iptablesStatus reports the legacy iptables ruleset by parsing the output of
// `iptables-save`. On hosts where iptables is just an nftables-backed
// compatibility shim the data overlaps with nftStatus — the dispatcher emits
// both and the UI deduplicates. Rule lines are recognised by the `-A <chain>`
// prefix iptables-save uses; chain default policies come from `:CHAIN POLICY`
// header lines.
func iptablesStatus(ctx context.Context) (apitypes.FirewallStatus, bool) {
	if !safeexec.Available("iptables-save") {
		return apitypes.FirewallStatus{}, false
	}
	out, err := safeexec.RunWithTimeout(ctx, iptablesCmdTimeout, "iptables-save")
	if err != nil || len(out) == 0 {
		if err != nil {
			logger.Debug("iptables-save failed", "err", err)
		}
		return apitypes.FirewallStatus{}, false
	}
	fs := apitypes.FirewallStatus{
		Engine:          "iptables",
		Active:          true,
		SnapshotExcerpt: capExcerpt(out),
	}
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "-A "):
			fs.RuleCount++
		case strings.HasPrefix(l, ":INPUT"):
			fs.DefaultInput = parseIptablesChainPolicy(l)
		case strings.HasPrefix(l, ":OUTPUT"):
			fs.DefaultOutput = parseIptablesChainPolicy(l)
		case strings.HasPrefix(l, ":FORWARD"):
			fs.DefaultForward = parseIptablesChainPolicy(l)
		}
	}
	return fs, true
}

// parseIptablesChainPolicy extracts the second field of an iptables-save
// chain header (e.g. ":INPUT ACCEPT [0:0]" → "ACCEPT"). It returns "" when
// the line is too short to contain a policy verb.
func parseIptablesChainPolicy(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 {
		return ""
	}
	return f[1]
}
