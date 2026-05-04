// Package agent wires config + collectors + transport + spool into a runnable
// loop. Kept in its own package so cmd/mon-agent stays a thin entry point.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent/buffer"
	"github.com/MalteKiefer/MonSys/internal/agent/collector"
	"github.com/MalteKiefer/MonSys/internal/agent/collector/identity"
	"github.com/MalteKiefer/MonSys/internal/agent/collector/packages"
	"github.com/MalteKiefer/MonSys/internal/agent/collector/security"
	"github.com/MalteKiefer/MonSys/internal/agent/collector/virt"
	"github.com/MalteKiefer/MonSys/internal/agent/collector/workload"
	"github.com/MalteKiefer/MonSys/internal/agent/config"
	"github.com/MalteKiefer/MonSys/internal/agent/transport"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
	"github.com/MalteKiefer/MonSys/internal/shared/version"

	"github.com/shirou/gopsutil/v4/host"
)

type Agent struct {
	Cfg        config.Config
	Client     *transport.Client
	Spool      *buffer.Spool
	Collectors []collector.Source
	Inventory  []collector.InventoryProvider

	agentKey string

	// Active interval after merging the server-provided config; falls back
	// to Cfg.Interval() when no remote config is available.
	currentInterval time.Duration
	// Cached remote config and its last successful fetch time, for log
	// breadcrumbs and quiet-hour decisions.
	remote          *apitypes.AgentConfigResolved
	remoteFetchedAt time.Time
}

func New(cfg config.Config) (*Agent, error) {
	c, err := transport.New(cfg.ServerURL,
		transport.WithCAFile(cfg.TLS.CAFile),
		transport.WithPin(cfg.TLS.CAPinSHA256),
	)
	if err != nil {
		return nil, err
	}
	if cfg.TLS.Insecure {
		_ = transport.WithInsecureSkipVerify()(c) // dev-only escape hatch
	}

	sp, err := buffer.New(cfg.BufferDir, int64(cfg.BufferMaxMB)*1024*1024)
	if err != nil {
		return nil, err
	}

	sys := collector.NewSystem()
	disk := collector.NewDisk()
	netc := collector.NewNet()

	collectors := []collector.Source{sys, disk, netc}
	inventory := []collector.InventoryProvider{sys, disk, netc}

	// Docker collector is opt-in via reachable socket. We probe once at init;
	// if the socket appears later (e.g. dockerd restart), the agent will need
	// to be restarted. That's acceptable — a probe-on-every-tick is wasteful.
	if d := workload.NewDocker(cfg.DockerEndpoint); d.Available(context.Background()) {
		collectors = append(collectors, d)
		inventory = append(inventory, d)
	}

	if pc, ok := packages.New(cfg.Packages); ok {
		collectors = append(collectors, pc)
	}

	if v := virt.New(); v.Available() {
		inventory = append(inventory, v)
		collectors = append(collectors, v)
	}

	id := identity.New(cfg.Redact)
	inventory = append(inventory, id)
	collectors = append(collectors, id)

	collectors = append(collectors, security.New())

	a := &Agent{
		Cfg:        cfg,
		Client:     c,
		Spool:      sp,
		Collectors: collectors,
		Inventory:  inventory,
	}
	return a, nil
}

// Bootstrap exchanges a one-time token for an agent_key when key file is missing.
// On success, the key is persisted with mode 0400.
func (a *Agent) Bootstrap(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("bootstrap token empty")
	}

	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return err
	}
	machineID := strings.TrimSpace(readFile("/etc/machine-id"))

	req := apitypes.AgentRegisterRequest{
		Hostname:     info.Hostname,
		MachineID:    machineID,
		OS:           runtime.GOOS,
		Kernel:       info.KernelVersion,
		Arch:         runtime.GOARCH,
		Distro:       info.Platform + " " + info.PlatformVersion,
		AgentVersion: version.Version,
		Labels:       a.Cfg.Labels,
	}

	resp, err := a.Client.Register(ctx, token, req)
	if err != nil {
		return err
	}
	if resp.AgentKey == "" {
		return errors.New("server returned empty agent_key")
	}
	if err := writeKeyFile(a.Cfg.KeyFile, resp.AgentID+":"+resp.AgentKey); err != nil {
		return err
	}
	a.agentKey = resp.AgentKey
	slog.Info("bootstrap successful", "host_id", resp.AgentID, "key_file", a.Cfg.KeyFile)
	return nil
}

func (a *Agent) loadKey() error {
	b, err := os.ReadFile(a.Cfg.KeyFile) //nolint:gosec // path from agent config / fixed-by-design
	if err != nil {
		return err
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return errors.New("malformed key file (expected agent_id:agent_key)")
	}
	a.agentKey = parts[1]
	return nil
}

