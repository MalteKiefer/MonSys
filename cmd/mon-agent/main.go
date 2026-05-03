package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pr0ph37/mon/internal/shared/version"
)

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		configPath  = flag.String("config", "/etc/mon-agent/config.yaml", "path to config file")
	)
	flag.Parse()

	if *showVersion {
		os.Stdout.WriteString(version.String() + "\n")
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	slog.Info("mon-agent starting", "version", version.String(), "config", *configPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	t := time.NewTicker(15 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("mon-agent shutting down")
			return
		case <-t.C:
			slog.Info("tick: collectors not implemented yet (M2)")
		}
	}
}
