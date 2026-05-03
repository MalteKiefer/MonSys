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

	"github.com/pr0ph37/mon/internal/agent/buffer"
	"github.com/pr0ph37/mon/internal/agent/collector"
	"github.com/pr0ph37/mon/internal/agent/collector/workload"
	"github.com/pr0ph37/mon/internal/agent/config"
	"github.com/pr0ph37/mon/internal/agent/transport"
	"github.com/pr0ph37/mon/internal/shared/apitypes"
	"github.com/pr0ph37/mon/internal/shared/version"

	"github.com/shirou/gopsutil/v4/host"
)

type Agent struct {
	Cfg        config.Config
	Client     *transport.Client
	Spool      *buffer.Spool
	Collectors []collector.Source
	Inventory  []collector.InventoryProvider

	agentKey string
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
	b, err := os.ReadFile(a.Cfg.KeyFile)
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

	t := time.NewTicker(a.Cfg.Interval())
	defer t.Stop()

	// Always send an initial inventory at startup so the server knows us.
	if err := a.tick(ctx, true); err != nil {
		slog.Warn("initial tick failed", "err", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := a.tick(ctx, false); err != nil {
				slog.Warn("tick failed", "err", err)
			}
		}
	}
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
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}
