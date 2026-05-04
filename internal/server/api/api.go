package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/google/uuid"

	"github.com/pr0ph37/mon/internal/server/alerts"
	"github.com/pr0ph37/mon/internal/server/ingestlog"
	"github.com/pr0ph37/mon/internal/server/notify"
	"github.com/pr0ph37/mon/internal/server/serverlog"
	"github.com/pr0ph37/mon/internal/server/spa"
	"github.com/pr0ph37/mon/internal/server/store"
	"github.com/pr0ph37/mon/internal/shared/apitypes"
	"github.com/pr0ph37/mon/internal/shared/version"
)

// Request body size caps. AUDIT-011: every endpoint must refuse oversized
// payloads to keep a hostile client from exhausting memory.
const (
	defaultMaxBodyBytes = 1 << 20  // 1 MiB for everything except ingest
	ingestMaxBodyBytes  = 32 << 20 // 32 MiB for /v1/ingest (large dpkg dumps)
)

type Server struct {
	Store        *store.Store
	Router       chi.Router
	API          huma.API
	LogBuffer    *serverlog.Buffer
	IngestBuffer *ingestlog.Buffer
	// Alerts is optional; main wires it so the admin quiet-hours handler can
	// invalidate the engine's cached config immediately rather than waiting
	// for the 60 s TTL to expire.
	Alerts *alerts.Engine
}

func New(s *store.Store) *Server {
	r := chi.NewRouter()
	srv := &Server{Store: s, Router: r}

	// chi requires every Use() call before any route registration.
	// humachi.New below registers the openapi/docs routes, so all
	// middleware must be installed first — including the docs gate that
	// closes over srv.
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	// AUDIT-011: clamp request body sizes immediately after Recoverer so the
	// rest of the pipeline never sees oversized inputs.
	r.Use(bodySizeLimiter)
	// AUDIT-015 / AUDIT-048: security response headers applied globally.
	r.Use(securityHeaders)
	// AUDIT-012, 016, 070, 071: per-IP rate limits on auth-adjacent and
	// ingest endpoints. Cheap routes like /healthz and /readyz are skipped
	// inside the middleware itself.
	r.Use(rateLimitByPath())
	// AUDIT-066: openapi/docs are session-protected.
	r.Use(srv.requireSessionForDocs)
	r.Use(middleware.Timeout(30 * time.Second))

	cfg := huma.DefaultConfig("mon", version.Version)
	cfg.Info.Description = "Self-hosted server-monitoring API. Agents push metrics; users query."
	cfg.Servers = []*huma.Server{{URL: "/", Description: "current"}}
	srv.API = humachi.New(r, cfg)

	srv.registerRoutes()
	return srv
}

