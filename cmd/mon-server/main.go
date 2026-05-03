package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pr0ph37/mon/internal/server/api"
	"github.com/pr0ph37/mon/internal/server/store"
	"github.com/pr0ph37/mon/internal/shared/version"
)

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		dumpOpenAPI = flag.String("dump-openapi", "", "write OpenAPI spec to this path (YAML by extension or JSON) and exit")
	)
	flag.Parse()

	if *showVersion {
		_, _ = os.Stdout.WriteString(version.String() + "\n")
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	addr := envOr("MON_LISTEN_ADDR", ":8080")
	dsn := envOr("MON_DSN", "")
	tlsCert := os.Getenv("MON_TLS_CERT")
	tlsKey := os.Getenv("MON_TLS_KEY")

	// --- OpenAPI dump shortcut: skip DB; build a stub server with nil store. ---
	if *dumpOpenAPI != "" {
		s := api.New(nil)
		if err := writeOpenAPI(s, *dumpOpenAPI); err != nil {
			slog.Error("dump openapi", "err", err)
			os.Exit(1)
		}
		slog.Info("openapi spec written", "path", *dumpOpenAPI)
		return
	}

	if dsn == "" {
		slog.Error("MON_DSN required (or use --dump-openapi)")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	st, err := store.Open(openCtx, dsn)
	if err != nil {
		slog.Error("db open", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := st.MigrateUp(openCtx); err != nil {
		slog.Error("migrations", "err", err)
		os.Exit(1)
	}

	s := api.New(st)

	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		var serveErr error
		if tlsCert != "" && tlsKey != "" {
			srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			slog.Info("mon-server starting (TLS)", "addr", addr, "version", version.String())
			serveErr = srv.ListenAndServeTLS(tlsCert, tlsKey)
		} else {
			slog.Info("mon-server starting (plain HTTP)", "addr", addr, "version", version.String())
			serveErr = srv.ListenAndServe()
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Error("listen failed", "err", serveErr)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("mon-server shutting down")

	shutdownCtx, c := context.WithTimeout(context.Background(), 15*time.Second)
	defer c()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "err", err)
		os.Exit(1)
	}
}

func writeOpenAPI(s *api.Server, path string) error {
	switch ext := suffix(path); ext {
	case ".yaml", ".yml":
		spec, err := s.API.OpenAPI().YAML()
		if err != nil {
			return err
		}
		return os.WriteFile(path, spec, 0o644)
	case ".json":
		spec, err := json.MarshalIndent(s.API.OpenAPI(), "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(path, spec, 0o644)
	default:
		return errors.New("dump-openapi path must end in .yaml, .yml, or .json")
	}
}

func suffix(p string) string {
	for i := len(p) - 1; i >= 0 && p[i] != '/'; i-- {
		if p[i] == '.' {
			return p[i:]
		}
	}
	return ""
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
