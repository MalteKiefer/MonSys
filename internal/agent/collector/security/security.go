// Package security collects firewall posture (ufw/nftables/iptables),
// fail2ban jail status, and CrowdSec decisions.
//
// All probes run via safeexec. Each backend is independent: missing tools or
// permission errors degrade silently to "not active" rather than returning
// errors, because a host that simply doesn't run fail2ban shouldn't make the
// whole tick fail.
package security

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/safeexec"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

const snapshotExcerptMax = 4 << 10 // 4 KiB cap for firewall dump excerpts

type Collector struct{}

func New() *Collector { return &Collector{} }

func (c *Collector) Name() string { return "security" }

func (c *Collector) Collect(ctx context.Context, batch *apitypes.IngestRequest) error {
	rep := apitypes.SecurityReport{Time: time.Now().UTC()}

	if fw := c.firewallSnapshot(ctx); len(fw) > 0 {
		rep.Firewalls = fw
	}
	if jails := c.fail2banJails(ctx); len(jails) > 0 {
		rep.Fail2ban = jails
	}
	if dec := c.crowdsecDecisions(ctx); len(dec) > 0 {
		rep.CrowdSec = dec
	}

	if len(rep.Firewalls)+len(rep.Fail2ban)+len(rep.CrowdSec) == 0 {
		return nil
	}
	batch.Security = &rep
	return nil
}

// --- firewall -------------------------------------------------------------

func (c *Collector) firewallSnapshot(ctx context.Context) []apitypes.FirewallStatus {
	var out []apitypes.FirewallStatus

	if fs, ok := ufwStatus(ctx); ok {
		out = append(out, fs)
	}
	if fs, ok := nftStatus(ctx); ok {
		out = append(out, fs)
	}
	if fs, ok := iptablesStatus(ctx); ok {
		out = append(out, fs)
	}
	if fs, ok := pveFirewallStatus(ctx); ok {
		out = append(out, fs)
	}
	return out
}

// pveFirewallStatus reports the Proxmox VE firewall daemon. It runs as its
// own pve-firewall stack on top of iptables/nftables and exposes status via
// `pve-firewall status` (text). The binary lives under /usr/sbin and is
// usually only readable by root, but we still try — agents on Proxmox nodes
// often run with CAP_NET_ADMIN and can read the status without sudo.
func pveFirewallStatus(ctx context.Context) (apitypes.FirewallStatus, bool) {
	if !safeexec.Available("pve-firewall") {
		return apitypes.FirewallStatus{}, false
	}
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second, "pve-firewall", "status")
	if err != nil || len(out) == 0 {
		return apitypes.FirewallStatus{}, false
	}
	fs := apitypes.FirewallStatus{
		Engine:          "pve-firewall",
		SnapshotExcerpt: capExcerpt(out),
	}
	// Output shape: "Status: enabled/running" (the most useful line). On a
	// disabled cluster: "Status: disabled/running".
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "Status:") {
			fs.Active = strings.Contains(l, "enabled")
		}
	}
	return fs, true
}

func ufwStatus(ctx context.Context) (apitypes.FirewallStatus, bool) {
	if !safeexec.Available("ufw") {
		return apitypes.FirewallStatus{}, false
	}
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second, "ufw", "status", "verbose")
	if err != nil {
		return apitypes.FirewallStatus{}, false
	}
	fs := apitypes.FirewallStatus{Engine: "ufw", SnapshotExcerpt: capExcerpt(out)}
	for _, line := range strings.Split(string(out), "\n") {
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "Status:"):
			fs.Active = strings.Contains(l, "active")
		case strings.HasPrefix(l, "Default:"):
			// "Default: deny (incoming), allow (outgoing), disabled (routed)"
			rest := strings.TrimSpace(strings.TrimPrefix(l, "Default:"))
			parts := strings.Split(rest, ",")
			for _, p := range parts {
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

func nftStatus(ctx context.Context) (apitypes.FirewallStatus, bool) {
	if !safeexec.Available("nft") {
		return apitypes.FirewallStatus{}, false
	}
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second, "nft", "list", "ruleset")
	if err != nil || len(out) == 0 {
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
		if strings.HasPrefix(l, "tcp ") || strings.HasPrefix(l, "udp ") ||
			strings.HasPrefix(l, "ip ") || strings.HasPrefix(l, "ip6 ") ||
			strings.HasPrefix(l, "ct ") || strings.HasPrefix(l, "iif ") ||
			strings.HasPrefix(l, "oif ") {
			fs.RuleCount++
		}
		// "type filter hook input priority …; policy drop;"
		if strings.Contains(l, "hook input") && strings.Contains(l, "policy ") {
			fs.DefaultInput = extractPolicy(l)
		}
		if strings.Contains(l, "hook output") && strings.Contains(l, "policy ") {
			fs.DefaultOutput = extractPolicy(l)
		}
		if strings.Contains(l, "hook forward") && strings.Contains(l, "policy ") {
			fs.DefaultForward = extractPolicy(l)
		}
	}
	return fs, true
}

func iptablesStatus(ctx context.Context) (apitypes.FirewallStatus, bool) {
	if !safeexec.Available("iptables-save") {
		return apitypes.FirewallStatus{}, false
	}
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second, "iptables-save")
	if err != nil || len(out) == 0 {
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
			fs.DefaultInput = chainPolicy(l)
		case strings.HasPrefix(l, ":OUTPUT"):
			fs.DefaultOutput = chainPolicy(l)
		case strings.HasPrefix(l, ":FORWARD"):
			fs.DefaultForward = chainPolicy(l)
		}
	}
	return fs, true
}

