//go:build linux

// Package agent wires config, collectors, transport, and the on-disk spool
// into a runnable agent loop. The package lives separately from cmd/mon-agent
// so the command stays a thin entry point.
//
// The whole package is currently Linux-only — it reads /etc/machine-id and
// the defaults baked into the config package assume a Linux filesystem
// layout. A future Windows port would supply a parallel agent_windows.go and
// a machineid_windows.go.
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

// Loop control knobs. Grouped here so operators reading the file can see every
// timing-related constant in one place.
const (
	// remoteConfigRefreshInterval is how often the agent re-fetches the
	// server-managed config so admin edits propagate without a restart.
	remoteConfigRefreshInterval = 5 * time.Minute
	// remoteConfigFetchTimeout caps each remote config fetch attempt.
	// Short enough that a wedged server doesn't stall the loop.
	remoteConfigFetchTimeout = 10 * time.Second
	// machineIDPath is the Linux per-host identifier seeded by systemd.
	machineIDPath = "/etc/machine-id"
)

// File-mode constants for the persisted agent key file. The directory is 0700
// (no one else may even list the file); the file itself is 0400 (read-only,
// owner-only) since we never rewrite it in place.
const (
	keyFileDirMode  os.FileMode = 0o700
	keyFileMode     os.FileMode = 0o400
	keyFilePartsSep             = ":"
	keyFileParts                = 2
)

// Time-of-day parsing constants (HH:MM strings used in quiet-hour configs).
const (
	hhmmMinLen = 4
	hhmmMaxLen = 5
	maxHour    = 23
	maxMinute  = 59
)

// loopLogger is the slog handle used by the main loop; tagging by component
// makes filtering across the multi-binary deployment easier.
var loopLogger = slog.With("component", "agent")

// Agent owns the runtime state of a running mon-agent: configuration, the
// HTTPS transport client, the on-disk spool, and the active collector and
// inventory provider lists.
type Agent struct {
	// Cfg is the merged config (yaml + env) the agent was started with.
	// The server may overlay this at runtime via refreshRemoteConfig.
	Cfg config.Config
	// Client is the shared HTTPS transport used for register, ingest, and
	// config-fetch calls.
	Client *transport.Client
	// Spool is the on-disk batch buffer used when the server is
	// unreachable.
	Spool *buffer.Spool
	// Collectors run on every tick; their output goes into IngestRequest.
	Collectors []collector.Source
	// Inventory providers run on the first tick (and any explicit
	// inventory refresh) to populate the InventorySnap payload.
	Inventory []collector.InventoryProvider

	agentKey string

	// currentInterval is the active cadence after merging the
	// server-provided config; falls back to Cfg.Interval() when no remote
	// config is available.
	currentInterval time.Duration
	// remote and remoteFetchedAt cache the last successful remote config
	// fetch, for log breadcrumbs and quiet-hour decisions.
	remote          *apitypes.AgentConfigResolved
	remoteFetchedAt time.Time
}

// New constructs an Agent by wiring the transport client, the on-disk spool,
// and every collector that probes available on this host. Docker, virt, and
// the package collector are opt-in based on host capability.
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

	collectors, inventory := buildCollectors(cfg)

	return &Agent{
		Cfg:        cfg,
		Client:     c,
		Spool:      sp,
		Collectors: collectors,
		Inventory:  inventory,
	}, nil
}

// buildCollectors assembles the collector and inventory provider lists for a
// given config. Extracted from New so the wiring is reviewable as a unit.
func buildCollectors(cfg config.Config) ([]collector.Source, []collector.InventoryProvider) {
	sys := collector.NewSystem()
	disk := collector.NewDisk()
	netc := collector.NewNet()

	collectors := []collector.Source{sys, disk, netc}
	inventory := []collector.InventoryProvider{sys, disk, netc}

	// Docker collector is opt-in via reachable socket. We probe once at
	// init; if the socket appears later (e.g. dockerd restart) the agent
	// needs a restart. A probe-on-every-tick would be wasteful.
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

	return collectors, inventory
}

// Bootstrap exchanges a one-time token for an agent_key when the key file is
// missing. On success the key is persisted with mode 0400 inside a 0700
// directory.
func (a *Agent) Bootstrap(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("bootstrap token empty")
	}

	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return fmt.Errorf("read host info: %w", err)
	}
	machineID := strings.TrimSpace(readFile(machineIDPath))

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
		return fmt.Errorf("register: %w", err)
	}
	if resp.AgentKey == "" {
		return errors.New("server returned empty agent_key")
	}
	if err := writeKeyFile(a.Cfg.KeyFile, resp.AgentID+keyFilePartsSep+resp.AgentKey); err != nil {
		return fmt.Errorf("persist key file: %w", err)
	}
	a.agentKey = resp.AgentKey
	loopLogger.Info("bootstrap successful", "host_id", resp.AgentID, "key_file", a.Cfg.KeyFile)
	return nil
}

// loadKey reads the persisted agent_id:agent_key from disk and stores the key
// portion on the receiver. Returns a wrapped error so callers can distinguish
// missing vs malformed.
func (a *Agent) loadKey() error {
	b, err := os.ReadFile(a.Cfg.KeyFile) //nolint:gosec // path from agent config / fixed-by-design
	if err != nil {
		return err
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), keyFilePartsSep, keyFileParts)
	if len(parts) != keyFileParts || parts[1] == "" {
		return errors.New("malformed key file (expected agent_id:agent_key)")
	}
	a.agentKey = parts[1]
	return nil
}