// bodySizeLimiter wraps r.Body in an http.MaxBytesReader so handlers and
// JSON decoders enforce a hard cap. /v1/ingest gets a higher cap to fit
// large package inventories; everything else uses the conservative default.
// AUDIT-011.
func bodySizeLimiter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			limit := int64(defaultMaxBodyBytes)
			if r.URL.Path == "/v1/ingest" {
				limit = int64(ingestMaxBodyBytes)
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets a hardened response-header set on every reply. The
// CSP is locked down to self plus inline styles (Vite-built React requires
// inline styles for code-split chunks); no script-src 'unsafe-inline'.
// AUDIT-015 / AUDIT-048.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; font-src 'self'; "+
				"script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// rateLimitByPath returns a chi middleware that applies one of two per-IP
// httprate limiters based on the request path. /healthz, /readyz and the
// SPA assets are exempt — those should never be rate-limited.
//
// Limits chosen for the audit:
//   - login / 2fa-challenge / consume-reset / agents-register: 20 req/min
//   - /v1/ingest: 600 req/min (10/s) — agents on noisy hosts spike briefly
//
// AUDIT-012, 016, 070, 071.
func rateLimitByPath() func(http.Handler) http.Handler {
	authLimiter := httprate.LimitByIP(20, time.Minute)
	ingestLimiter := httprate.LimitByIP(600, time.Minute)

	authPaths := map[string]struct{}{
		"/v1/auth/login":         {},
		"/v1/auth/2fa/challenge": {},
		"/v1/auth/consume-reset": {},
		"/v1/agents/register":    {},
	}

	return func(next http.Handler) http.Handler {
		auth := authLimiter(next)
		ingest := ingestLimiter(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			// Health probes must never be throttled.
			if path == "/healthz" || path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := authPaths[path]; ok {
				auth.ServeHTTP(w, r)
				return
			}
			if path == "/v1/ingest" {
				ingest.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireSessionForDocs hides /docs and /openapi.* behind a valid session
// token. Without it any unauthenticated caller could enumerate the full API
// surface from a public deployment. AUDIT-066.
func (s *Server) requireSessionForDocs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		gated := path == "/docs" ||
			strings.HasPrefix(path, "/docs/") ||
			strings.HasPrefix(path, "/openapi.")
		if !gated {
			next.ServeHTTP(w, r)
			return
		}
		if s.Store == nil {
			http.NotFound(w, r)
			return
		}
		tok, ok := bearer(r.Header.Get("Authorization"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		if _, err := s.Store.ValidateSession(r.Context(), tok); err != nil {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// internalErr is the helper for AUDIT-018 / AUDIT-065. It logs the raw
// error against an operation tag and returns a generic 500 to the client
// so internal details (SQL fragments, stack traces, etc.) never leak.
func internalErr(ctx context.Context, op string, err error) error {
	slog.ErrorContext(ctx, "internal error", "op", op, "err", err)
	return huma.Error500InternalServerError("internal error")
}

func (s *Server) Handler() http.Handler { return s.Router }

func (s *Server) registerRoutes() {
	s.Router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	s.Router.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.Store == nil {
			http.Error(w, "no store", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.Store.Pool.Ping(ctx); err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "agent-register",
		Method:      http.MethodPost,
		Path:        "/v1/agents/register",
		Summary:     "Register a new agent",
		Description: "Trade a one-time bootstrap token (Authorization: Bearer …) for a per-host agent_key.",
		Tags:        []string{"agents"},
	}, s.handleAgentRegister)

	huma.Register(s.API, huma.Operation{
		OperationID: "ingest",
		Method:      http.MethodPost,
		Path:        "/v1/ingest",
		Summary:     "Ingest metrics + inventory",
		Description: "Agents push samples here. Auth: Authorization: Bearer <agent_key>.",
		Tags:        []string{"ingest"},
	}, s.handleIngest)

	// Auth: login + me + logout. Login itself is unauthenticated.
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-login",
		Method:      http.MethodPost,
		Path:        "/v1/auth/login",
		Summary:     "Exchange email+password for a session token",
		Tags:        []string{"auth"},
	}, s.handleLogin)

	huma.Register(s.API, huma.Operation{
		OperationID: "auth-logout",
		Method:      http.MethodPost,
		Path:        "/v1/auth/logout",
		Summary:     "Revoke the current session",
		Tags:        []string{"auth"},
		Middlewares: huma.Middlewares{s.requireUser},
	}, s.handleLogout)

	huma.Register(s.API, huma.Operation{
		OperationID: "auth-me",
		Method:      http.MethodGet,
		Path:        "/v1/auth/me",
		Summary:     "Return the authenticated user",
		Tags:        []string{"auth"},
		Middlewares: huma.Middlewares{s.requireUser},
	}, s.handleMe)

	// Read APIs require a user session.
	protected := huma.Middlewares{s.requireUser}

	huma.Register(s.API, huma.Operation{
		OperationID: "list-hosts",
		Method:      http.MethodGet,
		Path:        "/v1/hosts",
		Summary:     "List all known hosts",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleListHosts)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-detail",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}",
		Summary:     "Single host with current inventory bundles",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleHostDetail)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-set-tags",
		Method:      http.MethodPut,
		Path:        "/v1/hosts/{id}/tags",
		Summary:     "Replace the host's tag set",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleSetHostTags)

	huma.Register(s.API, huma.Operation{
		OperationID: "list-tags",
		Method:      http.MethodGet,
		Path:        "/v1/tags",
		Summary:     "List all tags in use with host counts",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleListTags)

	// Host groups
	huma.Register(s.API, huma.Operation{
		OperationID: "list-groups",
		Method:      http.MethodGet,
		Path:        "/v1/groups",
		Summary:     "List host groups with member ids",
		Tags:        []string{"groups"},
		Middlewares: protected,
	}, s.handleListGroups)
	huma.Register(s.API, huma.Operation{
		OperationID: "create-group",
		Method:      http.MethodPost,
		Path:        "/v1/groups",
		Summary:     "Create a host group",
		Tags:        []string{"groups"},
		Middlewares: huma.Middlewares{s.requireUser, s.requireAdmin},
	}, s.handleCreateGroup)
	huma.Register(s.API, huma.Operation{
		OperationID: "update-group",
		Method:      http.MethodPut,
		Path:        "/v1/groups/{id}",
		Summary:     "Update a host group",
		Tags:        []string{"groups"},
		Middlewares: huma.Middlewares{s.requireUser, s.requireAdmin},
	}, s.handleUpdateGroup)
	huma.Register(s.API, huma.Operation{
		OperationID: "delete-group",
		Method:      http.MethodDelete,
		Path:        "/v1/groups/{id}",
		Summary:     "Delete a host group",
		Tags:        []string{"groups"},
		Middlewares: huma.Middlewares{s.requireUser, s.requireAdmin},
	}, s.handleDeleteGroup)
	huma.Register(s.API, huma.Operation{
		OperationID: "set-group-members",
		Method:      http.MethodPut,
		Path:        "/v1/groups/{id}/members",
		Summary:     "Replace group membership",
		Tags:        []string{"groups"},
		Middlewares: huma.Middlewares{s.requireUser, s.requireAdmin},
	}, s.handleSetGroupMembers)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-system-metrics",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/metrics/system",
		Summary:     "Time-range query of system metrics for a host",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleSystemMetrics)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-packages",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/packages",
		Summary:     "Installed packages for a host (paginated)",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleHostPackages)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-package-updates",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/packages/updates",
		Summary:     "Pending package updates for a host",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleHostPackageUpdates)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-disk-metrics",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/metrics/disk",
		Summary:     "Time-range disk samples for a host (optionally one disk)",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleDiskMetrics)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-net-metrics",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/metrics/net",
		Summary:     "Time-range network samples for a host (optionally one nic)",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleNetMetrics)

	huma.Register(s.API, huma.Operation{
		OperationID: "search-packages",
		Method:      http.MethodGet,
		Path:        "/v1/packages",
		Summary:     "Search installed packages across all hosts",
		Tags:        []string{"packages"},
		Middlewares: protected,
	}, s.handleSearchPackages)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-security",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/security",
		Summary:     "Latest firewall, fail2ban, and CrowdSec snapshot for a host",
		Tags:        []string{"security"},
		Middlewares: protected,
	}, s.handleHostSecurity)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-logins",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/logins",
		Summary:     "Recent login/auth events for a host",
		Tags:        []string{"security"},
		Middlewares: protected,
	}, s.handleHostLogins)

	// Notification channel CRUD
	huma.Register(s.API, huma.Operation{
		OperationID: "list-channels",
		Method:      http.MethodGet,
		Path:        "/v1/notifications/channels",
		Summary:     "List notification channels",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleListChannels)
	huma.Register(s.API, huma.Operation{
		OperationID: "create-channel",
		Method:      http.MethodPost,
		Path:        "/v1/notifications/channels",
		Summary:     "Create a notification channel",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleCreateChannel)
	huma.Register(s.API, huma.Operation{
		OperationID: "get-channel",
		Method:      http.MethodGet,
		Path:        "/v1/notifications/channels/{id}",
		Summary:     "Get a notification channel",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleGetChannel)
	huma.Register(s.API, huma.Operation{
		OperationID: "update-channel",
		Method:      http.MethodPut,
		Path:        "/v1/notifications/channels/{id}",
		Summary:     "Replace a notification channel",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleUpdateChannel)
	huma.Register(s.API, huma.Operation{
		OperationID: "delete-channel",
		Method:      http.MethodDelete,
		Path:        "/v1/notifications/channels/{id}",
		Summary:     "Delete a notification channel",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleDeleteChannel)
	huma.Register(s.API, huma.Operation{
		OperationID: "test-channel",
		Method:      http.MethodPost,
		Path:        "/v1/notifications/channels/{id}/test",
		Summary:     "Send a test message through a channel",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleTestChannel)

	// Active monitors
	huma.Register(s.API, huma.Operation{
		OperationID: "list-monitors",
		Method:      http.MethodGet,
		Path:        "/v1/monitors",
		Summary:     "List active monitors",
		Tags:        []string{"monitors"},
		Middlewares: protected,
	}, s.handleListMonitors)
	huma.Register(s.API, huma.Operation{
		OperationID: "create-monitor",
		Method:      http.MethodPost,
		Path:        "/v1/monitors",
		Summary:     "Create an active monitor",
		Tags:        []string{"monitors"},
		Middlewares: protected,
	}, s.handleCreateMonitor)
	huma.Register(s.API, huma.Operation{
		OperationID: "get-monitor",
		Method:      http.MethodGet,
		Path:        "/v1/monitors/{id}",
		Summary:     "Get a monitor",
		Tags:        []string{"monitors"},
		Middlewares: protected,
	}, s.handleGetMonitor)
	huma.Register(s.API, huma.Operation{
		OperationID: "update-monitor",
		Method:      http.MethodPut,
		Path:        "/v1/monitors/{id}",
		Summary:     "Replace a monitor",
		Tags:        []string{"monitors"},
		Middlewares: protected,
	}, s.handleUpdateMonitor)
	huma.Register(s.API, huma.Operation{
		OperationID: "delete-monitor",
		Method:      http.MethodDelete,
		Path:        "/v1/monitors/{id}",
		Summary:     "Delete a monitor",
		Tags:        []string{"monitors"},
		Middlewares: protected,
	}, s.handleDeleteMonitor)
	huma.Register(s.API, huma.Operation{
		OperationID: "monitor-results",
		Method:      http.MethodGet,
		Path:        "/v1/monitors/{id}/results",
		Summary:     "Recent results for a monitor",
		Tags:        []string{"monitors"},
		Middlewares: protected,
	}, s.handleMonitorResults)

	// Notification rules + alert history
	huma.Register(s.API, huma.Operation{
		OperationID: "list-rules",
		Method:      http.MethodGet,
		Path:        "/v1/notifications/rules",
		Summary:     "List notification rules",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleListRules)
	huma.Register(s.API, huma.Operation{
		OperationID: "create-rule",
		Method:      http.MethodPost,
		Path:        "/v1/notifications/rules",
		Summary:     "Create a notification rule",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleCreateRule)
	huma.Register(s.API, huma.Operation{
		OperationID: "update-rule",
		Method:      http.MethodPut,
		Path:        "/v1/notifications/rules/{id}",
		Summary:     "Replace a notification rule",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleUpdateRule)
	huma.Register(s.API, huma.Operation{
		OperationID: "delete-rule",
		Method:      http.MethodDelete,
		Path:        "/v1/notifications/rules/{id}",
		Summary:     "Delete a notification rule",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleDeleteRule)
	huma.Register(s.API, huma.Operation{
		OperationID: "alert-history",
		Method:      http.MethodGet,
		Path:        "/v1/notifications/alerts",
		Summary:     "Recent alert history",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleAlertHistory)

	// 2FA challenge after password login (unauthenticated; protected by the
	// short-lived challenge token).
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-2fa-challenge",
		Method:      http.MethodPost,
		Path:        "/v1/auth/2fa/challenge",
		Summary:     "Complete login with a TOTP code",
		Tags:        []string{"auth"},
	}, s.handleTOTPChallenge)

	// Self-service profile (require user)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-change-password",
		Method:      http.MethodPost,
		Path:        "/v1/auth/change-password",
		Summary:     "Change own password",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleChangePassword)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-change-email",
		Method:      http.MethodPost,
		Path:        "/v1/auth/change-email",
		Summary:     "Change own email",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleChangeEmail)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-2fa-setup",
		Method:      http.MethodPost,
		Path:        "/v1/auth/2fa/setup",
		Summary:     "Begin TOTP setup; returns secret + QR + backup codes",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleTOTPSetup)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-2fa-verify",
		Method:      http.MethodPost,
		Path:        "/v1/auth/2fa/verify",
		Summary:     "Verify a TOTP code; activates pending TOTP if first-time",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleTOTPVerify)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-2fa-disable",
		Method:      http.MethodPost,
		Path:        "/v1/auth/2fa/disable",
		Summary:     "Disable own TOTP (requires password)",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleTOTPDisable)

	// Public reset endpoint — consumed via the link emailed by an admin
	// invite. The token in the body is the only credential required.
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-consume-reset",
		Method:      http.MethodPost,
		Path:        "/v1/auth/consume-reset",
		Summary:     "Set a new password via an admin-issued reset/invite token",
		Tags:        []string{"auth"},
	}, s.handleConsumeReset)

	// Admin: user management
	adminOnly := huma.Middlewares{s.requireUser, s.requireAdmin}
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-list-users",
		Method:      http.MethodGet,
		Path:        "/v1/admin/users",
		Summary:     "List all users",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleListUsers)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-create-user",
		Method:      http.MethodPost,
		Path:        "/v1/admin/users",
		Summary:     "Create a user (optionally with an emailed invite)",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminCreateUser)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-delete-user",
		Method:      http.MethodDelete,
		Path:        "/v1/admin/users/{id}",
		Summary:     "Delete a user",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminDeleteUser)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-lock-user",
		Method:      http.MethodPost,
		Path:        "/v1/admin/users/{id}/lock",
		Summary:     "Lock (disable) a user",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminLockUser)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-unlock-user",
		Method:      http.MethodPost,
		Path:        "/v1/admin/users/{id}/unlock",
		Summary:     "Unlock a user",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminUnlockUser)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-reset-password",
		Method:      http.MethodPost,
		Path:        "/v1/admin/users/{id}/reset-password",
		Summary:     "Issue a password-reset token (and optionally email it)",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminResetPassword)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-reset-2fa",
		Method:      http.MethodPost,
		Path:        "/v1/admin/users/{id}/reset-2fa",
		Summary:     "Disable a user's TOTP (forces re-enrollment)",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminReset2FA)

	// Admin: outbound SMTP transport (singleton).
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-get-smtp",
		Method:      http.MethodGet,
		Path:        "/v1/admin/mail",
		Summary:     "Get the global SMTP settings (password redacted)",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminGetSmtp)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-put-smtp",
		Method:      http.MethodPut,
		Path:        "/v1/admin/mail",
		Summary:     "Replace the global SMTP settings",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminPutSmtp)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-test-smtp",
		Method:      http.MethodPost,
		Path:        "/v1/admin/mail/test",
		Summary:     "Send a test email through the configured SMTP transport",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminTestSmtp)

	// Admin: global quiet hours (singleton). When the configured window is
	// active, the alerts engine still records each alert in alert_history
	// but emits zero deliveries with a synthetic _quiet_hours marker.
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-get-quiet-hours",
		Method:      http.MethodGet,
		Path:        "/v1/admin/quiet-hours",
		Summary:     "Get the global quiet-hour window",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminGetQuietHours)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-put-quiet-hours",
		Method:      http.MethodPut,
		Path:        "/v1/admin/quiet-hours",
		Summary:     "Replace the global quiet-hour window",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminPutQuietHours)

	// Agent-config: agents fetch their resolved config (auth via agent_key,
	// not web user). Admin CRUD lives separately under /v1/admin/agent-config.
	huma.Register(s.API, huma.Operation{
		OperationID: "agent-config-fetch",
		Method:      http.MethodGet,
		Path:        "/v1/agent/config",
		Summary:     "Agent fetches its resolved config (auth: Bearer agent_key)",
		Tags:        []string{"agents"},
	}, s.handleAgentConfigFetch)

	huma.Register(s.API, huma.Operation{
		OperationID: "list-agent-configs",
		Method:      http.MethodGet,
		Path:        "/v1/admin/agent-config",
		Summary:     "List all agent config rows (global + group + host)",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleListAgentConfigs)
	huma.Register(s.API, huma.Operation{
		OperationID: "upsert-agent-config",
		Method:      http.MethodPut,
		Path:        "/v1/admin/agent-config",
		Summary:     "Create or replace an agent config row (keyed by scope+target)",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleUpsertAgentConfig)
	huma.Register(s.API, huma.Operation{
		OperationID: "delete-agent-config",
		Method:      http.MethodDelete,
		Path:        "/v1/admin/agent-config/{id}",
		Summary:     "Delete an agent config row",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleDeleteAgentConfig)
	huma.Register(s.API, huma.Operation{
		OperationID: "preview-agent-config",
		Method:      http.MethodGet,
		Path:        "/v1/admin/agent-config/preview/{host_id}",
		Summary:     "Resolve and preview the merged config a host would receive",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handlePreviewAgentConfig)

	// Admin: server logs
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-server-logs",
		Method:      http.MethodGet,
		Path:        "/v1/admin/logs",
		Summary:     "Read recent server log entries from the in-memory ring",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminServerLogs)

	// Admin: raw agent ingests
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-list-ingests",
		Method:      http.MethodGet,
		Path:        "/v1/admin/ingests",
		Summary:     "List recently captured agent ingest payloads",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminListIngests)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-get-ingest",
		Method:      http.MethodGet,
		Path:        "/v1/admin/ingests/{idx}",
		Summary:     "Fetch a single captured ingest payload as raw JSON",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminGetIngest)

	// Admin: password policy
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-get-password-policy",
		Method:      http.MethodGet,
		Path:        "/v1/admin/security/password-policy",
		Summary:     "Get the active password policy",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleGetPasswordPolicy)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-set-password-policy",
		Method:      http.MethodPut,
		Path:        "/v1/admin/security/password-policy",
		Summary:     "Replace the password policy",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleSetPasswordPolicy)

	// SPA mount: anything not claimed by /v1, /healthz, /readyz, /docs is
	// served from the embedded React build. Registered last so huma's API
	// routes win.
	s.Router.Handle("/*", spa.Handler())
}

