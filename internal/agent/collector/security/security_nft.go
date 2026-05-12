//go:build linux

package security

import (
	"context"
	"strings"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// nftStatus reports the nftables ruleset by parsing `nft list ruleset`. If nft
// is installed but the ruleset is empty, the function still returns Active=true
// because nft being present and queryable is the operator's signal that
// nftables (not iptables-legacy) is in use; rule count then conveys "no rules
// configured". A failed call returns (zero, false) so iptables can be tried.
func nftStatus(ctx context.Context) (apitypes.FirewallStatus, bool) {
	if !safeexec.Available("nft") {
		return apitypes.FirewallStatus{}, false
	}
	out, err := safeexec.RunWithTimeout(ctx, nftCmdTimeout, "nft", "list", "ruleset")
	if err != nil || len(out) == 0 {
		if err != nil {
			logger.Debug("nft list ruleset failed", "err", err)
		}
		return apitypes.FirewallStatus{}, false
	}
	fs := apitypes.FirewallStatus{
		Engine:          "nftables",
		Active:          true,
		SnapshotExcerpt: capExcerpt(out),
	}
	// "rule" line count is a reasonable proxy for "configured rules".
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		if isNftRuleLine(l) {
			fs.RuleCount++
		}
		// "type filter hook input priority …; policy drop;"
		if strings.Contains(l, "hook input") && strings.Contains(l, "policy ") {
			fs.DefaultInput = extractNftPolicy(l)
		}
		if strings.Contains(l, "hook output") && strings.Contains(l, "policy ") {
			fs.DefaultOutput = extractNftPolicy(l)
		}
		if strings.Contains(l, "hook forward") && strings.Contains(l, "policy ") {
			fs.DefaultForward = extractNftPolicy(l)
		}
	}
	return fs, true
}

// isNftRuleLine reports whether l looks like a single nftables rule body
// (tcp/udp/ip/ip6/ct/iif/oif match prefixes) as opposed to a chain header,
// table declaration, or counter line.
func isNftRuleLine(l string) bool {
	return strings.HasPrefix(l, "tcp ") ||
		strings.HasPrefix(l, "udp ") ||
		strings.HasPrefix(l, "ip ") ||
		strings.HasPrefix(l, "ip6 ") ||
		strings.HasPrefix(l, "ct ") ||
		strings.HasPrefix(l, "iif ") ||
		strings.HasPrefix(l, "oif ")
}

// extractNftPolicy pulls the policy verdict (accept/drop/queue/…) out of an
// nft chain header such as `type filter hook input priority 0; policy drop;`.
// It returns "" when the line lacks the expected `policy <verb>` token.
func extractNftPolicy(line string) string {
	idx := strings.Index(line, "policy ")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len("policy "):]
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimRight(fields[0], ";")
}