// Run drives the main agent loop: load the persisted key, optionally pull
// server-managed config, then on each tick collect + push (or spool on
// failure). The function returns when ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	if err := a.loadKey(); err != nil {
		return fmt.Errorf("agent key not present (%w); run with --bootstrap-token first", err)
	}

	// Try to pull server-managed config once at startup. On failure
	// (server older than agent or unreachable) fall back to the local
	// file's values silently — the bootstrap-only flow still works.
	a.currentInterval = a.Cfg.Interval()
	a.refreshRemoteConfig(ctx)

	cfgRefresh := time.NewTicker(remoteConfigRefreshInterval)
	defer cfgRefresh.Stop()

	t := time.NewTicker(a.currentInterval)
	defer t.Stop()

	// Always send an initial inventory at startup so the server knows us.
	if err := a.tick(ctx, true); err != nil {
		loopLogger.Warn("initial tick failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-cfgRefresh.C:
			if changed := a.refreshRemoteConfig(ctx); changed {
				t.Reset(a.currentInterval)
				loopLogger.Info("agent config reloaded; ticker reset", "interval", a.currentInterval)
			}
		case <-t.C:
			if a.inQuietHours(time.Now()) {
				continue
			}
			if err := a.tick(ctx, false); err != nil {
				loopLogger.Warn("tick failed", "err", err)
			}
		}
	}
}

// refreshRemoteConfig fetches the merged config from the server and applies
// it to the agent's runtime knobs. Returns true if any setting changed.
func (a *Agent) refreshRemoteConfig(ctx context.Context) bool {
	fetchCtx, cancel := context.WithTimeout(ctx, remoteConfigFetchTimeout)
	defer cancel()
	r, err := a.Client.FetchConfig(fetchCtx, a.agentKey)
	if err != nil {
		loopLogger.Warn("remote config fetch failed; keeping current settings", "err", err)
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
	if len(r.Config.Labels) > 0 {
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
	loopLogger.Info("remote config applied",
		"sources", strings.Join(r.SourceScopes, ","),
		"interval", a.currentInterval)
	return prev != a.currentInterval
}

// inQuietHours returns true when "now" falls inside the configured quiet
// window. Only the remote config carries quiet hours — yaml has no such
// field. Days are 0=Sun..6=Sat. The window may wrap midnight
// (e.g. 22:00 → 06:00).
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

// parseHHMM parses a "HH:MM" string into a minute-of-day count. Returns
// (0, false) when the input is malformed.
func parseHHMM(s string) (int, bool) {
	if len(s) < hhmmMinLen || len(s) > hhmmMaxLen {
		return 0, false
	}
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := atoiSafe(parts[0])
	m, err2 := atoiSafe(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > maxHour || m < 0 || m > maxMinute {
		return 0, false
	}
	return h*60 + m, true
}

// atoiSafe is a tiny strconv.Atoi-like helper that rejects any non-digit rune,
// including leading "+" or "-" signs. Keeps quiet-hour parsing strict.
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

// tick runs every active collector, then either pushes the batch directly or
// spools it for the next attempt. When withInventory is true an inventory
// snapshot is also collected and attached to the batch.
func (a *Agent) tick(ctx context.Context, withInventory bool) error {
	batch := apitypes.IngestRequest{SnapshotAt: time.Now().UTC()}

	if withInventory {
		snap := apitypes.InventorySnap{AgentVersion: version.Version, Labels: a.Cfg.Labels}
		for _, p := range a.Inventory {
			if err := p.Inventory(ctx, &snap); err != nil {
				loopLogger.Warn("inventory provider failed", "err", err)
			}
		}
		snap.Sources = a.activeSources()
		batch.Inventory = &snap
	}

	for _, c := range a.Collectors {
		if err := c.Collect(ctx, &batch); err != nil {
			loopLogger.Warn("collector failed", "name", c.Name(), "err", err)
		}
	}

	payload, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}

	// First try direct send. If it fails, spool to disk and try draining
	// the spool on the next tick.
	if err := a.Client.Ingest(ctx, a.agentKey, payload); err != nil {
		loopLogger.Warn("send failed; spooling", "err", err)
		return a.Spool.Append(batch)
	}
	// Send succeeded; flush any spooled batches.
	return a.Spool.Drain(func(raw []byte) error {
		return a.Client.Ingest(ctx, a.agentKey, raw)
	})
}

// activeSources returns the names of every registered collector. Used by the
// inventory snapshot so the server can render an "agent reports" capability
// matrix.
func (a *Agent) activeSources() []string {
	out := make([]string, 0, len(a.Collectors))
	for _, c := range a.Collectors {
		out = append(out, c.Name())
	}
	return out
}

// writeKeyFile atomically writes content to path with mode 0400 inside a 0700
// parent. The write goes via a .tmp sibling + rename so the destination is
// never observed half-written.
func writeKeyFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), keyFileDirMode); err != nil {
		return fmt.Errorf("mkdir key dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content+"\n"), keyFileMode); err != nil {
		return fmt.Errorf("write key tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename key file: %w", err)
	}
	return nil
}

// readFile reads p and returns its contents as a string, or "" on any error.
// Used for best-effort reads of fixed-path Linux files like /etc/machine-id.
func readFile(p string) string {
	b, err := os.ReadFile(p) //nolint:gosec // path from agent config / fixed-by-design
	if err != nil {
		return ""
	}
	return string(b)
}
