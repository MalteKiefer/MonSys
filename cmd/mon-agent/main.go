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
		// Best-effort wipe of the in-memory token.
		token = ""
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