// --- Register ---------------------------------------------------------------

type registerInput struct {
	Authorization string `header:"Authorization" required:"true" doc:"Bearer <bootstrap-token>"`
	RawHost       string `header:"X-Forwarded-For" doc:"caller IP, set by reverse proxy"`
	Body          apitypes.AgentRegisterRequest
}

type registerOutput struct {
	Body apitypes.AgentRegisterResponse
}

func (s *Server) handleAgentRegister(ctx context.Context, in *registerInput) (*registerOutput, error) {
	token, ok := bearer(in.Authorization)
	if !ok {
		slog.Warn("agent register: missing bootstrap token", "remote", in.RawHost)
		return nil, huma.Error401Unauthorized("missing bootstrap token")
	}
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	resp, err := s.Store.RegisterAgent(ctx, token, in.Body, in.RawHost)
	if err != nil {
		if errors.Is(err, store.ErrTokenInvalid) {
			slog.Warn("agent register: bootstrap token rejected",
				"remote", in.RawHost, "hostname", in.Body.Hostname)
			return nil, huma.Error401Unauthorized("bootstrap token invalid or expired")
		}
		slog.Error("agent register failed", "err", err, "hostname", in.Body.Hostname)
		return nil, internalErr(ctx, "registration failed", err)
	}
	slog.Info("agent registered",
		"host_id", resp.AgentID,
		"hostname", in.Body.Hostname,
		"distro", in.Body.Distro,
		"arch", in.Body.Arch,
		"agent_version", in.Body.AgentVersion,
		"remote", in.RawHost)
	return &registerOutput{Body: resp}, nil
}

// --- Ingest -----------------------------------------------------------------

type ingestInput struct {
	Authorization string `header:"Authorization" required:"true" doc:"Bearer <agent_key>"`
	Body          apitypes.IngestRequest
}

type ingestOutput struct {
	Body apitypes.IngestResponse
}

func (s *Server) handleIngest(ctx context.Context, in *ingestInput) (*ingestOutput, error) {
	key, ok := bearer(in.Authorization)
	if !ok {
		slog.Warn("ingest: missing agent key")
		return nil, huma.Error401Unauthorized("missing agent key")
	}
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	hostID, err := s.Store.AuthenticateAgent(ctx, key)
	if err != nil {
		if errors.Is(err, store.ErrAgentKeyInvalid) {
			slog.Warn("ingest: agent key rejected")
			return nil, huma.Error401Unauthorized("agent key invalid")
		}
		return nil, internalErr(ctx, "auth failed", err)
	}

	b := in.Body
	pkgsCount := 0
	updatesCount := 0
	if b.Packages != nil {
		pkgsCount = b.Packages.Summary.InstalledCount
		updatesCount = b.Packages.Summary.UpdatesCount
	}
	hasInventory := b.Inventory != nil
	hasSecurity := b.Security != nil

	if err := s.Store.SaveIngest(ctx, hostID, b); err != nil {
		slog.Error("ingest persist failed",
			"host_id", hostID.String(),
			"err", err,
			"snapshot_at", b.SnapshotAt)
		return nil, internalErr(ctx, "ingest persist failed", err)
	}

	// Stash the canonical re-marshal in the ingest ring so admins can see
	// exactly what the agent uploaded. Re-encoding is fine — the parsed
	// struct is the authoritative shape after huma validation.
	if s.IngestBuffer != nil {
		if raw, err := json.Marshal(b); err == nil {
			hostname := ""
			if b.Inventory != nil {
				hostname = b.Inventory.Hostname
			}
			s.IngestBuffer.Append(hostID, hostname, raw)
		}
	}

	// Log a summary, not the payload itself: full dpkg listings are huge and
	// the operator can drill into individual rows via /v1/hosts/{id}/* anyway.
	slog.Info("ingest accepted",
		"host_id", hostID.String(),
		"snapshot_at", b.SnapshotAt,
		"system_samples", len(b.System),
		"disk_samples", len(b.Disks),
		"net_samples", len(b.Nics),
		"workload_samples", len(b.Workloads),
		"login_events", len(b.Logins),
		"packages_installed", pkgsCount,
		"packages_updates", updatesCount,
		"has_inventory", hasInventory,
		"has_security", hasSecurity)

	return &ingestOutput{Body: apitypes.IngestResponse{
		Accepted:   true,
		ServerTime: time.Now().UTC(),
	}}, nil
}

// --- List hosts -------------------------------------------------------------

type listHostsOutput struct {
	Body struct {
		Hosts []apitypes.Host `json:"hosts"`
	}
}

func (s *Server) handleListHosts(ctx context.Context, _ *struct{}) (*listHostsOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	hosts, err := s.Store.ListHosts(ctx)
	if err != nil {
		return nil, internalErr(ctx, "list hosts failed", err)
	}
	out := &listHostsOutput{}
	out.Body.Hosts = hosts
	return out, nil
}

// --- Host system metrics ----------------------------------------------------

type sysMetricsInput struct {
	ID   string    `path:"id"   doc:"host UUID"`
	From time.Time `query:"from" doc:"inclusive start (RFC3339); default = now-1h"`
	To   time.Time `query:"to"   doc:"inclusive end (RFC3339); default = now"`
}

type sysMetricsOutput struct {
	Body struct {
		HostID  string                  `json:"host_id"`
		From    time.Time               `json:"from"`
		To      time.Time               `json:"to"`
		Samples []apitypes.SystemSample `json:"samples"`
	}
}

// --- Host disk/net range metrics ------------------------------------------

type rangeMetricsInput struct {
	ID   string    `path:"id"`
	From time.Time `query:"from"`
	To   time.Time `query:"to"`
}

type diskMetricsOutput struct {
	Body struct {
		HostID  string                 `json:"host_id"`
		From    time.Time              `json:"from"`
		To      time.Time              `json:"to"`
		Devices []string               `json:"devices"`
		Samples []apitypes.DiskSample  `json:"samples"`
	}
}

