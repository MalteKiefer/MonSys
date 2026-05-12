//go:build linux

// Command mon-agent is the host-side agent binary: it gathers system metrics,
// inventory, and security posture on a Linux host and ships them to the
// mon-server.
//
// The agent is Linux-only today — collectors shell to apt/dnf/pacman/apk,
// libvirt/lxc/qm/pct, ufw/nft/iptables/pve-firewall/fail2ban/cscli, journalctl,
// and read /proc, /sys, /etc/machine-id. A Windows port is tracked
// separately; every collector package and the agent runtime already carry
// //go:build linux so a sibling Windows tree can sit next to them without
// colliding.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/MalteKiefer/MonSys/internal/agent"
	"github.com/MalteKiefer/MonSys/internal/agent/config"
	"github.com/MalteKiefer/MonSys/internal/agent/heal"
	"github.com/MalteKiefer/MonSys/internal/agent/updater"
	"github.com/MalteKiefer/MonSys/internal/shared/version"
)

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		configPath  = flag.String("config", "/etc/mon-agent/config.yaml", "path to config file")
		bootstrap   = flag.String("bootstrap-token", "", "one-time token for first registration; takes precedence over MON_BOOTSTRAP_TOKEN")
		selfUpdate  = flag.Bool("self-update", false, "fetch latest agent binary, sha256-verify, atomic-replace, then trigger systemctl restart and exit")
		binaryPath  = flag.String("self-update-binary", "/usr/local/bin/mon-agent", "destination path for --self-update")
		stagingDir  = flag.String("self-update-staging", "/var/lib/mon-agent", "writable staging directory for --self-update")
	)
	flag.Parse()

	if *showVersion {
		_, _ = os.Stdout.WriteString(version.String() + "\n")
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(2)
	}

	if *selfUpdate {
		if !cfg.AutoUpdateEnabled() {
			slog.Info("self-update: disabled in config (auto_update.enabled=false); exiting")
			return
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		res, err := updater.Run(ctx, updater.Options{
			ServerURL:      cfg.ServerURL,
			CurrentVersion: version.Version,
			BinaryPath:     *binaryPath,
			StagingDir:     *stagingDir,
			RestartCmd:     []string{"systemctl", "try-restart", "mon-agent.service"},
		})
		if err != nil {
			slog.Error("self-update failed", "err", err, "from", res.From, "to", res.To)
			os.Exit(1)
		}
		if !res.Replaced {
			slog.Info("self-update: already current", "version", res.From)
			return
		}
		slog.Info("self-update: replaced binary", "from", res.From, "to", res.To, "path", res.BinaryPath)
		return
	}

	if err := heal.Verify(cfg); err != nil {
		slog.Error("self-heal failed", "err", err)
		os.Exit(2)
	}

	a, err := agent.New(cfg)
	if err != nil {
		slog.Error("agent init", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	token := firstNonEmpty(*bootstrap, os.Getenv("MON_BOOTSTRAP_TOKEN"))
	if token != "" {
		if err := a.Bootstrap(ctx, token); err != nil {
			slog.Error("bootstrap failed", "err", err)
			os.Exit(1)
		}
		// Best-effort wipe of the env reference; the local `token` string is
		// immutable so we can't truly zero it, only stop referencing it.
		_ = os.Unsetenv("MON_BOOTSTRAP_TOKEN")
	}

	slog.Info("mon-agent running", "version", version.String(), "server", cfg.ServerURL, "interval", cfg.Interval())

	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