func extractPolicy(line string) string {
	idx := strings.Index(line, "policy ")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len("policy "):]
	// trim ";" and surrounding whitespace
	return strings.TrimRight(strings.Fields(rest)[0], ";")
}

func chainPolicy(line string) string {
	// ":INPUT ACCEPT [0:0]"
	f := strings.Fields(line)
	if len(f) < 2 {
		return ""
	}
	return f[1]
}

func capExcerpt(b []byte) string {
	if len(b) <= snapshotExcerptMax {
		return string(b)
	}
	return string(b[:snapshotExcerptMax])
}

// --- fail2ban -------------------------------------------------------------

func (c *Collector) fail2banJails(ctx context.Context) []apitypes.Fail2banJailInfo {
	if !safeexec.Available("fail2ban-client") {
		return nil
	}
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second, "fail2ban-client", "status")
	if err != nil {
		return nil
	}
	jailLine := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Jail list:") {
			_, jailLine, _ = strings.Cut(line, ":")
			break
		}
	}
	if jailLine == "" {
		return nil
	}
	var jails []apitypes.Fail2banJailInfo
	for _, jail := range strings.Split(jailLine, ",") {
		jail = strings.TrimSpace(jail)
		if jail == "" {
			continue
		}
		info, err := fail2banJailDetail(ctx, jail)
		if err != nil {
			continue
		}
		jails = append(jails, info)
	}
	return jails
}

func fail2banJailDetail(ctx context.Context, jail string) (apitypes.Fail2banJailInfo, error) {
	out, err := safeexec.RunWithTimeout(ctx, 5*time.Second, "fail2ban-client", "status", jail)
	if err != nil {
		return apitypes.Fail2banJailInfo{}, err
	}
	info := apitypes.Fail2banJailInfo{Jail: jail}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case strings.HasSuffix(key, "Currently failed"):
			info.CurrentlyFailed = atoi(val)
		case strings.HasSuffix(key, "Total failed"):
			info.TotalFailed = atoi(val)
		case strings.HasSuffix(key, "Currently banned"):
			info.CurrentlyBanned = atoi(val)
		case strings.HasSuffix(key, "Total banned"):
			info.TotalBanned = atoi(val)
		case strings.HasSuffix(key, "Banned IP list"):
			if val != "" {
				info.BannedIPs = strings.Fields(val)
			}
		}
	}
	return info, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// --- crowdsec -------------------------------------------------------------

// cscli decisions list -o json emits a nested shape:
//
//	[{
//	  "created_at": "2026-05-04T16:16:48Z",
//	  "decisions": [
//	    {"id": 21046401, "origin": "crowdsec", "type": "ban",
//	     "scope": "Ip", "value": "1.2.3.4",
//	     "scenario": "crowdsecurity/ssh-bf",
//	     "duration": "10m0s"}
//	  ],
//	  "events": [...],
//	  ...
//	}, ...]
//
// Older versions sometimes emit a flat array of decisions with an "until"
// field instead of "duration"; we support both.
type cscliDecisionItem struct {
	ID       int    `json:"id"`
	Origin   string `json:"origin"`
	Type     string `json:"type"`
	Scope    string `json:"scope"`
	Value    string `json:"value"`
	Scenario string `json:"scenario"`
	Duration string `json:"duration"`
	Until    string `json:"until"`
}

type cscliAlert struct {
	CreatedAt string              `json:"created_at"`
	Decisions []cscliDecisionItem `json:"decisions"`
}

func (c *Collector) crowdsecDecisions(ctx context.Context) []apitypes.CrowdsecDecision {
	if !safeexec.Available("cscli") {
		return nil
	}
	out, err := safeexec.RunWithTimeout(ctx, 6*time.Second, "cscli", "decisions", "list", "-o", "json")
	if err != nil {
		return nil
	}

	// Try nested shape first; fall back to the flat one.
	var alerts []cscliAlert
	flat := false
	if err := json.Unmarshal(out, &alerts); err != nil || (len(alerts) > 0 && alerts[0].Decisions == nil && alerts[0].CreatedAt == "") {
		flat = true
	}
	var rawFlat []cscliDecisionItem
	if flat {
		if err := json.Unmarshal(out, &rawFlat); err != nil {
			return nil
		}
	}

	decisions := make([]apitypes.CrowdsecDecision, 0, 8)
	add := func(d cscliDecisionItem, parentCreatedAt string) {
		decisions = append(decisions, apitypes.CrowdsecDecision{
			DecisionID: strconv.Itoa(d.ID),
			Origin:     d.Origin,
			Scope:      d.Scope,
			Target:     d.Value,
			Type:       d.Type,
			Reason:     d.Scenario,
			Until:      computeUntil(d, parentCreatedAt),
		})
	}
	if flat {
		for _, d := range rawFlat {
			add(d, "")
		}
	} else {
		for _, a := range alerts {
			for _, d := range a.Decisions {
				add(d, a.CreatedAt)
			}
		}
	}
	return decisions
}

// computeUntil resolves the decision's expiry to an absolute timestamp.
// Preference order: explicit "until" RFC3339 → parent created_at + duration
// → time.Now() + duration. Returns the zero time when none parse, which the
// frontend renders as a dash.
func computeUntil(d cscliDecisionItem, parentCreatedAt string) time.Time {
	if d.Until != "" {
		if t, err := time.Parse(time.RFC3339, d.Until); err == nil {
			return t
		}
	}
	if d.Duration == "" {
		return time.Time{}
	}
	dur, err := time.ParseDuration(d.Duration)
	if err != nil {
		return time.Time{}
	}
	base := time.Now().UTC()
	if parentCreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, parentCreatedAt); err == nil {
			base = t
		}
	}
	return base.Add(dur)
}
