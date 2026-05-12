//go:build linux

// Package security collects firewall posture (ufw/nftables/iptables/pve-firewall),
// fail2ban jail status, and CrowdSec decisions on Linux hosts.
//
// All probes run via safeexec. Each backend is independent: missing tools or
// permission errors degrade silently to "not active" rather than returning
// errors, because a host that simply doesn't run fail2ban shouldn't make the
// whole tick fail.
//
// The collector is Linux-only because every backend it queries (ufw, nft,
// iptables-save, pve-firewall, fail2ban-client, cscli) is a Linux user-space
// tool. The package is therefore guarded with `//go:build linux` so cross
// builds for other GOOS values exclude it cleanly.
package security

import (
	"context"
	"log/slog"
	"time"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
)

// snapshotExcerptMax caps how many bytes of a firewall dump (ufw/nft/iptables
// output) are retained in FirewallStatus.SnapshotExcerpt. Anything beyond this
// is truncated so a host with a huge ruleset can't blow up the ingest payload.
const snapshotExcerptMax = 4 << 10 // 4 KiB

// Per-backend command timeouts. These are intentionally separate constants so
// a slow tool (e.g. cscli decoding a large JSON list) can be tuned without
// affecting the fast ones.
const (
	ufwCmdTimeout         = 5 * time.Second
	nftCmdTimeout         = 5 * time.Second
	iptablesCmdTimeout    = 5 * time.Second
	pveFirewallCmdTimeout = 5 * time.Second
	fail2banCmdTimeout    = 5 * time.Second
	crowdsecCmdTimeout    = 6 * time.Second
)

// logger is the package-scoped slog handle. Backend files (security_ufw.go,
// security_crowdsec.go, …) attach further key/value pairs via logger.With.
var logger = slog.With("component", "security")

// Collector implements collector.Source for firewall, fail2ban and CrowdSec
// posture. It holds no state; New returns a fresh instance per agent.
type Collector struct{}

// New returns a Security collector with default settings. It performs no I/O
// and never fails; backend availability is probed lazily inside Collect.
func New() *Collector { return &Collector{} }

// Name returns the collector identifier ("security") used in logs and metrics.
func (c *Collector) Name() string { return "security" }

// Collect probes every supported security backend in turn and, when at least
// one yields data, attaches a SecurityReport to batch. It never returns a
// non-nil error: per-backend failures degrade silently because the agent tick
// must keep running even on hosts that only ship a subset of the tools.
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

// firewallSnapshot probes every supported firewall backend (ufw, nftables,
// iptables, pve-firewall) and returns the ones that responded. Hosts often
// have several tools installed at once (e.g. Proxmox nodes ship both
// pve-firewall and the raw iptables it manages); we report each independently
// and let the UI decide how to merge.
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

// capExcerpt returns the first snapshotExcerptMax bytes of b as a string. It
// is used by every firewall backend to bound the SnapshotExcerpt field, so
// that a single noisy host can't inflate the ingest payload.
func capExcerpt(b []byte) string {
	if len(b) <= snapshotExcerptMax {
		return string(b)
	}
	return string(b[:snapshotExcerptMax])
}
