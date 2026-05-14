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
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MalteKiefer/MonSys/internal/agent"
	"github.com/MalteKiefer/MonSys/internal/agent/config"
	"github.com/MalteKiefer/MonSys/internal/agent/heal"
	"github.com/MalteKiefer/MonSys/internal/agent/transport"
	"github.com/MalteKiefer/MonSys/internal/agent/updater"
	"github.com/MalteKiefer/MonSys/internal/shared/version"
)

// lifecycleTimeout caps the total time the agent will spend talking to the
// server when running --deactivate or --delete. The default loop never sets
// a deadline on these calls because it runs forever; the one-shot lifecycle
// flags must bound them.
const lifecycleTimeout = 30 * time.Second

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		configPath  = flag.String("config", "/etc/mon-agent/config.yaml", "path to config file")
		bootstrap   = flag.String("bootstrap-token", "", "one-time token for first registration; takes precedence over MON_BOOTSTRAP_TOKEN")
		selfUpdate  = flag.Bool("self-update", false, "fetch latest agent binary, sha256-verify, atomic-replace, then trigger systemctl restart and exit")
		binaryPath  = flag.String("self-update-binary", "/usr/local/bin/mon-agent", "destination path for --self-update")
		stagingDir  = flag.String("self-update-staging", "/var/lib/mon-agent", "writable staging directory for --self-update")
		deactivate  = flag.Bool("deactivate", false, "revoke this host's agent key on the server (host row + history kept; agent must be re-enrolled to reconnect), then exit")
		deleteFlag  = flag.Bool("delete", false, "permanently delete this host record from the server (irreversible: drops inventory and metrics), then exit")
		assumeYes   = flag.Bool("yes", false, "skip the interactive confirmation prompt used by --delete and --deactivate")
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

	if *deactivate && *deleteFlag {
		slog.Error("--deactivate and --delete are mutually exclusive")
		os.Exit(2)
	}
	if *deactivate || *deleteFlag {
		if err := runLifecycle(cfg, *deleteFlag, *assumeYes); err != nil {
			slog.Error("lifecycle command failed", "delete", *deleteFlag, "err", err)
			os.Exit(1)
		}
		return
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
			stop()
			os.Exit(1) //nolint:gocritic // stop() invoked manually above; defer stop() is a no-op fallback for the success path
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

// runLifecycle wires --deactivate / --delete: read the persisted agent_key,
// confirm with the operator (unless --yes), call the server, then on
// success remove the local key file so the next start is a clean
// bootstrap.
//
// Failure modes:
//   - missing key file → exit 1 with a clear message; the host was never
//     enrolled or was already torn down.
//   - server returns 401 (key already revoked) → treated as success since
//     the user's intent has been satisfied.
//   - any other server error → propagated; key file is *not* removed.
func runLifecycle(cfg config.Config, deleteHost, assumeYes bool) error {
	key, err := readKeyFile(cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("read key file %s: %w", cfg.KeyFile, err)
	}
	action := "deactivate"
	if deleteHost {
		action = "delete"
	}
	if !assumeYes {
		if !confirm(action, deleteHost, cfg.ServerURL) {
			slog.Info("lifecycle aborted by operator", "action", action)
			return nil
		}
	}

	c, err := transport.New(cfg.ServerURL,
		transport.WithCAFile(cfg.TLS.CAFile),
		transport.WithPin(cfg.TLS.CAPinSHA256),
	)
	if err != nil {
		return fmt.Errorf("transport init: %w", err)
	}
	if cfg.TLS.Insecure {
		_ = transport.WithInsecureSkipVerify()(c) // dev-only escape hatch, mirrors agent.New
	}

	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTimeout)
	defer cancel()

	if deleteHost {
		err = c.Delete(ctx, key)
	} else {
		err = c.Deactivate(ctx, key)
	}
	if err != nil {
		// 401 means the key is already revoked or the host already
		// deleted server-side; user intent is satisfied so we still
		// clean up the local file. Any other error is fatal.
		if !strings.Contains(err.Error(), " 401 ") {
			return err
		}
		slog.Warn("server reports key already revoked/host already removed; treating as success", "err", err)
	}

	if rmErr := os.Remove(cfg.KeyFile); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		// Don't fail the command for cleanup errors — the server-side
		// action already succeeded — but the operator must know they
		// have to remove the stale key file by hand.
		slog.Warn("removed host on server but failed to delete local key file",
			"key_file", cfg.KeyFile, "err", rmErr)
	}
	slog.Info("lifecycle command succeeded", "action", action, "server", cfg.ServerURL)
	return nil
}

// readKeyFile parses the on-disk "agent_id:agent_key" payload and returns
// the secret half. Mirrors the agent runtime's parsing rules.
func readKeyFile(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path from agent config / fixed-by-design
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(strings.TrimSpace(string(b)), ":", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", errors.New("malformed key file (expected agent_id:agent_key)")
	}
	return parts[1], nil
}

// confirm prints a destructive-action prompt and returns true only when the
// operator types the exact word "yes". stdin not being a TTY (cron, service
// hooks) gives an automatic "no" so accidental piped invocations don't
// wipe a host.
func confirm(action string, deleteHost bool, server string) bool {
	scope := "agent key for this host"
	if deleteHost {
		scope = "this host record and all of its inventory + metrics"
	}
	fmt.Fprintf(os.Stderr,
		"About to %s on %s. Target: %s. This action is logged.\nType 'yes' to confirm: ",
		action, server, scope)
	var resp string
	if _, err := fmt.Fscanln(os.Stdin, &resp); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(resp), "yes")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