func (a *Agent) Run(ctx context.Context) error {
	if err := a.loadKey(); err != nil {
		return fmt.Errorf("agent key not present (%w); run with --bootstrap-token first", err)
	}

	// Try to pull server-managed config once at startup. On failure (server
	// older than agent or unreachable) we fall back to the local file's
	// values silently — the bootstrap-only flow still works.
	a.currentInterval = a.Cfg.Interval()
	a.refreshRemoteConfig(ctx)

	// Reload remote config every 5 minutes so admin edits propagate without
	// requiring an agent restart.
	cfgRefresh := time.NewTicker(5 * time.Minute)
	defer cfgRefresh.Stop()

	t := time.NewTicker(a.currentInterval)
	defer t.Stop()

	// Always send an initial inventory at startup so the server knows us.
	if err := a.tick(ctx, true); err != nil {
		slog.Warn("initial tick failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-cfgRefresh.C:
			if changed := a.refreshRemoteConfig(ctx); changed {
				t.Reset(a.currentInterval)
				slog.Info("agent config reloaded; ticker reset", "interval", a.currentInterval)
			}
		case <-t.C:
			if a.inQuietHours(time.Now()) {
				continue
			}
			if err := a.tick(ctx, false); err != nil {
				slog.Warn("tick failed", "err", err)
			}
		}
	}
}

// refreshRemoteConfig fetches the merged config from the server and applies
// it to the agent's runtime knobs. Returns true if any setting changed.
func (a *Agent) refreshRemoteConfig(ctx context.Context) bool {
	fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := a.Client.FetchConfig(fetchCtx, a.agentKey)
	if err != nil {
		slog.Warn("remote config fetch failed; keeping current settings", "err", err)
		return false
	}
	if r == nil {
		// Server doesn't expose config endpoint; treat as no-op.
		return false
	}
	a.remote = r
	a.remoteFetchedAt = time.Now()

	prev := a.currentInterval
	if r.Config.IntervalSeconds != nil && *r.Config.IntervalSeconds > 0 {
		a.currentInterval = time.Duration(*r.Config.IntervalSeconds) * time.Second
	} else {
		a.currentInterval = a.Cfg.Interval()
	}
	if r.Config.Labels != nil && len(r.Config.Labels) > 0 {
		// Remote labels add to (don't replace) yaml labels — yaml is
		// considered more authoritative for ground-truth identity tags.
		merged := map[string]string{}
		for k, v := range a.Cfg.Labels {
			merged[k] = v
		}
		for k, v := range r.Config.Labels {
			if _, taken := merged[k]; !taken {
				merged[k] = v
			}
		}
		a.Cfg.Labels = merged
	}
	slog.Info("remote config applied",
		"sources", strings.Join(r.SourceScopes, ","),
		"interval", a.currentInterval)
	return prev != a.currentInterval
}

// inQuietHours returns true when "now" falls inside the configured quiet
// window. We check the remote config only — yaml has no quiet-hour field.
// Days are 0=Sun..6=Sat. Window may wrap midnight (e.g. 22:00 → 06:00).
func (a *Agent) inQuietHours(now time.Time) bool {
	if a.remote == nil || a.remote.Config.QuietHours == nil {
		return false
	}
	q := a.remote.Config.QuietHours
	if !q.Enabled {
		return false
	}
	if len(q.Days) > 0 {
		match := false
		for _, d := range q.Days {
			if int(now.Weekday()) == d {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}
	startMin, ok1 := parseHHMM(q.Start)
	endMin, ok2 := parseHHMM(q.End)
	if !ok1 || !ok2 || startMin == endMin {
		return false
	}
	cur := now.Hour()*60 + now.Minute()
	if startMin < endMin {
		return cur >= startMin && cur < endMin
	}
	// Wraparound (e.g. 22:00 - 06:00).
	return cur >= startMin || cur < endMin
}

func parseHHMM(s string) (int, bool) {
	if len(s) < 4 || len(s) > 5 {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := atoiSafe(parts[0])
	m, err2 := atoiSafe(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func atoiSafe(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func (a *Agent) tick(ctx context.Context, withInventory bool) error {
	batch := apitypes.IngestRequest{SnapshotAt: time.Now().UTC()}

	if withInventory {
		snap := apitypes.InventorySnap{AgentVersion: version.Version, Labels: a.Cfg.Labels}
		for _, p := range a.Inventory {
			if err := p.Inventory(ctx, &snap); err != nil {
				slog.Warn("inventory provider failed", "err", err)
			}
		}
		snap.Sources = a.activeSources()
		batch.Inventory = &snap
	}

	for _, c := range a.Collectors {
		if err := c.Collect(ctx, &batch); err != nil {
			slog.Warn("collector failed", "name", c.Name(), "err", err)
		}
	}

	payload, err := json.Marshal(batch)
	if err != nil {
		return err
	}

	// First try direct send. If it fails, spool to disk and try draining
	// the spool on the next tick.
	if err := a.Client.Ingest(ctx, a.agentKey, payload); err != nil {
		slog.Warn("send failed; spooling", "err", err)
		return a.Spool.Append(batch)
	}
	// Send succeeded; flush any spooled batches.
	return a.Spool.Drain(func(raw []byte) error {
		return a.Client.Ingest(ctx, a.agentKey, raw)
	})
}

func (a *Agent) activeSources() []string {
	out := make([]string, 0, len(a.Collectors))
	for _, c := range a.Collectors {
		out = append(out, c.Name())
	}
	return out
}

func writeKeyFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content+"\n"), 0o400); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readFile(p string) string {
	b, err := os.ReadFile(p) //nolint:gosec // path from agent config / fixed-by-design
	if err != nil {
		return ""
	}
	return string(b)
}