func (s *Server) handleDiskMetrics(ctx context.Context, in *rangeMetricsInput) (*diskMetricsOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	to := in.To
	if to.IsZero() {
		to = time.Now().UTC()
	}
	from := in.From
	if from.IsZero() {
		from = to.Add(-1 * time.Hour)
	}
	samples, devices, err := s.Store.QueryDiskMetrics(ctx, id, from, to, nil)
	if err != nil {
		if errors.Is(err, store.ErrHostNotFound) {
			return nil, huma.Error404NotFound("host not found")
		}
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &diskMetricsOutput{}
	out.Body.HostID = id.String()
	out.Body.From = from
	out.Body.To = to
	out.Body.Devices = devices
	out.Body.Samples = samples
	return out, nil
}

type netMetricsOutput struct {
	Body struct {
		HostID  string               `json:"host_id"`
		From    time.Time            `json:"from"`
		To      time.Time            `json:"to"`
		Nics    []string             `json:"nics"`
		Samples []apitypes.NetSample `json:"samples"`
	}
}

func (s *Server) handleNetMetrics(ctx context.Context, in *rangeMetricsInput) (*netMetricsOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	to := in.To
	if to.IsZero() {
		to = time.Now().UTC()
	}
	from := in.From
	if from.IsZero() {
		from = to.Add(-1 * time.Hour)
	}
	samples, nics, err := s.Store.QueryNetMetrics(ctx, id, from, to, nil)
	if err != nil {
		if errors.Is(err, store.ErrHostNotFound) {
			return nil, huma.Error404NotFound("host not found")
		}
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &netMetricsOutput{}
	out.Body.HostID = id.String()
	out.Body.From = from
	out.Body.To = to
	out.Body.Nics = nics
	out.Body.Samples = samples
	return out, nil
}

// --- Global packages search -----------------------------------------------

type searchPackagesInput struct {
	Q       string `query:"q"        doc:"substring match on package name or version"`
	Manager string `query:"manager"  enum:"dpkg,rpm,pacman,apk" doc:"filter by manager"`
	HostID  string `query:"host_id"  doc:"restrict to a single host"`
	Limit   int    `query:"limit"    minimum:"1" maximum:"1000"`
	Offset  int    `query:"offset"   minimum:"0"`
}

type searchPackagesOutput struct {
	Body struct {
		Total    int                         `json:"total"`
		Limit    int                         `json:"limit"`
		Offset   int                         `json:"offset"`
		Packages []apitypes.GlobalPackageRow `json:"packages"`
	}
}

func (s *Server) handleSearchPackages(ctx context.Context, in *searchPackagesInput) (*searchPackagesOutput, error) {
	var hostID *uuid.UUID
	if in.HostID != "" {
		id, err := uuid.Parse(in.HostID)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid host_id")
		}
		hostID = &id
	}
	results, total, err := s.Store.SearchPackages(ctx, in.Q, in.Manager, hostID, in.Limit, in.Offset)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	out := &searchPackagesOutput{}
	out.Body.Total = total
	out.Body.Limit = limit
	out.Body.Offset = in.Offset
	for _, r := range results {
		out.Body.Packages = append(out.Body.Packages, apitypes.GlobalPackageRow{
			HostID: r.HostID, Hostname: r.Hostname, Manager: r.Manager,
			Name: r.Name, Version: r.Version, Arch: r.Arch,
			SourceRepo: r.SourceRepo, InstalledAt: r.InstalledAt,
		})
	}
	return out, nil
}

// --- Tags + groups ---------------------------------------------------------

type setHostTagsInput struct {
	ID   string `path:"id"`
	Body apitypes.HostTagsInput
}

func (s *Server) handleSetHostTags(ctx context.Context, in *setHostTagsInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid host id")
	}
	if err := s.Store.ReplaceHostTags(ctx, id, in.Body.Tags); err != nil {
		if errors.Is(err, store.ErrHostNotFound) {
			return nil, huma.Error404NotFound("host not found")
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type listTagsOutput struct {
	Body struct {
		Tags []struct {
			Tag   string `json:"tag"`
			Count int    `json:"count"`
		} `json:"tags"`
	}
}

func (s *Server) handleListTags(ctx context.Context, _ *struct{}) (*listTagsOutput, error) {
	out := &listTagsOutput{}
	tags, err := s.Store.ListAllTags(ctx)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	for _, t := range tags {
		out.Body.Tags = append(out.Body.Tags, struct {
			Tag   string `json:"tag"`
			Count int    `json:"count"`
		}{Tag: t.Tag, Count: t.Count})
	}
	return out, nil
}

type listGroupsOutput struct {
	Body struct {
		Groups []apitypes.HostGroup `json:"groups"`
	}
}

func (s *Server) handleListGroups(ctx context.Context, _ *struct{}) (*listGroupsOutput, error) {
	groups, err := s.Store.ListGroups(ctx)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &listGroupsOutput{}
	out.Body.Groups = groups
	return out, nil
}

type createGroupInput struct {
	Body apitypes.HostGroupInput
}
type groupOutput struct {
	Body apitypes.HostGroup
}

func (s *Server) handleCreateGroup(ctx context.Context, in *createGroupInput) (*groupOutput, error) {
	u, _ := userFromContext(ctx)
	g, err := s.Store.CreateGroup(ctx, in.Body, u.Email)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &groupOutput{Body: g}, nil
}

type updateGroupInput struct {
	ID   string `path:"id"`
	Body apitypes.HostGroupInput
}

func (s *Server) handleUpdateGroup(ctx context.Context, in *updateGroupInput) (*groupOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	g, err := s.Store.UpdateGroup(ctx, id, in.Body)
	if err != nil {
		if errors.Is(err, store.ErrGroupNotFound) {
			return nil, huma.Error404NotFound("group not found")
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &groupOutput{Body: g}, nil
}

type groupIDInput struct {
	ID string `path:"id"`
}

func (s *Server) handleDeleteGroup(ctx context.Context, in *groupIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := s.Store.DeleteGroup(ctx, id); err != nil {
		if errors.Is(err, store.ErrGroupNotFound) {
			return nil, huma.Error404NotFound("group not found")
		}
		return nil, internalErr(ctx, "delete failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type setGroupMembersInput struct {
	ID   string `path:"id"`
	Body apitypes.GroupMembersInput
}

func (s *Server) handleSetGroupMembers(ctx context.Context, in *setGroupMembersInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	hostIDs := make([]uuid.UUID, 0, len(in.Body.HostIDs))
	for _, s := range in.Body.HostIDs {
		hid, err := uuid.Parse(s)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid host id: " + s)
		}
		hostIDs = append(hostIDs, hid)
	}
	if err := s.Store.ReplaceGroupMembers(ctx, id, hostIDs); err != nil {
		if errors.Is(err, store.ErrGroupNotFound) {
			return nil, huma.Error404NotFound("group not found")
		}
		return nil, internalErr(ctx, "update failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// --- Host detail (single host with bundles) --------------------------------

type hostDetailOutput struct {
	Body apitypes.HostDetail
}

func (s *Server) handleHostDetail(ctx context.Context, in *hostIDInput) (*hostDetailOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid host id")
	}
	d, err := s.Store.GetHostDetail(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrHostNotFound) {
			return nil, huma.Error404NotFound("host not found")
		}
		return nil, internalErr(ctx, "query failed", err)
	}
	return &hostDetailOutput{Body: d}, nil
}

// --- Host packages ---------------------------------------------------------

type hostPackagesInput struct {
	ID     string `path:"id"`
	Limit  int    `query:"limit"  doc:"max rows (1..1000); default 200"`
	Offset int    `query:"offset" doc:"page offset; default 0"`
}
type hostPackagesOutput struct {
	Body struct {
		Total    int                   `json:"total"`
		Limit    int                   `json:"limit"`
		Offset   int                   `json:"offset"`
		Packages []apitypes.PackageRow `json:"packages"`
	}
}

func (s *Server) handleHostPackages(ctx context.Context, in *hostPackagesInput) (*hostPackagesOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid host id")
	}
	pkgs, total, err := s.Store.ListHostPackages(ctx, id, in.Limit, in.Offset)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &hostPackagesOutput{}
	out.Body.Total = total
	out.Body.Limit = in.Limit
	if out.Body.Limit <= 0 {
		out.Body.Limit = 200
	}
	out.Body.Offset = in.Offset
	out.Body.Packages = pkgs
	return out, nil
}

type hostPackageUpdatesOutput struct {
	Body struct {
		Updates []apitypes.PendingUpdate `json:"updates"`
	}
}

func (s *Server) handleHostPackageUpdates(ctx context.Context, in *hostIDInput) (*hostPackageUpdatesOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid host id")
	}
	ups, err := s.Store.ListHostPackageUpdates(ctx, id)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &hostPackageUpdatesOutput{}
	out.Body.Updates = ups
	return out, nil
}

// --- Host security snapshot -------------------------------------------------

type hostIDInput struct {
	ID string `path:"id" doc:"host UUID"`
}

type hostSecurityOutput struct {
	Body struct {
		HostID    string                      `json:"host_id"`
		Firewalls []apitypes.FirewallStatus   `json:"firewalls"`
		Fail2ban  []apitypes.Fail2banJailInfo `json:"fail2ban"`
		CrowdSec  []apitypes.CrowdsecDecision `json:"crowdsec"`
	}
}

func (s *Server) handleHostSecurity(ctx context.Context, in *hostIDInput) (*hostSecurityOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	hostID, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid host id")
	}
	hs, err := s.Store.HostSecurity(ctx, hostID)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &hostSecurityOutput{}
	out.Body.HostID = hostID.String()
	out.Body.Firewalls = hs.Firewalls
	out.Body.Fail2ban = hs.Fail2ban
	out.Body.CrowdSec = hs.CrowdSec
	return out, nil
}

// --- Host logins ------------------------------------------------------------

type hostLoginsInput struct {
	ID    string    `path:"id"     doc:"host UUID"`
	Since time.Time `query:"since" doc:"earliest event timestamp (RFC3339); default = now-24h"`
	Limit int       `query:"limit" doc:"max events to return (1..1000); default 200"`
}

type hostLoginsOutput struct {
	Body struct {
		HostID string                `json:"host_id"`
		Since  time.Time             `json:"since"`
		Events []apitypes.LoginEvent `json:"events"`
	}
}

func (s *Server) handleHostLogins(ctx context.Context, in *hostLoginsInput) (*hostLoginsOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	hostID, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid host id")
	}
	since := in.Since
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour).UTC()
	}
	events, err := s.Store.ListHostLogins(ctx, hostID, since, in.Limit)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &hostLoginsOutput{}
	out.Body.HostID = hostID.String()
	out.Body.Since = since
	out.Body.Events = events
	return out, nil
}

func (s *Server) handleSystemMetrics(ctx context.Context, in *sysMetricsInput) (*sysMetricsOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	hostID, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid host id")
	}
	to := in.To
	if to.IsZero() {
		to = time.Now().UTC()
	}
	from := in.From
	if from.IsZero() {
		from = to.Add(-1 * time.Hour)
	}
	if from.After(to) {
		return nil, huma.Error400BadRequest("from must be <= to")
	}
	samples, err := s.Store.QuerySystemMetrics(ctx, hostID, from, to)
	if err != nil {
		if errors.Is(err, store.ErrHostNotFound) {
			return nil, huma.Error404NotFound("host not found")
		}
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &sysMetricsOutput{}
	out.Body.HostID = hostID.String()
	out.Body.From = from
	out.Body.To = to
	out.Body.Samples = samples
	return out, nil
}

func bearer(h string) (string, bool) {
	const p = "Bearer "
	if !strings.HasPrefix(h, p) {
		return "", false
	}
	t := strings.TrimSpace(h[len(p):])
	return t, t != ""
}

// --- Notification rules + alert history ------------------------------------

type ruleIDInput struct {
	ID string `path:"id" doc:"Rule UUID"`
}

type listRulesOutput struct {
	Body struct {
		Rules []apitypes.NotificationRule `json:"rules"`
	}
}

func (s *Server) handleListRules(ctx context.Context, _ *struct{}) (*listRulesOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	rs, err := s.Store.ListRules(ctx)
	if err != nil {
		return nil, internalErr(ctx, "list failed", err)
	}
	out := &listRulesOutput{}
	out.Body.Rules = rs
	return out, nil
}

type createRuleInput struct {
	Body apitypes.NotificationRuleInput
}
type ruleOutput struct {
	Body apitypes.NotificationRule
}

func (s *Server) handleCreateRule(ctx context.Context, in *createRuleInput) (*ruleOutput, error) {
	u, _ := userFromContext(ctx)
	r, err := s.Store.CreateRule(ctx, in.Body, u.Email)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &ruleOutput{Body: r}, nil
}

type updateRuleInput struct {
	ID   string `path:"id"`
	Body apitypes.NotificationRuleInput
}

func (s *Server) handleUpdateRule(ctx context.Context, in *updateRuleInput) (*ruleOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	r, err := s.Store.UpdateRule(ctx, id, in.Body)
	if err != nil {
		if errors.Is(err, store.ErrRuleNotFound) {
			return nil, huma.Error404NotFound("rule not found")
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &ruleOutput{Body: r}, nil
}

func (s *Server) handleDeleteRule(ctx context.Context, in *ruleIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := s.Store.DeleteRule(ctx, id); err != nil {
		if errors.Is(err, store.ErrRuleNotFound) {
			return nil, huma.Error404NotFound("rule not found")
		}
		return nil, internalErr(ctx, "delete failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type alertHistoryInput struct {
	Since time.Time `query:"since" doc:"earliest alert timestamp; default = now-24h"`
	Limit int       `query:"limit" doc:"max entries (1..1000); default 200"`
}
type alertHistoryOutput struct {
	Body struct {
		Alerts []apitypes.AlertHistoryEntry `json:"alerts"`
	}
}

func (s *Server) handleAlertHistory(ctx context.Context, in *alertHistoryInput) (*alertHistoryOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	since := in.Since
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour).UTC()
	}
	alerts, err := s.Store.ListAlertHistory(ctx, since, in.Limit)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &alertHistoryOutput{}
	out.Body.Alerts = alerts
	return out, nil
}

// --- Active monitors --------------------------------------------------------

type monitorIDInput struct {
	ID string `path:"id" doc:"Monitor UUID"`
}

type listMonitorsOutput struct {
	Body struct {
		Monitors []apitypes.Monitor `json:"monitors"`
	}
}

func (s *Server) handleListMonitors(ctx context.Context, _ *struct{}) (*listMonitorsOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	ms, err := s.Store.ListMonitors(ctx)
	if err != nil {
		return nil, internalErr(ctx, "list failed", err)
	}
	out := &listMonitorsOutput{}
	out.Body.Monitors = ms
	return out, nil
}

type createMonitorInput struct {
	Body apitypes.MonitorInput
}
type monitorOutput struct {
	Body apitypes.Monitor
}

func (s *Server) handleCreateMonitor(ctx context.Context, in *createMonitorInput) (*monitorOutput, error) {
	u, _ := userFromContext(ctx)
	m, err := s.Store.CreateMonitor(ctx, in.Body, u.Email)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &monitorOutput{Body: m}, nil
}

func (s *Server) handleGetMonitor(ctx context.Context, in *monitorIDInput) (*monitorOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	m, err := s.Store.GetMonitor(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrMonitorNotFound) {
			return nil, huma.Error404NotFound("monitor not found")
		}
		return nil, internalErr(ctx, "get failed", err)
	}
	return &monitorOutput{Body: m}, nil
}

type updateMonitorInput struct {
	ID   string `path:"id"`
	Body apitypes.MonitorInput
}

func (s *Server) handleUpdateMonitor(ctx context.Context, in *updateMonitorInput) (*monitorOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	m, err := s.Store.UpdateMonitor(ctx, id, in.Body)
	if err != nil {
		if errors.Is(err, store.ErrMonitorNotFound) {
			return nil, huma.Error404NotFound("monitor not found")
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &monitorOutput{Body: m}, nil
}

func (s *Server) handleDeleteMonitor(ctx context.Context, in *monitorIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := s.Store.DeleteMonitor(ctx, id); err != nil {
		if errors.Is(err, store.ErrMonitorNotFound) {
			return nil, huma.Error404NotFound("monitor not found")
		}
		return nil, internalErr(ctx, "delete failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type monitorResultsInput struct {
	ID    string    `path:"id"`
	Since time.Time `query:"since" doc:"earliest result timestamp; default = now-24h"`
	Limit int       `query:"limit" doc:"max results (1..1000); default 200"`
}
type monitorResultsOutput struct {
	Body struct {
		MonitorID string                   `json:"monitor_id"`
		Results   []apitypes.MonitorResult `json:"results"`
	}
}

func (s *Server) handleMonitorResults(ctx context.Context, in *monitorResultsInput) (*monitorResultsOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	since := in.Since
	if since.IsZero() {
		since = time.Now().Add(-24 * time.Hour).UTC()
	}
	rs, err := s.Store.MonitorResults(ctx, id, since, in.Limit)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &monitorResultsOutput{}
	out.Body.MonitorID = id.String()
	out.Body.Results = rs
	return out, nil
}

// --- Notification channels --------------------------------------------------

type channelIDInput struct {
	ID string `path:"id" doc:"Channel UUID"`
}

type listChannelsOutput struct {
	Body struct {
		Channels []apitypes.NotificationChannel `json:"channels"`
	}
}

func (s *Server) handleListChannels(ctx context.Context, _ *struct{}) (*listChannelsOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	u, _ := userFromContext(ctx)
	cs, err := s.Store.ListChannels(ctx, u.ID, u.Role == "admin")
	if err != nil {
		return nil, internalErr(ctx, "list failed", err)
	}
	out := &listChannelsOutput{}
	out.Body.Channels = cs
	return out, nil
}

type channelInput struct {
	Body apitypes.NotificationChannelInput
}
type channelOutput struct {
	Body apitypes.NotificationChannel
}

func (s *Server) handleCreateChannel(ctx context.Context, in *channelInput) (*channelOutput, error) {
	u, _ := userFromContext(ctx)
	// Default email recipient to caller's account email — the common case.
	if strings.EqualFold(in.Body.Type, "email") && in.Body.RecipientEmail == "" {
		in.Body.RecipientEmail = u.Email
	}
	owner := &u.ID
	c, err := s.Store.CreateChannel(ctx, in.Body, u.Email, owner)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &channelOutput{Body: c}, nil
}

func (s *Server) handleGetChannel(ctx context.Context, in *channelIDInput) (*channelOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	u, _ := userFromContext(ctx)
	c, err := s.Store.GetChannel(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrChannelNotFound) {
			return nil, huma.Error404NotFound("channel not found")
		}
		return nil, internalErr(ctx, "get failed", err)
	}
	// Hide channels owned by other users from non-admin callers.
	if u.Role != "admin" && c.OwnerUserID != "" && c.OwnerUserID != u.ID.String() {
		return nil, huma.Error404NotFound("channel not found")
	}
	return &channelOutput{Body: c}, nil
}

type updateChannelInput struct {
	ID   string `path:"id"`
	Body apitypes.NotificationChannelInput
}

func (s *Server) handleUpdateChannel(ctx context.Context, in *updateChannelInput) (*channelOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	u, _ := userFromContext(ctx)
	isAdmin := u.Role == "admin"
	if strings.EqualFold(in.Body.Type, "email") && in.Body.RecipientEmail == "" {
		in.Body.RecipientEmail = u.Email
	}
	c, err := s.Store.UpdateChannel(ctx, id, in.Body, u.ID, isAdmin)
	if err != nil {
		if errors.Is(err, store.ErrChannelNotFound) {
			return nil, huma.Error404NotFound("channel not found")
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &channelOutput{Body: c}, nil
}

func (s *Server) handleDeleteChannel(ctx context.Context, in *channelIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	u, _ := userFromContext(ctx)
	if err := s.Store.DeleteChannel(ctx, id, u.ID, u.Role == "admin"); err != nil {
		if errors.Is(err, store.ErrChannelNotFound) {
			return nil, huma.Error404NotFound("channel not found")
		}
		return nil, internalErr(ctx, "delete failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type testChannelInput struct {
	ID   string `path:"id"`
	Body apitypes.NotificationTestRequest
}

type testChannelOutput struct {
	Body apitypes.NotificationTestResponse
}

func (s *Server) handleTestChannel(ctx context.Context, in *testChannelInput) (*testChannelOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	// Visibility check: non-admin callers may only test their own channels.
	u, _ := userFromContext(ctx)
	if u.Role != "admin" {
		c, err := s.Store.GetChannel(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrChannelNotFound) {
				return nil, huma.Error404NotFound("channel not found")
			}
			return nil, internalErr(ctx, "get failed", err)
		}
		if c.OwnerUserID != u.ID.String() {
			return nil, huma.Error404NotFound("channel not found")
		}
	}

	subject := in.Body.Subject
	if subject == "" {
		subject = "mon test message"
	}
	body := in.Body.Body
	if body == "" {
		body = "If you see this, the channel works."
	}
	out := &testChannelOutput{}
	if err := s.Store.SendChannel(ctx, id, notify.Message{
		Subject:  subject,
		Body:     body,
		Severity: "info",
	}); err != nil {
		out.Body.OK = false
		out.Body.Error = err.Error()
		return out, nil
	}
	out.Body.OK = true
	return out, nil
}

// --- Auth: login / logout / me ---------------------------------------------

type loginInput struct {
	Body apitypes.LoginRequest
}
type loginOutput struct {
	Body apitypes.LoginResponse
}

func (s *Server) handleLogin(ctx context.Context, in *loginInput) (*loginOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	if in.Body.Email == "" || in.Body.Password == "" {
		return nil, huma.Error400BadRequest("email and password required")
	}
	u, err := s.Store.AuthenticateUser(ctx, in.Body.Email, in.Body.Password)
	if err != nil {
		// Avoid leaking whether the email exists.
		if errors.Is(err, store.ErrUserNotFound) || errors.Is(err, store.ErrPasswordMismatch) {
			return nil, huma.Error401Unauthorized("invalid credentials")
		}
		if errors.Is(err, store.ErrUserDisabled) {
			return nil, huma.Error403Forbidden("user is disabled")
		}
		if errors.Is(err, store.ErrUserLockedOut) {
			return nil, huma.Error429TooManyRequests("account temporarily locked; retry later")
		}
		return nil, internalErr(ctx, "login failed", err)
	}

	// 2FA gate: if the user has TOTP enabled, defer the session issue until
	// they pass /v1/auth/2fa/challenge with a fresh code.
	if u.TOTPActive {
		challenge, err := s.Store.CreateActionToken(ctx, u.ID, "login_2fa", 5*time.Minute, nil, "")
		if err != nil {
			return nil, internalErr(ctx, "challenge create failed", err)
		}
		out := &loginOutput{}
		out.Body.NeedsTOTP = true
		out.Body.ChallengeToken = challenge
		out.Body.ExpiresAt = time.Now().Add(5 * time.Minute).UTC()
		out.Body.User = apitypes.CurrentUser{
			ID: u.ID.String(), Email: u.Email, Role: u.Role, TOTPActive: true,
		}
		return out, nil
	}

	token, err := s.Store.IssueSession(ctx, u, "", "", 0)
	if err != nil {
		return nil, internalErr(ctx, "session create failed", err)
	}
	out := &loginOutput{}
	out.Body.Token = token
	out.Body.ExpiresAt = time.Now().Add(12 * time.Hour).UTC()
	out.Body.User = apitypes.CurrentUser{
		ID: u.ID.String(), Email: u.Email, Role: u.Role, TOTPActive: u.TOTPActive,
	}
	return out, nil
}

// --- 2FA challenge after password login ------------------------------------

type totpChallengeInput struct {
	Body apitypes.TOTPChallengeRequest
}
type totpChallengeOutput struct {
	Body apitypes.LoginResponse
}

func (s *Server) handleTOTPChallenge(ctx context.Context, in *totpChallengeInput) (*totpChallengeOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	if in.Body.ChallengeToken == "" || in.Body.Code == "" {
		return nil, huma.Error400BadRequest("challenge_token and code required")
	}
	userID, _, err := s.Store.ConsumeActionToken(ctx, in.Body.ChallengeToken, "login_2fa")
	if err != nil {
		return nil, huma.Error401Unauthorized("invalid or expired challenge")
	}
	if err := s.Store.VerifyTOTP(ctx, userID, in.Body.Code); err != nil {
		return nil, huma.Error401Unauthorized("invalid TOTP code")
	}
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return nil, internalErr(ctx, "user lookup failed", err)
	}
	token, err := s.Store.IssueSession(ctx, u, "", "", 0)
	if err != nil {
		return nil, internalErr(ctx, "session create failed", err)
	}
	out := &totpChallengeOutput{}
	out.Body.Token = token
	out.Body.ExpiresAt = time.Now().Add(12 * time.Hour).UTC()
	out.Body.User = apitypes.CurrentUser{
		ID: u.ID.String(), Email: u.Email, Role: u.Role, TOTPActive: true,
	}
	return out, nil
}

// --- Self-service profile --------------------------------------------------

type changePasswordInput struct {
	Body apitypes.ChangePasswordRequest
}

func (s *Server) handleChangePassword(ctx context.Context, in *changePasswordInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	if in.Body.NewPassword == "" || in.Body.CurrentPassword == "" {
		return nil, huma.Error400BadRequest("current_password and new_password required")
	}
	if err := s.Store.CheckPassword(ctx, in.Body.NewPassword); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if err := s.Store.ChangePassword(ctx, u.ID, in.Body.CurrentPassword, in.Body.NewPassword); err != nil {
		if errors.Is(err, store.ErrPasswordMismatch) {
			return nil, huma.Error401Unauthorized("current password is wrong")
		}
		return nil, internalErr(ctx, "change password failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type changeEmailInput struct {
	Body apitypes.ChangeEmailRequest
}

func (s *Server) handleChangeEmail(ctx context.Context, in *changeEmailInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	if err := s.Store.ChangeEmail(ctx, u.ID, in.Body.CurrentPassword, in.Body.NewEmail); err != nil {
		if errors.Is(err, store.ErrPasswordMismatch) {
			return nil, huma.Error401Unauthorized("current password is wrong")
		}
		if errors.Is(err, store.ErrUserExists) {
			return nil, huma.Error409Conflict("email already in use")
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// --- 2FA self-management --------------------------------------------------

type totpSetupOutput struct {
	Body apitypes.TOTPSetupResponse
}

func (s *Server) handleTOTPSetup(ctx context.Context, _ *struct{}) (*totpSetupOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	resp, err := s.Store.StartTOTPSetup(ctx, u)
	if err != nil {
		return nil, internalErr(ctx, "totp setup failed", err)
	}
	return &totpSetupOutput{Body: resp}, nil
}

type totpVerifyInput struct {
	Body apitypes.TOTPVerifyRequest
}

func (s *Server) handleTOTPVerify(ctx context.Context, in *totpVerifyInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	if err := s.Store.VerifyTOTP(ctx, u.ID, in.Body.Code); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type totpDisableInput struct {
	Body apitypes.TOTPDisableRequest
}

func (s *Server) handleTOTPDisable(ctx context.Context, in *totpDisableInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	// Verify the password first — we never want a session-hijacked attacker
	// to be able to remove a user's second factor.
	if _, err := s.Store.AuthenticateUser(ctx, u.Email, in.Body.Password); err != nil {
		return nil, huma.Error401Unauthorized("invalid password")
	}
	if err := s.Store.DisableTOTP(ctx, u.ID); err != nil {
		return nil, internalErr(ctx, "disable failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type emptyInput struct{}
type emptyOutput struct {
	Body struct {
		OK bool `json:"ok"`
	}
}

func (s *Server) handleLogout(ctx context.Context, _ *emptyInput) (*emptyOutput, error) {
	tok, _ := tokenFromContext(ctx)
	if tok != "" {
		_ = s.Store.RevokeSession(ctx, tok)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type meOutput struct {
	Body apitypes.CurrentUser
}

func (s *Server) handleMe(ctx context.Context, _ *emptyInput) (*meOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	// Re-fetch so TOTPActive reflects the latest state (the cached User in
	// context is from session validation, where it was filled at login).
	full, err := s.Store.GetUser(ctx, u.ID)
	if err != nil {
		return nil, internalErr(ctx, "user fetch failed", err)
	}
	return &meOutput{Body: apitypes.CurrentUser{
		ID:         full.ID.String(),
		Email:      full.Email,
		Role:       full.Role,
		TOTPActive: full.TOTPActive,
	}}, nil
}

// --- Session middleware ----------------------------------------------------

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
	ctxKeyToken
)

// requireAdmin chains after requireUser and rejects non-admin callers.
// huma middlewares run in registration order, so callers stack them as
// `huma.Middlewares{s.requireUser, s.requireAdmin}`.
func (s *Server) requireAdmin(c huma.Context, next func(huma.Context)) {
	u, ok := c.Context().Value(ctxKeyUser).(store.User)
	if !ok {
		_ = huma.WriteErr(s.API, c, http.StatusUnauthorized, "no session")
		return
	}
	if u.Role != "admin" {
		_ = huma.WriteErr(s.API, c, http.StatusForbidden, "admin role required")
		return
	}
	next(c)
}

// requireUser is a huma middleware: verifies session token, stashes user on
// the context, denies with 401 otherwise.
func (s *Server) requireUser(c huma.Context, next func(huma.Context)) {
	if s.Store == nil {
		_ = huma.WriteErr(s.API, c, http.StatusServiceUnavailable,
			"server has no store configured")
		return
	}
	tok, ok := bearer(c.Header("Authorization"))
	if !ok {
		_ = huma.WriteErr(s.API, c, http.StatusUnauthorized,
			"missing session token")
		return
	}
	u, err := s.Store.ValidateSession(c.Context(), tok)
	if err != nil {
		_ = huma.WriteErr(s.API, c, http.StatusUnauthorized,
			"invalid session")
		return
	}
	ctx := context.WithValue(c.Context(), ctxKeyUser, u)
	ctx = context.WithValue(ctx, ctxKeyToken, tok)
	next(huma.WithContext(c, ctx))
}

func userFromContext(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxKeyUser).(store.User)
	return u, ok
}

func tokenFromContext(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(ctxKeyToken).(string)
	return t, ok
}

// --- Reset / invite consume (public) ---------------------------------------

type consumeResetInput struct {
	Body apitypes.ConsumeResetTokenRequest
}

func (s *Server) handleConsumeReset(ctx context.Context, in *consumeResetInput) (*emptyOutput, error) {
	if in.Body.Token == "" || in.Body.NewPassword == "" {
		return nil, huma.Error400BadRequest("token and new_password required")
	}
	if err := s.Store.CheckPassword(ctx, in.Body.NewPassword); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	// Try the password_reset and invite kinds; both produce a settable
	// password.
	userID, _, err := s.Store.ConsumeActionToken(ctx, in.Body.Token, "")
	if err != nil {
		return nil, huma.Error401Unauthorized("token invalid or expired")
	}
	if err := s.Store.SetPasswordByAdmin(ctx, userID, in.Body.NewPassword); err != nil {
		return nil, internalErr(ctx, "set password failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// --- Admin: user management -----------------------------------------------

type listUsersOutput struct {
	Body struct {
		Users []apitypes.AdminUserSummary `json:"users"`
	}
}

func (s *Server) handleListUsers(ctx context.Context, _ *struct{}) (*listUsersOutput, error) {
	us, err := s.Store.ListUsers(ctx)
	if err != nil {
		return nil, internalErr(ctx, "list failed", err)
	}
	out := &listUsersOutput{}
	for _, u := range us {
		out.Body.Users = append(out.Body.Users, apitypes.AdminUserSummary{
			ID:          u.ID.String(),
			Email:       u.Email,
			Role:        u.Role,
			CreatedAt:   u.CreatedAt,
			DisabledAt:  u.DisabledAt,
			TOTPActive:  u.TOTPActive,
			LastLoginAt: u.LastLoginAt,
		})
	}
	return out, nil
}

type adminCreateUserInput struct {
	Body apitypes.AdminCreateUserRequest
}
type adminCreateUserOutput struct {
	Body apitypes.AdminCreateUserResponse
}

func (s *Server) handleAdminCreateUser(ctx context.Context, in *adminCreateUserInput) (*adminCreateUserOutput, error) {
	if in.Body.Email == "" {
		return nil, huma.Error400BadRequest("email required")
	}
	role := in.Body.Role
	if role == "" {
		role = "user"
	}

	out := &adminCreateUserOutput{}

	if in.Body.Password != "" {
		if err := s.Store.CheckPassword(ctx, in.Body.Password); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		u, err := s.Store.CreateUser(ctx, in.Body.Email, in.Body.Password, role)
		if err != nil {
			if errors.Is(err, store.ErrUserExists) {
				return nil, huma.Error409Conflict("user already exists")
			}
			return nil, huma.Error400BadRequest(err.Error())
		}
		out.Body.User = apitypes.AdminUserSummary{
			ID: u.ID.String(), Email: u.Email, Role: u.Role, CreatedAt: u.CreatedAt,
		}
		return out, nil
	}

	// Invite path: create with a random placeholder password the user can
	// never know, then issue an invite token.
	tmp, err := randomPlaceholder()
	if err != nil {
		return nil, internalErr(ctx, "invite failed", err)
	}
	u, err := s.Store.CreateUser(ctx, in.Body.Email, tmp, role)
	if err != nil {
		if errors.Is(err, store.ErrUserExists) {
			return nil, huma.Error409Conflict("user already exists")
		}
		return nil, huma.Error400BadRequest(err.Error())
	}

	actor, _ := userFromContext(ctx)
	tok, err := s.Store.CreateActionToken(ctx, u.ID, "invite", 7*24*time.Hour, nil, actor.Email)
	if err != nil {
		return nil, internalErr(ctx, "invite token failed", err)
	}
	out.Body.User = apitypes.AdminUserSummary{
		ID: u.ID.String(), Email: u.Email, Role: u.Role, CreatedAt: u.CreatedAt,
	}
	out.Body.ResetURL = inviteURL(tok)

	if in.Body.SendInvite {
		// Optional best-effort email via SMTP channel of name "system".
		// If absent, the admin still gets a copy-paste URL.
		if err := s.sendInviteMail(ctx, u.Email, out.Body.ResetURL); err == nil {
			out.Body.InviteSent = true
		}
	}
	return out, nil
}

// inviteURL builds the URL that will land on the SPA's /reset page. The SPA
// is responsible for rendering a "set your password" form and posting the
// token + new password to /v1/auth/consume-reset.
func inviteURL(token string) string { return "/reset?token=" + token }

func randomPlaceholder() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "x!" + base64.RawURLEncoding.EncodeToString(b), nil
}

// sendInviteMail dispatches a welcome message via the global SMTP transport
// directly to the invitee's address. Failure is non-fatal — the admin still
// has the URL in the response.
func (s *Server) sendInviteMail(ctx context.Context, recipient, url string) error {
	if recipient == "" {
		return errors.New("invite mail: empty recipient")
	}
	settings, err := s.Store.GetSmtpSettings(ctx)
	if err != nil {
		return err
	}
	cfg := map[string]any{
		"host":                 settings.Host,
		"port":                 settings.Port,
		"username":             settings.Username,
		"password":             settings.Password,
		"from":                 settings.FromAddress,
		"to":                   []string{recipient},
		"starttls":             settings.StartTLS,
		"tls":                  settings.TLS,
		"insecure_skip_verify": settings.InsecureSkipVerify,
	}
	return notify.Dispatch(ctx, notify.Channel{
		ID: "invite-mail", Type: "email", Name: "invite", Config: cfg,
	}, notify.Message{
		Subject:  "Welcome to mon",
		Body:     "An admin invited you to mon. Open this link to set your password:\n\n" + url,
		Severity: "info",
	})
}

func (s *Server) handleAdminDeleteUser(ctx context.Context, in *hostIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	caller, _ := userFromContext(ctx)
	if caller.ID == id {
		return nil, huma.Error400BadRequest("cannot delete yourself")
	}
	if err := s.refuseIfLastAdmin(ctx, id, "delete"); err != nil {
		return nil, err
	}
	if err := s.Store.DeleteUser(ctx, id); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, huma.Error404NotFound("user not found")
		}
		return nil, internalErr(ctx, "delete failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

func (s *Server) handleAdminLockUser(ctx context.Context, in *hostIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	caller, _ := userFromContext(ctx)
	if caller.ID == id {
		return nil, huma.Error400BadRequest("cannot lock yourself")
	}
	if err := s.refuseIfLastAdmin(ctx, id, "lock"); err != nil {
		return nil, err
	}
	if err := s.Store.SetUserDisabled(ctx, id, true); err != nil {
		return nil, internalErr(ctx, "lock failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// refuseIfLastAdmin returns a 400 if removing/disabling target would leave
// the system without any enabled admin. Looking up the target's role first
// avoids the count query when the target is a regular user.
func (s *Server) refuseIfLastAdmin(ctx context.Context, target uuid.UUID, action string) error {
	u, err := s.Store.GetUser(ctx, target)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return huma.Error404NotFound("user not found")
		}
		return internalErr(ctx, "user fetch failed", err)
	}
	if u.Role != "admin" {
		return nil
	}
	last, err := s.Store.IsLastEnabledAdmin(ctx, target)
	if err != nil {
		return internalErr(ctx, "admin check failed", err)
	}
	if last {
		return huma.Error400BadRequest("cannot " + action + " the last enabled admin")
	}
	return nil
}

func (s *Server) handleAdminUnlockUser(ctx context.Context, in *hostIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := s.Store.SetUserDisabled(ctx, id, false); err != nil {
		return nil, internalErr(ctx, "unlock failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type adminResetPwOutput struct {
	Body apitypes.AdminResetPasswordResponse
}

func (s *Server) handleAdminResetPassword(ctx context.Context, in *hostIDInput) (*adminResetPwOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, huma.Error404NotFound("user not found")
		}
		return nil, internalErr(ctx, "user fetch failed", err)
	}
	actor, _ := userFromContext(ctx)
	tok, err := s.Store.CreateActionToken(ctx, id, "password_reset", 24*time.Hour, nil, actor.Email)
	if err != nil {
		return nil, internalErr(ctx, "reset token failed", err)
	}
	out := &adminResetPwOutput{}
	out.Body.ResetURL = inviteURL(tok)
	if err := s.sendInviteMail(ctx, u.Email, out.Body.ResetURL); err == nil {
		out.Body.InviteSent = true
	}
	return out, nil
}

func (s *Server) handleAdminReset2FA(ctx context.Context, in *hostIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := s.Store.DisableTOTP(ctx, id); err != nil {
		return nil, internalErr(ctx, "reset 2fa failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// --- SMTP settings (admin) -------------------------------------------------

type smtpOutput struct {
	Body apitypes.SmtpSettings
}

type smtpInput struct {
	Body apitypes.SmtpSettingsInput
}

type smtpTestInput struct {
	Body apitypes.SmtpTestRequest
}

type smtpTestOutput struct {
	Body apitypes.NotificationTestResponse
}

func (s *Server) handleAdminGetSmtp(ctx context.Context, _ *struct{}) (*smtpOutput, error) {
	raw, err := s.Store.GetSmtpSettings(ctx)
	if err != nil {
		if errors.Is(err, store.ErrSmtpNotConfigured) {
			// Return a zero-value record so the admin UI can present a
			// blank form rather than treating "not set yet" as an error.
			return &smtpOutput{Body: apitypes.SmtpSettings{Port: 587, StartTLS: true}}, nil
		}
		return nil, internalErr(ctx, "smtp get failed", err)
	}
	return &smtpOutput{Body: raw.SmtpSettings}, nil
}

func (s *Server) handleAdminPutSmtp(ctx context.Context, in *smtpInput) (*smtpOutput, error) {
	u, _ := userFromContext(ctx)
	saved, err := s.Store.UpsertSmtpSettings(ctx, in.Body, u.Email)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &smtpOutput{Body: saved}, nil
}

func (s *Server) handleAdminTestSmtp(ctx context.Context, in *smtpTestInput) (*smtpTestOutput, error) {
	if in.Body.To == "" {
		return nil, huma.Error400BadRequest("'to' is required")
	}
	settings, err := s.Store.GetSmtpSettings(ctx)
	if err != nil {
		if errors.Is(err, store.ErrSmtpNotConfigured) {
			return nil, huma.Error400BadRequest("smtp settings not configured")
		}
		return nil, internalErr(ctx, "smtp get failed", err)
	}
	cfg := map[string]any{
		"host":                 settings.Host,
		"port":                 settings.Port,
		"username":             settings.Username,
		"password":             settings.Password,
		"from":                 settings.FromAddress,
		"to":                   []string{in.Body.To},
		"starttls":             settings.StartTLS,
		"tls":                  settings.TLS,
		"insecure_skip_verify": settings.InsecureSkipVerify,
	}
	dispatchErr := notify.Dispatch(ctx, notify.Channel{
		ID: "smtp-settings", Type: "email", Name: "smtp-test", Config: cfg,
	}, notify.Message{
		Subject:  "mon SMTP test",
		Body:     "If you received this message, the global SMTP settings work.",
		Severity: "info",
	})
	out := &smtpTestOutput{}
	if dispatchErr != nil {
		out.Body.OK = false
		out.Body.Error = dispatchErr.Error()
		return out, nil
	}
	out.Body.OK = true
	return out, nil
}

// --- Quiet hours (admin) ---------------------------------------------------

type quietHoursOutput struct {
	Body apitypes.NotificationSettings
}

type quietHoursInput struct {
	Body apitypes.NotificationSettingsInput
}

func (s *Server) handleAdminGetQuietHours(ctx context.Context, _ *struct{}) (*quietHoursOutput, error) {
	cur, err := s.Store.GetNotificationSettings(ctx)
	if err != nil {
		return nil, internalErr(ctx, "quiet-hours get failed", err)
	}
	return &quietHoursOutput{Body: cur}, nil
}

func (s *Server) handleAdminPutQuietHours(ctx context.Context, in *quietHoursInput) (*quietHoursOutput, error) {
	// Validate the timezone server-side. The migration default is UTC; we
	// accept anything time.LoadLocation can resolve so operators on systems
	// without /usr/share/zoneinfo (distroless) at least get UTC + Local.
	if in.Body.QuietTZ != "" {
		if _, err := time.LoadLocation(in.Body.QuietTZ); err != nil {
			return nil, huma.Error400BadRequest("invalid timezone: " + in.Body.QuietTZ)
		}
	}
	for _, d := range in.Body.QuietDays {
		if d < 0 || d > 6 {
			return nil, huma.Error400BadRequest("quiet_days entries must be in 0..6 (0=Sun)")
		}
	}
	u, _ := userFromContext(ctx)
	saved, err := s.Store.UpsertNotificationSettings(ctx, in.Body, u.Email)
	if err != nil {
		return nil, internalErr(ctx, "quiet-hours save failed", err)
	}
	// Drop the engine's cache so the change takes effect on the next fire
	// rather than after the 60 s TTL.
	if s.Alerts != nil {
		s.Alerts.InvalidateQuietCache()
	}
	return &quietHoursOutput{Body: saved}, nil
}

// --- Agent config (agent fetch + admin CRUD) ------------------------------

type agentConfigFetchInput struct {
	Authorization string `header:"Authorization" required:"true" doc:"Bearer <agent_key>"`
}
type agentConfigFetchOutput struct {
	Body apitypes.AgentConfigResolved
}

func (s *Server) handleAgentConfigFetch(ctx context.Context, in *agentConfigFetchInput) (*agentConfigFetchOutput, error) {
	key, ok := bearer(in.Authorization)
	if !ok {
		return nil, huma.Error401Unauthorized("missing agent key")
	}
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	hostID, err := s.Store.HostIDForAgentKey(ctx, key)
	if err != nil {
		if errors.Is(err, store.ErrAgentKeyInvalid) {
			return nil, huma.Error401Unauthorized("agent key invalid")
		}
		return nil, internalErr(ctx, "auth failed", err)
	}
	resolved, err := s.Store.ResolveAgentConfig(ctx, hostID)
	if err != nil {
		return nil, internalErr(ctx, "resolve failed", err)
	}
	return &agentConfigFetchOutput{Body: resolved}, nil
}

type listAgentConfigsOutput struct {
	Body struct {
		Configs []apitypes.AgentConfigEntry `json:"configs"`
	}
}

func (s *Server) handleListAgentConfigs(ctx context.Context, _ *struct{}) (*listAgentConfigsOutput, error) {
	cs, err := s.Store.ListAgentConfigs(ctx)
	if err != nil {
		return nil, internalErr(ctx, "list failed", err)
	}
	out := &listAgentConfigsOutput{}
	out.Body.Configs = cs
	return out, nil
}

type upsertAgentConfigInput struct {
	Body apitypes.AgentConfigInput
}
type agentConfigEntryOutput struct {
	Body apitypes.AgentConfigEntry
}

func (s *Server) handleUpsertAgentConfig(ctx context.Context, in *upsertAgentConfigInput) (*agentConfigEntryOutput, error) {
	u, _ := userFromContext(ctx)
	c, err := s.Store.UpsertAgentConfig(ctx, in.Body, u.Email)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &agentConfigEntryOutput{Body: c}, nil
}

type agentConfigIDInput struct {
	ID string `path:"id"`
}

func (s *Server) handleDeleteAgentConfig(ctx context.Context, in *agentConfigIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := s.Store.DeleteAgentConfig(ctx, id); err != nil {
		if errors.Is(err, store.ErrAgentConfigNotFound) {
			return nil, huma.Error404NotFound("agent config not found")
		}
		return nil, internalErr(ctx, "delete failed", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type previewAgentConfigInput struct {
	HostID string `path:"host_id"`
}
type previewAgentConfigOutput struct {
	Body apitypes.AgentConfigResolved
}

func (s *Server) handlePreviewAgentConfig(ctx context.Context, in *previewAgentConfigInput) (*previewAgentConfigOutput, error) {
	id, err := uuid.Parse(in.HostID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid host_id")
	}
	resolved, err := s.Store.ResolveAgentConfig(ctx, id)
	if err != nil {
		return nil, internalErr(ctx, "resolve failed", err)
	}
	return &previewAgentConfigOutput{Body: resolved}, nil
}

// --- Admin: server logs ---------------------------------------------------

type adminLogsInput struct {
	Since  time.Time `query:"since"   doc:"earliest entry time (RFC3339)"`
	Until  time.Time `query:"until"   doc:"latest entry time (RFC3339)"`
	Level  string    `query:"level"   enum:"debug,info,warn,error"`
	Q      string    `query:"q"       doc:"substring match on msg, level, or any string attr"`
	HostID string    `query:"host_id" doc:"only entries with attrs.host_id == this id"`
	Limit  int       `query:"limit"   minimum:"1" maximum:"1000"`
	Offset int       `query:"offset"  minimum:"0"`
}

type adminLogsOutput struct {
	Body struct {
		Total   int                `json:"total"   doc:"matching entries before paging"`
		Limit   int                `json:"limit"`
		Offset  int                `json:"offset"`
		Entries []serverlog.Entry  `json:"entries"`
		Seq     uint64             `json:"seq"     doc:"monotonic write counter; gaps mean entries were dropped"`
	}
}

func (s *Server) handleAdminServerLogs(ctx context.Context, in *adminLogsInput) (*adminLogsOutput, error) {
	if s.LogBuffer == nil {
		return nil, huma.Error503ServiceUnavailable("log buffer not initialized")
	}
	filter := serverlog.Filter{
		Since:    in.Since,
		Until:    in.Until,
		MinLevel: serverlog.LevelFromString(in.Level),
		Q:        in.Q,
		HostID:   in.HostID,
	}
	entries, seq := s.LogBuffer.Snapshot(filter)
	limit := in.Limit
	if limit <= 0 {
		limit = 200
	}
	page := serverlog.Page(entries, in.Offset, limit)
	out := &adminLogsOutput{}
	out.Body.Total = len(entries)
	out.Body.Limit = limit
	out.Body.Offset = in.Offset
	out.Body.Entries = page
	out.Body.Seq = seq
	return out, nil
}

// --- Admin: raw ingest payloads -------------------------------------------

type adminListIngestsInput struct {
	HostID string `query:"host_id" doc:"only entries from this host"`
	Limit  int    `query:"limit"   minimum:"1" maximum:"500"`
}

type adminListIngestsOutput struct {
	Body struct {
		Entries []ingestSummary `json:"entries"`
	}
}

type ingestSummary struct {
	Idx       int       `json:"idx"        doc:"0-based newest-first; pass to /v1/admin/ingests/{idx}"`
	Time      time.Time `json:"time"`
	HostID    string    `json:"host_id"`
	Hostname  string    `json:"hostname,omitempty"`
	SizeBytes int       `json:"size_bytes" doc:"original payload size; truncated copies will show < SizeBytes when fetched"`
}

func (s *Server) handleAdminListIngests(ctx context.Context, in *adminListIngestsInput) (*adminListIngestsOutput, error) {
	if s.IngestBuffer == nil {
		return nil, huma.Error503ServiceUnavailable("ingest buffer not initialized")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	entries := s.IngestBuffer.Snapshot(in.HostID, limit)
	out := &adminListIngestsOutput{}
	for i, e := range entries {
		out.Body.Entries = append(out.Body.Entries, ingestSummary{
			Idx: i, Time: e.Time, HostID: e.HostID, Hostname: e.Hostname, SizeBytes: e.SizeBytes,
		})
	}
	return out, nil
}

type adminGetIngestInput struct {
	Idx    int    `path:"idx"`
	HostID string `query:"host_id"`
}

type adminGetIngestOutput struct {
	Body struct {
		Time      time.Time       `json:"time"`
		HostID    string          `json:"host_id"`
		Hostname  string          `json:"hostname,omitempty"`
		SizeBytes int             `json:"size_bytes"`
		Truncated bool            `json:"truncated"`
		Payload   json.RawMessage `json:"payload"`
	}
}

func (s *Server) handleAdminGetIngest(ctx context.Context, in *adminGetIngestInput) (*adminGetIngestOutput, error) {
	if s.IngestBuffer == nil {
		return nil, huma.Error503ServiceUnavailable("ingest buffer not initialized")
	}
	e, ok := s.IngestBuffer.Get(in.HostID, in.Idx)
	if !ok {
		return nil, huma.Error404NotFound("ingest entry not found")
	}
	out := &adminGetIngestOutput{}
	out.Body.Time = e.Time
	out.Body.HostID = e.HostID
	out.Body.Hostname = e.Hostname
	out.Body.SizeBytes = e.SizeBytes
	out.Body.Truncated = len(e.Payload) < e.SizeBytes
	out.Body.Payload = json.RawMessage(e.Payload)
	return out, nil
}

// --- Admin: password policy ------------------------------------------------

type passwordPolicyOutput struct {
	Body apitypes.PasswordPolicy
}

func (s *Server) handleGetPasswordPolicy(ctx context.Context, _ *struct{}) (*passwordPolicyOutput, error) {
	p, err := s.Store.GetPasswordPolicy(ctx)
	if err != nil {
		return nil, internalErr(ctx, "get policy failed", err)
	}
	return &passwordPolicyOutput{Body: p}, nil
}

type setPolicyInput struct {
	Body apitypes.PasswordPolicy
}

func (s *Server) handleSetPasswordPolicy(ctx context.Context, in *setPolicyInput) (*passwordPolicyOutput, error) {
	actor, _ := userFromContext(ctx)
	if err := s.Store.SetPasswordPolicy(ctx, in.Body, actor.Email); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	p, _ := s.Store.GetPasswordPolicy(ctx)
	return &passwordPolicyOutput{Body: p}, nil
}
