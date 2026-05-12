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

	"github.com/MalteKiefer/MonSys/internal/server/agentupdate"
	"github.com/MalteKiefer/MonSys/internal/server/alerts"
	"github.com/MalteKiefer/MonSys/internal/server/api"
	"github.com/MalteKiefer/MonSys/internal/server/housekeeping"
	"github.com/MalteKiefer/MonSys/internal/server/ingestlog"
	"github.com/MalteKiefer/MonSys/internal/server/liveness"
	"github.com/MalteKiefer/MonSys/internal/server/probe"
	"github.com/MalteKiefer/MonSys/internal/server/serverlog"
	"github.com/MalteKiefer/MonSys/internal/server/store"
	"github.com/MalteKiefer/MonSys/internal/server/webauthn"
	"github.com/MalteKiefer/MonSys/internal/shared/version"
)

func main() {
	var (
		showVersion       = flag.Bool("version", false, "print version and exit")
		printSpec         = flag.Bool("print-spec", false, "print the OpenAPI spec (YAML) to stdout and exit")
		dumpOpenAPI       = flag.String("dump-openapi", "", "write OpenAPI spec to this path (YAML by extension or JSON) and exit")
		newToken          = flag.Bool("new-token", false, "issue a new bootstrap token, print it, and exit")
		newTokenDesc      = flag.String("token-description", "", "description for the new bootstrap token")
		newTokenTTLString = flag.String("token-ttl", "24h", "lifetime of the new bootstrap token")
		createUser        = flag.Bool("create-user", false, "create a web user (interactive password unless --password is given) and exit")
		createUserEmail   = flag.String("user-email", "", "email for --create-user")
		createUserRole    = flag.String("user-role", "user", "role for --create-user (admin|user)")
		createUserPassword = flag.String("user-password", "", "password for --create-user; if empty, read from stdin")
		resetPassword      = flag.Bool("reset-password", false, "reset a user's password (use --user-email + --user-password) and exit")

		// CLI recovery flags for 2FA / passkeys / security policy. All are
		// admin shell-level recovery: they bypass current-password / current-
		// factor checks because the operator already has docker-exec access.
		disableTOTP       = flag.Bool("disable-totp", false, "wipe a user's TOTP enrollment (use --user-email) and exit")
		listPasskeys      = flag.Bool("list-passkeys", false, "list a user's registered passkeys (use --user-email) and exit")
		deleteAllPasskeys = flag.Bool("delete-all-passkeys", false, "wipe all of a user's passkeys (use --user-email) and exit")
		getSecPolicy      = flag.Bool("get-security-policy", false, "print the global security policy as JSON and exit")
		setSecPolicy      = flag.Bool("set-security-policy", false, "update the global security policy; only fields whose flags are explicitly set are changed; then exit")
		secForceMode      = flag.String("force-mode", "", "for --set-security-policy: off|2fa_any|passkey_required")
		secGraceDays      = flag.Int("grace-days", -1, "for --set-security-policy: 0..365 (-1 = no change)")
		secMaxSessionHrs  = flag.Int("max-session-hours", -1, "for --set-security-policy: 1..8760 (-1 = no change)")
		secIdleMinutes    = flag.Int("idle-timeout-minutes", -1, "for --set-security-policy: 0..10080 (-1 = no change; 0 = disabled)")
		revokeAllSess     = flag.Bool("revoke-all-sessions", false, "revoke every active web session and exit")
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
	if *printSpec {
		s := api.New(nil)
		spec, err := s.API.OpenAPI().YAML()
		if err != nil {
			slog.Error("print openapi", "err", err)
			os.Exit(1)
		}
		if _, err := os.Stdout.Write(spec); err != nil {
			slog.Error("print openapi", "err", err)
			os.Exit(1)
		}
		return
	}
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

	if *resetPassword {
		if *createUserEmail == "" {
			slog.Error("--reset-password requires --user-email")
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
		if err := st.SetPassword(openCtx, *createUserEmail, pw); err != nil {
			slog.Error("reset password", "err", err)
			os.Exit(1)
		}
		slog.Info("password reset", "email", *createUserEmail)
		return
	}

	if *disableTOTP {
		if *createUserEmail == "" {
			slog.Error("--disable-totp requires --user-email")
			os.Exit(2)
		}
		u, err := st.GetUserByEmail(openCtx, *createUserEmail)
		if err != nil {
			slog.Error("lookup user", "email", *createUserEmail, "err", err)
			os.Exit(1)
		}
		if err := st.DisableTOTP(openCtx, u.ID); err != nil {
			slog.Error("disable totp", "err", err)
			os.Exit(1)
		}
		slog.Info("totp disabled", "email", u.Email, "id", u.ID.String())
		return
	}

	if *listPasskeys {
		if *createUserEmail == "" {
			slog.Error("--list-passkeys requires --user-email")
			os.Exit(2)
		}
		u, err := st.GetUserByEmail(openCtx, *createUserEmail)
		if err != nil {
			slog.Error("lookup user", "email", *createUserEmail, "err", err)
			os.Exit(1)
		}
		pks, err := st.ListPasskeys(openCtx, u.ID)
		if err != nil {
			slog.Error("list passkeys", "err", err)
			os.Exit(1)
		}
		if len(pks) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "no passkeys registered")
			return
		}
		for _, p := range pks {
			last := "never"
			if p.LastUsedAt != nil {
				last = p.LastUsedAt.Format(time.RFC3339)
			}
			_, _ = fmt.Fprintf(os.Stdout, "%s  %-32s  aaguid=%s  last_used=%s  created=%s\n",
				p.ID, truncate(p.Name, 32), defaultStr(p.AAGUID, "-"), last, p.CreatedAt.Format(time.RFC3339))
		}
		return
	}

	if *deleteAllPasskeys {
		if *createUserEmail == "" {
			slog.Error("--delete-all-passkeys requires --user-email")
			os.Exit(2)
		}
		u, err := st.GetUserByEmail(openCtx, *createUserEmail)
		if err != nil {
			slog.Error("lookup user", "email", *createUserEmail, "err", err)
			os.Exit(1)
		}
		n, err := st.DeleteAllPasskeysForUser(openCtx, u.ID)
		if err != nil {
			slog.Error("delete passkeys", "err", err)
			os.Exit(1)
		}
		slog.Info("passkeys deleted", "email", u.Email, "count", n)
		return
	}

	if *getSecPolicy {
		p, err := st.GetSecurityPolicy(openCtx)
		if err != nil {
			slog.Error("get security policy", "err", err)
			os.Exit(1)
		}
		raw, _ := json.MarshalIndent(p, "", "  ")
		_, _ = os.Stdout.Write(raw)
		_, _ = os.Stdout.WriteString("\n")
		return
	}

	if *setSecPolicy {
		cur, err := st.GetSecurityPolicy(openCtx)
		if err != nil {
			slog.Error("get current security policy", "err", err)
			os.Exit(1)
		}
		// Track which CLI flags the operator explicitly passed so we only
		// overwrite those fields. The sentinels (-1 for ints, "" for strings)
		// distinguish "not provided" from a valid zero value (idle=0 means
		// disabled, which IS valid input).
		set := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { set[f.Name] = true })

		next := cur
		if set["force-mode"] {
			next.ForceMode = *secForceMode
		}
		if set["grace-days"] {
			next.GraceDays = *secGraceDays
		}
		if set["max-session-hours"] {
			next.MaxSessionHours = *secMaxSessionHrs
		}
		if set["idle-timeout-minutes"] {
			next.IdleTimeoutMinutes = *secIdleMinutes
		}
		if next == cur {
			slog.Error("--set-security-policy needs at least one of --force-mode, --grace-days, --max-session-hours, --idle-timeout-minutes")
			os.Exit(2)
		}
		if err := st.SetSecurityPolicy(openCtx, next, "cli"); err != nil {
			slog.Error("set security policy", "err", err)
			os.Exit(1)
		}
		raw, _ := json.MarshalIndent(next, "", "  ")
		_, _ = os.Stdout.Write(raw)
		_, _ = os.Stdout.WriteString("\n")
		slog.Info("security policy updated")
		return
	}

	if *revokeAllSess {
		n, err := st.RevokeAllSessions(openCtx, "")
		if err != nil {
			slog.Error("revoke all sessions", "err", err)
			os.Exit(1)
		}
		slog.Info("sessions revoked", "count", n)
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

	// WebAuthn relying-party service. Configured from env so operators can
	// run multiple deployments without rebuilding. RPID must be the bare
	// hostname; RPOrigin the full origin including scheme (and port for dev).
	rpID := os.Getenv("MON_RP_ID")
	if rpID == "" {
		rpID = "localhost"
	}
	rpOrigin := os.Getenv("MON_RP_ORIGIN")
	if rpOrigin == "" {
		rpOrigin = "http://localhost:5173"
	}
	wa, werr := webauthn.New(webauthn.Config{
		RPID:    rpID,
		RPName:  "MonSys",
		Origins: []string{rpOrigin},
	})
	if werr != nil {
		slog.Error("webauthn config invalid", "rp_id", rpID, "origin", rpOrigin, "err", werr)
		os.Exit(1)
	}
	st.Webauthn = wa

	s := api.New(st)
	s.LogBuffer = logBuf
	s.IngestBuffer = ingestlog.New(envInt("MON_INGEST_BUFFER", 100), envInt("MON_INGEST_MAX_BYTES", 1<<20))
	s.Alerts = eng
	s.AgentUpdate = agentupdate.NewFromEnv()

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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
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
