package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MalteKiefer/MonSys/internal/server/alerts"
	"github.com/MalteKiefer/MonSys/internal/server/api"
	"github.com/MalteKiefer/MonSys/internal/server/housekeeping"
	"github.com/MalteKiefer/MonSys/internal/server/ingestlog"
	"github.com/MalteKiefer/MonSys/internal/server/liveness"
	"github.com/MalteKiefer/MonSys/internal/server/probe"
	"github.com/MalteKiefer/MonSys/internal/server/serverlog"
	"github.com/MalteKiefer/MonSys/internal/server/store"
	"github.com/MalteKiefer/MonSys/internal/shared/version"
)

func main() {
	var (
		showVersion       = flag.Bool("version", false, "print version and exit")
		dumpOpenAPI       = flag.String("dump-openapi", "", "write OpenAPI spec to this path (YAML by extension or JSON) and exit")
		newToken          = flag.Bool("new-token", false, "issue a new bootstrap token, print it, and exit")
		newTokenDesc      = flag.String("token-description", "", "description for the new bootstrap token")
		newTokenTTLString = flag.String("token-ttl", "24h", "lifetime of the new bootstrap token")
		createUser        = flag.Bool("create-user", false, "create a web user (interactive password unless --password is given) and exit")
		createUserEmail   = flag.String("user-email", "", "email for --create-user")
		createUserRole    = flag.String("user-role", "user", "role for --create-user (admin|user)")
		createUserPassword = flag.String("user-password", "", "password for --create-user; if empty, read from stdin")
	)
	flag.Parse()

	if *showVersion {
		_, _ = os.Stdout.WriteString(version.String() + "\n")
		return
	}

	// Tee slog output: (1) JSON to stdout for container/journal capture,
	// (2) ring buffer in process for the admin /v1/admin/logs endpoint.
	logBuf := serverlog.NewBuffer(envInt("MON_LOG_BUFFER", 5000))
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(serverlog.NewHandler(jsonHandler, logBuf))
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

	if *newToken {
		ttl, err := time.ParseDuration(*newTokenTTLString)
		if err != nil {
			slog.Error("invalid --token-ttl", "value", *newTokenTTLString, "err", err)
			os.Exit(2)
		}
		plaintext, err := st.CreateBootstrapToken(openCtx, *newTokenDesc, ttl, "cli")
		if err != nil {
			slog.Error("create token", "err", err)
			os.Exit(1)
		}
		_, _ = os.Stdout.WriteString(plaintext + "\n")
		return
	}

	if *createUser {
		if *createUserEmail == "" {
			slog.Error("--create-user requires --user-email")
			os.Exit(2)
		}
		pw := *createUserPassword
		if pw == "" {
			line, err := readLine(os.Stdin)
			if err != nil || line == "" {
				slog.Error("password required (pass --user-password or pipe via stdin)")
				os.Exit(2)
			}
			pw = line
		}
		u, err := st.CreateUser(openCtx, *createUserEmail, pw, *createUserRole)
		if err != nil {
			slog.Error("create user", "err", err)
			os.Exit(1)
		}
		slog.Info("user created", "id", u.ID.String(), "email", u.Email, "role", u.Role)
		return
	}

	// Background liveness watcher: every 30s, derives host_status from
	// last_seen_at. Channel forwards transitions to the alerts engine.
	lw := liveness.New(st.Pool)
	go lw.Run(ctx)

	// Active monitors scheduler.
	sched := probe.NewScheduler(st.Pool)
	go sched.Run(ctx)

	// Alerts engine: subscribes to liveness + monitor events and runs
	// stateful checks (failed-login threshold, security updates) on a
	// 60s tick.
	eng := alerts.New(st.Pool, lw.Out, sched.Out)
	go eng.Run(ctx)

	// Housekeeping reaper: bounds tables that don't have a Timescale
	// retention policy (user_sessions, user_action_tokens) and the
	// in-memory failed-login tracker.
	reaper := housekeeping.New(st.Pool, st.FailedLoginsTracker())
	go reaper.Run(ctx)

	s := api.New(st)
	s.LogBuffer = logBuf
	s.IngestBuffer = ingestlog.New(envInt("MON_INGEST_BUFFER", 100), envInt("MON_INGEST_MAX_BYTES", 1<<20))
	s.Alerts = eng

	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// AUDIT-011: 1 MiB cap on request headers. Bodies are bounded
		// separately by the api.New() body-size middleware.
		MaxHeaderBytes: 1 << 20,
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

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err == nil && n > 0 {
			return n
		}
	}
	return def
}

func readLine(r *os.File) (string, error) {
	buf := make([]byte, 1024)
	var out []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				if buf[i] == '\n' {
					out = append(out, buf[:i]...)
					return string(out), nil
				}
			}
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if len(out) > 0 {
				return string(out), nil
			}
			return "", err
		}
	}
}
