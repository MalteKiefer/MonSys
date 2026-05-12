package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	httppprof "net/http/pprof"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/MalteKiefer/MonSys/internal/server/agentupdate"
	"github.com/MalteKiefer/MonSys/internal/server/alerts"
	"github.com/MalteKiefer/MonSys/internal/server/docs"
	"github.com/MalteKiefer/MonSys/internal/server/ingestlog"
	"github.com/MalteKiefer/MonSys/internal/server/notify"
	"github.com/MalteKiefer/MonSys/internal/server/serverlog"
	"github.com/MalteKiefer/MonSys/internal/server/spa"
	"github.com/MalteKiefer/MonSys/internal/server/store"
	"github.com/MalteKiefer/MonSys/internal/server/telemetry"
	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
	"github.com/MalteKiefer/MonSys/internal/shared/version"
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
	// AgentUpdate publishes the metadata mon-agent's self-updater consults
	// (latest version + per-arch URL + sha256). Optional — endpoint returns
	// 404 when nil.
	AgentUpdate *agentupdate.Resolver

	// audit 2026-05-12 F-9: process-local cache of UserCompliesWithPolicy
	// results. Used by requireMethodCompliance to fail-closed-when-possible:
	// on a transient policy lookup error we reuse the most recent answer
	// (even past its TTL) rather than fail-open. New entries get a 60 s TTL.
	policyComplianceCache   map[uuid.UUID]policyComplianceCacheEntry
	policyComplianceCacheMu sync.RWMutex

	// audit 2026-05-12 F-11: per-user throttle for /v1/auth/email/request.
	// Maps user ID -> time of most recent request. Refuses with 429 if the
	// caller already requested an email change in the last 60 s. Entries
	// older than 1 h are purged on access so the map stays bounded.
	emailChangeThrottle   map[uuid.UUID]time.Time
	emailChangeThrottleMu sync.Mutex
}

// audit 2026-05-12 F-9: cache entry. complies+grace are the most recent
// successful UserCompliesWithPolicy return; expiresAt marks the TTL so a
// success path knows when to re-query. On error we ignore expiresAt and
// reuse the entry whatever its age.
type policyComplianceCacheEntry struct {
	complies  bool
	grace     *time.Time
	expiresAt time.Time
}

func New(s *store.Store) *Server {
	r := chi.NewRouter()
	srv := &Server{
		Store:                 s,
		Router:                r,
		policyComplianceCache: make(map[uuid.UUID]policyComplianceCacheEntry),
		emailChangeThrottle:   make(map[uuid.UUID]time.Time),
	}

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
	// AUDIT-702: per-agent-key (host) ingest quota. The per-IP limiter above
	// caps request volume from any one source IP, but a single host behind a
	// shared egress IP can still burn the quota for everyone else. This
	// middleware adds a 600 req/min ceiling keyed on a SHA-256 of the
	// Authorization header so each agent key is throttled independently.
	r.Use(ingestQuotaPerHost)
	// AUDIT-066: openapi/docs are session-protected.
	r.Use(srv.requireSessionForDocs)
	r.Use(middleware.Timeout(30 * time.Second))

	// AUDIT-201/202/207/208/209: bring the OpenAPI surface up to spec —
	// security schemes, root-level security, license, real server URL.
	// See internal/server/api/openapi_config.go for the policy.
	cfg := openAPIConfig("MonSys", version.Version)
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
		// AUDIT-502: include `preload` so the response is eligible for the
		// HSTS preload list. Operators who do not want preload can strip the
		// directive at the reverse proxy.
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
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

// ingestQuotaPerHost applies a per-agent-key rate limit (600 req/min) on the
// /v1/ingest path on top of the per-IP cap. The httprate KeyFunc returns the
// SHA-256 hex of the Authorization header so each agent throttles
// independently — agents behind a shared NAT egress no longer burn each
// other's per-IP quota. Other paths bypass this middleware entirely.
// AUDIT-702.
func ingestQuotaPerHost(next http.Handler) http.Handler {
	limiter := httprate.Limit(
		600,
		time.Minute,
		httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
			if r.URL.Path != "/v1/ingest" {
				return "", nil
			}
			tok, _ := bearer(r.Header.Get("Authorization"))
			if tok == "" {
				return "", nil
			}
			h := sha256.Sum256([]byte(tok))
			return "agent:" + hex.EncodeToString(h[:]), nil
		}),
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/ingest" {
			next.ServeHTTP(w, r)
			return
		}
		limiter(next).ServeHTTP(w, r)
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
		"/v1/auth/login":                 {},
		"/v1/auth/2fa/challenge":         {},
		"/v1/auth/consume-reset":         {},
		"/v1/agents/register":            {},
		"/v1/auth/webauthn/login/begin":  {},
		"/v1/auth/webauthn/login/finish": {},
		"/v1/auth/email/confirm":         {},
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

// docsCookieName is the HttpOnly cookie that mirrors the bearer session
// token so the docs viewer is reachable via top-level browser navigation
// (where Authorization headers cannot be set by the SPA). Scoped to "/"
// because the browser must send it with /docs and /openapi.* requests
// initiated by Scalar; the requireSessionForDocs middleware is the only
// reader and ignores it for /v1/* so CSRF posture on the API stays
// header-based.
const docsCookieName = "mon_docs_session"

// docsCookieSetHeader serializes a Set-Cookie value pinning the docs
// session cookie to the given token and lifetime. Secure + HttpOnly +
// SameSite=Strict match the threat model: cookie is for the same origin
// only, never readable from JS, never sent on cross-site navigations.
func docsCookieSetHeader(token string, ttl time.Duration) string {
	c := &http.Cookie{
		Name:     docsCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	}
	return c.String()
}

// docsCookieClearHeader is the inverse: a Max-Age=-1 Set-Cookie used by
// logout so the cookie is removed in lockstep with the bearer revocation.
func docsCookieClearHeader() string {
	c := &http.Cookie{
		Name:     docsCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	}
	return c.String()
}

// docsCookieTTL is the remaining lifetime the cookie should advertise.
// Mirrors sessionExpiresAt's policy lookup so cookie + bearer expire in
// sync. Falls back to 12h on policy load failure (matches sessionExpiresAt).
func (s *Server) docsCookieTTL(ctx context.Context) time.Duration {
	p, err := s.Store.GetSecurityPolicy(ctx)
	if err != nil || p.MaxSessionHours <= 0 {
		return 12 * time.Hour
	}
	return time.Duration(p.MaxSessionHours) * time.Hour
}

// requireSessionForDocs hides /docs and /openapi.* behind a valid session
// token. Without it any unauthenticated caller could enumerate the full API
// surface from a public deployment. AUDIT-066.
//
// Accepts the session token via either the Authorization bearer header
// (used by API tooling and the SPA's fetch() calls) or the docs session
// cookie (used by top-level browser navigation, where the SPA cannot
// attach headers). Both sources hit the same Store.ValidateSession path
// so the cookie inherits all the same revocation + idle-timeout rules.
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
			if c, err := r.Cookie(docsCookieName); err == nil && c.Value != "" {
				tok, ok = c.Value, true
			}
		}
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

// sessionExpiresAt computes the wall-clock expiry the API surface advertises
// in LoginResponse.ExpiresAt. The actual TTL applied to the session row is
// enforced inside Store.IssueSession via effectiveSessionTTL; we recompute
// it here so the timestamp the client sees matches what the store wrote.
// On policy load failure we fall back to 12h — better a slightly stale
// hint than a refused login.
func (s *Server) sessionExpiresAt(ctx context.Context) time.Time {
	p, err := s.Store.GetSecurityPolicy(ctx)
	if err != nil || p.MaxSessionHours <= 0 {
		return time.Now().Add(12 * time.Hour).UTC()
	}
	return time.Now().Add(time.Duration(p.MaxSessionHours) * time.Hour).UTC()
}

// currentUserWithSecurity builds the CurrentUser response with the
// security-policy-derived fields (PasskeyCount, MustEnroll, GraceUntil)
// populated. Used by every endpoint that returns CurrentUser so the UI
// can render "you must enroll a passkey" banners consistently.
func (s *Server) currentUserWithSecurity(ctx context.Context, u store.User) apitypes.CurrentUser {
	lang := u.Language
	if lang == "" {
		// Defensive fallback: older code paths or migration races might
		// leave Language blank. Treat that the same as the DB default so
		// the SPA never sees an empty value through the API.
		lang = "auto"
	}
	out := apitypes.CurrentUser{
		ID:         u.ID.String(),
		Email:      u.Email,
		Role:       u.Role,
		TOTPActive: u.TOTPActive,
		Language:   lang,
	}
	pks, _ := s.Store.ListPasskeys(ctx, u.ID)
	out.PasskeyCount = len(pks)
	complies, grace, _ := s.Store.UserCompliesWithPolicy(ctx, u.ID)
	out.MustEnroll = !complies
	out.GraceUntil = grace
	// Avatar meta is best-effort: a DB hiccup here shouldn't block /me.
	if hasAvatar, updatedAt, err := s.Store.GetAvatarMeta(ctx, u.ID); err == nil {
		out.HasAvatar = hasAvatar
		out.AvatarUpdatedAt = updatedAt
	}
	return out
}

// Handler returns the public HTTP handler with OTel HTTP instrumentation
// wrapped around the chi router. otelhttp must wrap AFTER all middleware
// has been installed on the router (Use() before route registration is
// chi's contract), so we wrap once here at serve time. Each request gets
// a server span with http.method, http.route, http.status_code, plus the
// usual otelhttp trace/parent linking.
func (s *Server) Handler() http.Handler {
	return otelhttp.NewHandler(s.Router, "mon-server",
		// span name is server-side; format as "GET /v1/hosts" rather than
		// the raw path, which is noisier in the trace viewer.
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)
}

func (s *Server) registerRoutes() {
	// Middleware bundles. See per-stack docs at first use below.
	//
	// openProtected: session-required, but NO compliance gate. Endpoints that
	// a non-compliant user must still be able to reach in order to BECOME
	// compliant (enroll a passkey/TOTP, change password, view /me, log out)
	// use this so the force-mode middleware doesn't lock them out of the
	// remediation surface.
	openProtected := huma.Middlewares{s.requireUser}
	// Read APIs require a user session AND compliance with the active
	// force-mode (passkey/2FA enrollment, when configured).
	protected := huma.Middlewares{s.requireUser, s.requireMethodCompliance}
	// Operator surfaces (user management, agent config, notification rules, …)
	// require an admin in addition to a valid session and method compliance.
	adminOnly := huma.Middlewares{s.requireUser, s.requireMethodCompliance, s.requireAdmin}

	s.registerHealthRoutes()
	s.registerAgentLifecycleRoutes()
	s.registerPublicRoutes()
	s.registerAuthRoutes(openProtected)
	s.registerHostRoutes(protected)
	s.registerNotificationRoutes(protected, adminOnly)
	s.registerMonitorRoutes(protected)
	s.registerSelfServiceAuthRoutes(openProtected, protected)
	s.registerAdminRoutes(adminOnly)

	// Interactive OpenAPI viewer (Scalar, vendored into the binary).
	// huma's built-in /docs renderer is disabled by openAPIConfig — we
	// serve the HTML shell and the JS bundle here so the supply chain is
	// explicit and air-gapped deployments work without an outbound CDN
	// fetch. Both routes are session-gated by requireSessionForDocs
	// (AUDIT-066), and registered BEFORE the SPA catch-all so the SPA's
	// index.html does not shadow /docs and /docs/scalar.js.
	s.Router.Handle("/docs", docs.IndexHandler())
	s.Router.Handle("/docs/scalar.js", docs.AssetHandler())

	// SPA mount: anything not claimed by /v1, /healthz, /readyz, /docs is
	// served from the embedded React build. Registered last so huma's API
	// routes win.
	s.Router.Handle("/*", spa.Handler())
}

// registerHealthRoutes wires the unauthenticated liveness/readiness probes
// plus the admin-gated Prometheus scrape endpoint.
func (s *Server) registerHealthRoutes() {
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

	// /metrics — Prometheus scrape endpoint. Admin-gated because the
	// runtime metrics (go_goroutines, process_resident_memory_bytes, ...)
	// plus our domain metrics (mon_login_failures_total{reason},
	// mon_ingest_requests_total{host_id}) are operationally sensitive.
	// Scrape from inside the network with a session bearer token belonging
	// to an admin user. AUDIT-066-style: never publicly exposed.
	s.Router.Get("/metrics", s.requireAdminBearer(telemetry.PromHandler()))

	// /debug/pprof/* — runtime profiling endpoints. Admin-gated identically
	// to /metrics: the surface is operationally sensitive (heap dumps leak
	// internal layout, CPU profiles can extend over arbitrary windows) so
	// we never expose it publicly. Capture a 30s CPU profile with
	//   curl -H "Authorization: Bearer $TOKEN" \
	//        https://host/debug/pprof/profile?seconds=30 > cpu.pprof
	// See docs/OPERATIONS.md "Performance investigation".
	s.Router.Get("/debug/pprof/", s.requireAdminBearer(http.HandlerFunc(httppprof.Index)))
	s.Router.Get("/debug/pprof/cmdline", s.requireAdminBearer(http.HandlerFunc(httppprof.Cmdline)))
	s.Router.Get("/debug/pprof/profile", s.requireAdminBearer(http.HandlerFunc(httppprof.Profile)))
	s.Router.Get("/debug/pprof/symbol", s.requireAdminBearer(http.HandlerFunc(httppprof.Symbol)))
	s.Router.Post("/debug/pprof/symbol", s.requireAdminBearer(http.HandlerFunc(httppprof.Symbol)))
	s.Router.Get("/debug/pprof/trace", s.requireAdminBearer(http.HandlerFunc(httppprof.Trace)))
	for _, p := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
		s.Router.Get("/debug/pprof/"+p, s.requireAdminBearer(httppprof.Handler(p)))
	}
}

// requireAdminBearer is a chi-style auth gate for the /metrics endpoint.
// It mirrors the huma-side requireAdmin/requireUser pipeline but lives on
// the raw chi router because /metrics is not registered through humachi.
// Missing/invalid bearer -> 404 (matches requireSessionForDocs so we
// don't leak the endpoint's existence to unauth callers); valid session
// but non-admin -> 403.
func (s *Server) requireAdminBearer(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Store == nil {
			http.NotFound(w, r)
			return
		}
		tok, ok := bearer(r.Header.Get("Authorization"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		u, err := s.Store.ValidateSession(r.Context(), tok)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if u.Role != "admin" {
			http.Error(w, "admin role required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// registerAgentLifecycleRoutes wires agent registration, ingest, and self-update
// metadata. These endpoints authenticate with agent-issued bearer tokens rather
// than web sessions.
func (s *Server) registerAgentLifecycleRoutes() {
	huma.Register(s.API, huma.Operation{
		OperationID: "agent-register",
		Method:      http.MethodPost,
		Path:        "/v1/agents/register",
		Summary:     "Register a new agent",
		Description: "Trade a one-time bootstrap token (Authorization: Bearer …) for a per-host agent_key.",
		Tags:        []string{"agents"},
		Security:    secAgentRegister,
	}, s.handleAgentRegister)

	huma.Register(s.API, huma.Operation{
		OperationID: "ingest",
		Method:      http.MethodPost,
		Path:        "/v1/ingest",
		Summary:     "Ingest metrics + inventory",
		Description: "Agents push samples here. Auth: Authorization: Bearer <agent_key>.",
		Tags:        []string{"ingest"},
		Security:    secIngest,
	}, s.handleIngest)

	huma.Register(s.API, huma.Operation{
		OperationID: "agent-latest-version",
		Method:      http.MethodGet,
		Path:        "/v1/agents/latest-version",
		Summary:     "Latest mon-agent build metadata for self-update",
		Description: "Public. Returns the version, per-arch download URL, and SHA256 the agent's auto-updater verifies the binary against. Sourced from operator env (static mode) or the GitHub Releases API (default).",
		Tags:        []string{"agents"},
		Security:    secNoAuth,
	}, s.handleAgentLatestVersion)

	// Agent-config: agents fetch their resolved config (auth via agent_key,
	// not web user). Admin CRUD lives separately under /v1/admin/agent-config
	// and is registered in registerAdminRoutes.
	huma.Register(s.API, huma.Operation{
		OperationID: "agent-config-fetch",
		Method:      http.MethodGet,
		Path:        "/v1/agent/config",
		Summary:     "Agent fetches its resolved config (auth: Bearer agent_key)",
		Tags:        []string{"agents"},
	}, s.handleAgentConfigFetch)
}

// registerPublicRoutes wires routes that must be reachable with no auth at all
// (curl|bash installer, RFC 9116 security.txt). The installer's ?t=… query
// param is the only credential and is single-use.
func (s *Server) registerPublicRoutes() {
	s.Router.Get("/v1/agents/install.sh", s.handleInstallScript)
	s.Router.Get("/v1/agents/install-qr", s.handleInstallQR)
	s.Router.Get("/.well-known/security.txt", s.handleSecurityTxt)
}

// registerAuthRoutes wires login, the post-login session surface (/me, logout,
// config), and the 2FA challenge step. Login itself is unauthenticated; the
// /me-style endpoints accept session-only (no compliance gate) so a user under
// a strict force-mode can still reach the remediation surface.
func (s *Server) registerAuthRoutes(openProtected huma.Middlewares) {
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
		Middlewares: openProtected,
	}, s.handleLogout)

	huma.Register(s.API, huma.Operation{
		OperationID: "auth-me",
		Method:      http.MethodGet,
		Path:        "/v1/auth/me",
		Summary:     "Return the authenticated user",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleMe)

	// Lightweight server-wide auth/notification readiness flags. Any logged-in
	// user can read this so non-admins can be told *before* they create an
	// email channel that SMTP isn't configured yet.
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-config",
		Method:      http.MethodGet,
		Path:        "/v1/auth/config",
		Summary:     "Server-wide readiness flags (sso, smtp) visible to any user",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleAuthConfig)

	// 2FA challenge after password login (unauthenticated; protected by the
	// short-lived challenge token).
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-2fa-challenge",
		Method:      http.MethodPost,
		Path:        "/v1/auth/2fa/challenge",
		Summary:     "Complete login with a TOTP code",
		Tags:        []string{"auth"},
	}, s.handleTOTPChallenge)
}

// registerHostRoutes wires the host inventory + per-host metrics + groups +
// packages + security read surface. All endpoints require an authenticated
// (and compliant) user; group mutations additionally require admin.
func (s *Server) registerHostRoutes(protected huma.Middlewares) {
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

	s.registerHostGroupRoutes(protected)
	s.registerHostMetricsRoutes(protected)
}

// registerHostGroupRoutes wires host group CRUD + membership management. Group
// mutations are admin-only (the bundle inlined per route, not the shared
// `protected` bundle the helper receives) because they affect every host in
// the group.
func (s *Server) registerHostGroupRoutes(protected huma.Middlewares) {
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
}

// registerHostMetricsRoutes wires per-host time-series read endpoints
// (system/disk/network metrics), package listings, and the security snapshot.
func (s *Server) registerHostMetricsRoutes(protected huma.Middlewares) {
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
}

// registerNotificationRoutes wires the channel CRUD (any compliant user owns
// their own channels) and rule CRUD + alert history (rule mutations are
// admin-only, see comment block at the rule-list operation).
func (s *Server) registerNotificationRoutes(protected, adminOnly huma.Middlewares) {
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

	s.registerNotificationRuleRoutes(protected, adminOnly)
}

// registerMonitorRoutes wires the active-monitor CRUD + results read endpoints.
func (s *Server) registerMonitorRoutes(protected huma.Middlewares) {
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
}

// registerNotificationRuleRoutes wires rule CRUD + alert history.
// Rule CRUD is admin-only: operators compose rules that target the channels
// users own. notification_rules has no owner column, so a non-admin who knew
// (or guessed) another user's channel UUID could otherwise POST a rule that
// fired on it.
func (s *Server) registerNotificationRuleRoutes(protected, adminOnly huma.Middlewares) {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-rules",
		Method:      http.MethodGet,
		Path:        "/v1/notifications/rules",
		Summary:     "List notification rules",
		Tags:        []string{"notifications"},
		Middlewares: adminOnly,
	}, s.handleListRules)
	huma.Register(s.API, huma.Operation{
		OperationID: "create-rule",
		Method:      http.MethodPost,
		Path:        "/v1/notifications/rules",
		Summary:     "Create a notification rule",
		Tags:        []string{"notifications"},
		Middlewares: adminOnly,
	}, s.handleCreateRule)
	huma.Register(s.API, huma.Operation{
		OperationID: "notif-rule-group-create",
		Method:      http.MethodPost,
		Path:        "/v1/notifications/rules/batch",
		Summary:     "Create N rules sharing one group_id",
		Tags:        []string{"notifications"},
		Middlewares: adminOnly,
	}, s.handleCreateRuleGroup)
	huma.Register(s.API, huma.Operation{
		OperationID: "update-rule",
		Method:      http.MethodPut,
		Path:        "/v1/notifications/rules/{id}",
		Summary:     "Replace a notification rule",
		Tags:        []string{"notifications"},
		Middlewares: adminOnly,
	}, s.handleUpdateRule)
	huma.Register(s.API, huma.Operation{
		OperationID: "delete-rule",
		Method:      http.MethodDelete,
		Path:        "/v1/notifications/rules/{id}",
		Summary:     "Delete a notification rule",
		Tags:        []string{"notifications"},
		Middlewares: adminOnly,
	}, s.handleDeleteRule)
	huma.Register(s.API, huma.Operation{
		OperationID: "alert-history",
		Method:      http.MethodGet,
		Path:        "/v1/notifications/alerts",
		Summary:     "Recent alert history",
		Tags:        []string{"notifications"},
		Middlewares: protected,
	}, s.handleAlertHistory)
}

// registerSelfServiceAuthRoutes wires the user's self-management surface:
// change password/email, TOTP setup/verify/disable, WebAuthn/passkey
// ceremonies, public reset consume, avatar upload/delete, UI language, and the
// verified email-change flow.
//
// openProtected vs protected: endpoints that a non-compliant user must reach
// IN ORDER to become compliant (enroll TOTP/passkey, change password, view
// /me) use openProtected. Pure profile customisation (avatar, language) sits
// under protected per F-10 — no business mutating those past the grace window.
func (s *Server) registerSelfServiceAuthRoutes(openProtected, protected huma.Middlewares) {
	s.registerSelfServiceProfileRoutes(openProtected)
	s.registerWebAuthnRoutes(openProtected)
	s.registerSelfServiceExtrasRoutes(protected)
}

// registerSelfServiceProfileRoutes wires the credential-management surface
// (change password/email, TOTP setup/verify/disable). All sit under
// openProtected — these are the endpoints a non-compliant user reaches in
// order to become compliant under a strict force-mode.
func (s *Server) registerSelfServiceProfileRoutes(openProtected huma.Middlewares) {
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-change-password",
		Method:      http.MethodPost,
		Path:        "/v1/auth/change-password",
		Summary:     "Change own password",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleChangePassword)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-change-email",
		Method:      http.MethodPost,
		Path:        "/v1/auth/change-email",
		Summary:     "Change own email",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleChangeEmail)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-2fa-setup",
		Method:      http.MethodPost,
		Path:        "/v1/auth/2fa/setup",
		Summary:     "Begin TOTP setup; returns secret + QR + backup codes",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleTOTPSetup)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-2fa-verify",
		Method:      http.MethodPost,
		Path:        "/v1/auth/2fa/verify",
		Summary:     "Verify a TOTP code; activates pending TOTP if first-time",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleTOTPVerify)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-2fa-disable",
		Method:      http.MethodPost,
		Path:        "/v1/auth/2fa/disable",
		Summary:     "Disable own TOTP (requires password)",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleTOTPDisable)
}

// registerWebAuthnRoutes wires WebAuthn/passkey ceremonies, passkey
// self-management, and the public consume-reset token endpoint. Login halves
// are unauthenticated (no session yet); register halves and management endpoints
// are session-required but exempt from the force-mode compliance gate so a user
// can enroll a passkey under a strict policy.
func (s *Server) registerWebAuthnRoutes(openProtected huma.Middlewares) {
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-webauthn-register-begin",
		Method:      http.MethodPost,
		Path:        "/v1/auth/webauthn/register/begin",
		Summary:     "Begin a passkey registration ceremony",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleWebAuthnRegisterBegin)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-webauthn-register-finish",
		Method:      http.MethodPost,
		Path:        "/v1/auth/webauthn/register/finish",
		Summary:     "Finish a passkey registration ceremony",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleWebAuthnRegisterFinish)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-webauthn-login-begin",
		Method:      http.MethodPost,
		Path:        "/v1/auth/webauthn/login/begin",
		Summary:     "Begin a discoverable-credential passkey login",
		Tags:        []string{"auth"},
	}, s.handleWebAuthnLoginBegin)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-webauthn-login-finish",
		Method:      http.MethodPost,
		Path:        "/v1/auth/webauthn/login/finish",
		Summary:     "Finish a discoverable-credential passkey login; returns session",
		Tags:        []string{"auth"},
	}, s.handleWebAuthnLoginFinish)

	// Passkey self-management (list/rename/delete).
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-list-passkeys",
		Method:      http.MethodGet,
		Path:        "/v1/auth/me/passkeys",
		Summary:     "List the caller's registered passkeys",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleListPasskeys)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-rename-passkey",
		Method:      http.MethodPut,
		Path:        "/v1/auth/me/passkeys/{id}",
		Summary:     "Rename one of the caller's passkeys",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleRenamePasskey)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-delete-passkey",
		Method:      http.MethodDelete,
		Path:        "/v1/auth/me/passkeys/{id}",
		Summary:     "Delete one of the caller's passkeys",
		Tags:        []string{"auth"},
		Middlewares: openProtected,
	}, s.handleDeletePasskey)

	// Public reset endpoint — consumed via the link emailed by an admin
	// invite. The token in the body is the only credential required.
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-consume-reset",
		Method:      http.MethodPost,
		Path:        "/v1/auth/consume-reset",
		Summary:     "Set a new password via an admin-issued reset/invite token",
		Tags:        []string{"auth"},
	}, s.handleConsumeReset)
}

// registerSelfServiceExtrasRoutes wires pure profile-customisation endpoints
// (avatar upload/delete, UI language) and the verified email-change flow. All
// sit under `protected` per F-10 — no business mutating these past the grace
// window. The public consume-token step of the email-change flow is wired
// without auth (the token IS the credential, rate-limited by authPaths).
//
// The avatar GET is wired directly on chi to stream raw image bytes (huma's
// content negotiation always wraps replies in a typed envelope).
func (s *Server) registerSelfServiceExtrasRoutes(protected huma.Middlewares) {
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-me-avatar-upload",
		Method:      http.MethodPost,
		Path:        "/v1/auth/me/avatar",
		Summary:     "Upload/replace the caller's avatar (base64-encoded image)",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleSetAvatar)
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-me-avatar-delete",
		Method:      http.MethodDelete,
		Path:        "/v1/auth/me/avatar",
		Summary:     "Delete the caller's avatar",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleDeleteAvatar)

	// Self-service UI language preference. Under `protected` per F-10 —
	// the TopBar switcher shouldn't let a non-compliant user customise
	// the UI past the grace window.
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-me-set-language",
		Method:      http.MethodPut,
		Path:        "/v1/auth/me/language",
		Summary:     "Set the caller's UI language preference",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleSetLanguage)

	// Avatar fetch by user id. Wired on chi directly so we can stream raw
	// image bytes (huma's content negotiation always wraps replies in a
	// typed envelope). The handler does its own bearer-token validation
	// because no huma middleware is in the path.
	s.Router.Get("/v1/users/{id}/avatar", s.handleGetUserAvatar)

	// Verified email-change flow.
	// Step 1 — authenticated user posts the new address; we mail a token to
	// the NEW address proving they control it.
	// Under `protected` per F-10: an attacker who stole a session of a
	// non-compliant user inside the grace window could otherwise harvest
	// the email-change token by redirecting to an address they control.
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-me-email-request",
		Method:      http.MethodPost,
		Path:        "/v1/auth/me/email/request",
		Summary:     "Request an email change; sends a confirmation link to the new address",
		Tags:        []string{"auth"},
		Middlewares: protected,
	}, s.handleRequestEmailChange)
	// Step 2 — public consume of the token. No auth: the user may already
	// be logged out, and the token itself is the credential. Rate-limited
	// by inclusion in authPaths.
	huma.Register(s.API, huma.Operation{
		OperationID: "auth-email-confirm",
		Method:      http.MethodPost,
		Path:        "/v1/auth/email/confirm",
		Summary:     "Confirm an email change by presenting the token sent to the new address",
		Tags:        []string{"auth"},
	}, s.handleConfirmEmailChange)
}

// registerAdminRoutes wires every operator-facing endpoint. All require an
// authenticated, compliant admin session. Split across three helpers by area
// of operation: user management, platform settings (SMTP/quiet-hours/agent-
// config/server logs/ingests), and security/governance (password + security
// policy, enrollment management, audit log).
func (s *Server) registerAdminRoutes(adminOnly huma.Middlewares) {
	s.registerAdminUserRoutes(adminOnly)
	s.registerAdminPlatformRoutes(adminOnly)
	s.registerAdminSecurityRoutes(adminOnly)
}

// registerAdminUserRoutes wires per-user admin actions: list/create/delete,
// lock/unlock, password + 2FA reset, and force session revocation.
func (s *Server) registerAdminUserRoutes(adminOnly huma.Middlewares) {
	// Admin: user management
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
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-revoke-user-sessions",
		Method:      http.MethodPost,
		Path:        "/v1/admin/users/{id}/revoke-sessions",
		Summary:     "Revoke every active web session for a user",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminRevokeUserSessions)
}

// registerAdminPlatformRoutes wires platform-wide singletons + read surfaces:
// outbound SMTP, global quiet hours, agent-config CRUD, server logs, and the
// raw ingest debug viewer.
func (s *Server) registerAdminPlatformRoutes(adminOnly huma.Middlewares) {
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

	// Admin: agent-config CRUD. The agent-side fetch endpoint
	// (/v1/agent/config) is wired in registerAgentLifecycleRoutes — it
	// authenticates with the agent_key, not a web session.
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
}

// registerAdminSecurityRoutes wires the security/governance admin surface:
// password policy, security policy (force-mode + session caps), global session
// revocation, agent self-enrollment management, and the audit log read.
func (s *Server) registerAdminSecurityRoutes(adminOnly huma.Middlewares) {
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

	// Admin: security policy (force-mode + session/idle caps)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-get-security-policy",
		Method:      http.MethodGet,
		Path:        "/v1/admin/security/policy",
		Summary:     "Get the active security policy (force-mode, session caps)",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleGetSecurityPolicy)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-set-security-policy",
		Method:      http.MethodPut,
		Path:        "/v1/admin/security/policy",
		Summary:     "Replace the security policy",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleSetSecurityPolicy)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-revoke-all-sessions",
		Method:      http.MethodPost,
		Path:        "/v1/admin/security/revoke-all-sessions",
		Summary:     "Revoke every active session except the caller's",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleRevokeAllSessions)

	// Admin: agent self-enrollments. The plaintext token is surfaced on
	// POST only; subsequent GETs return metadata. The companion installer
	// is rendered at the public /v1/agents/install.sh endpoint above.
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-create-agent-enrollment",
		Method:      http.MethodPost,
		Path:        "/v1/admin/agents/enrollments",
		Summary:     "Create a one-shot agent enrollment (returns plaintext token + install URL)",
		Tags:        []string{"agents"},
		Middlewares: adminOnly,
	}, s.handleCreateEnrollment)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-list-agent-enrollments",
		Method:      http.MethodGet,
		Path:        "/v1/admin/agents/enrollments",
		Summary:     "List agent enrollments created in the last 24h",
		Tags:        []string{"agents"},
		Middlewares: adminOnly,
	}, s.handleListEnrollments)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-get-agent-enrollment",
		Method:      http.MethodGet,
		Path:        "/v1/admin/agents/enrollments/{id}",
		Summary:     "Get a single agent enrollment",
		Tags:        []string{"agents"},
		Middlewares: adminOnly,
	}, s.handleGetEnrollment)
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-revoke-agent-enrollment",
		Method:      http.MethodDelete,
		Path:        "/v1/admin/agents/enrollments/{id}",
		Summary:     "Revoke an unused agent enrollment",
		Tags:        []string{"agents"},
		Middlewares: adminOnly,
	}, s.handleRevokeEnrollment)

	// Admin: audit log
	huma.Register(s.API, huma.Operation{
		OperationID: "admin-list-audit",
		Method:      http.MethodGet,
		Path:        "/v1/admin/audit",
		Summary:     "List audit log entries (admin actions)",
		Tags:        []string{"admin"},
		Middlewares: adminOnly,
	}, s.handleAdminListAudit)
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

type agentLatestInput struct {
	Fresh bool `query:"fresh" doc:"if true, force the resolver to bypass its cache and re-fetch the upstream manifest. Rate-limited server-side; safe to call after a SHA mismatch."`
}

type agentLatestOutput struct {
	Body agentupdate.Manifest
}

func (s *Server) handleAgentLatestVersion(ctx context.Context, in *agentLatestInput) (*agentLatestOutput, error) {
	if s.AgentUpdate == nil {
		return nil, huma.Error404NotFound("agent update resolver not configured")
	}
	m, err := s.AgentUpdate.Latest(ctx, in.Fresh)
	if err != nil {
		slog.Warn("agent latest-version: resolver failed", "err", err, "fresh", in.Fresh)
		return nil, huma.Error503ServiceUnavailable("agent update resolver unavailable")
	}
	return &agentLatestOutput{Body: *m}, nil
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

	// Observability: count accepted ingests per host so operators can see
	// who's pushing what frequency. Recorded after the successful persist
	// so we don't double-count retried failures.
	MetricIngestAccepted(hostID.String())

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
		HostID  string                `json:"host_id"`
		From    time.Time             `json:"from"`
		To      time.Time             `json:"to"`
		Devices []string              `json:"devices"`
		Samples []apitypes.DiskSample `json:"samples"`
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
	s.audit(ctx, "rule.create", r.ID, r.Name)
	return &ruleOutput{Body: r}, nil
}

type ruleGroupInput struct {
	Body apitypes.NotificationRuleGroupInput
}
type ruleGroupOutput struct {
	Body apitypes.NotificationRuleGroupResponse
}

// handleCreateRuleGroup expands one NotificationRuleGroupInput into N
// notification_rules rows under a shared group_id, so the UI can present a
// single rule with multiple conditions while the alert engine still sees
// flat rule rows.
func (s *Server) handleCreateRuleGroup(ctx context.Context, in *ruleGroupInput) (*ruleGroupOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	u, _ := userFromContext(ctx)
	resp, err := s.Store.CreateRuleGroup(ctx, in.Body, u.Email)
	if err != nil {
		if strings.Contains(err.Error(), "required") ||
			strings.Contains(err.Error(), "already exists") ||
			strings.Contains(err.Error(), "repeat_interval_sec") {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return nil, internalErr(ctx, "create rule group", err)
	}
	detail := fmt.Sprintf("{\"name\":%q,\"condition_count\":%d,\"channel_ids\":%d}",
		in.Body.Name, len(in.Body.Conditions), len(in.Body.ChannelIDs))
	s.audit(ctx, "rule.group.create", resp.GroupID.String(), detail)
	return &ruleGroupOutput{Body: resp}, nil
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
	s.audit(ctx, "rule.update", r.ID, r.Name)
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
	s.audit(ctx, "rule.delete", in.ID, "")
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
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	// Admins see every alert; everyone else is restricted to alerts where one
	// of their delivery channels appears in delivered_to. We pass an explicit
	// (possibly empty) slice so the store knows "filtered" vs "unfiltered".
	var restrict []uuid.UUID
	if u.Role != "admin" {
		channels, err := s.Store.ListChannels(ctx, u.ID, false)
		if err != nil {
			return nil, internalErr(ctx, "channel lookup failed", err)
		}
		restrict = make([]uuid.UUID, 0, len(channels))
		for _, c := range channels {
			id, err := uuid.Parse(c.ID)
			if err != nil {
				continue
			}
			restrict = append(restrict, id)
		}
	}
	history, err := s.Store.ListAlertHistory(ctx, since, in.Limit, restrict)
	if err != nil {
		return nil, internalErr(ctx, "query failed", err)
	}
	out := &alertHistoryOutput{}
	out.Body.Alerts = history
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
	s.audit(ctx, "channel.create", c.ID, c.Type+":"+c.Name)
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
	s.audit(ctx, "channel.update", c.ID, c.Type+":"+c.Name)
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
	s.audit(ctx, "channel.delete", in.ID, "")
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
		// "Test channel" surfaces dispatch failure inside the response
		// body so the UI can render the operator-facing reason. The HTTP
		// call itself succeeded.
		out.Body.OK = false
		out.Body.Error = err.Error()
		return out, nil //nolint:nilerr // surfaced in out.Body.Error by API contract
	}
	out.Body.OK = true
	return out, nil
}

// --- Auth: login / logout / me ---------------------------------------------

type loginInput struct {
	Body apitypes.LoginRequest
}
type loginOutput struct {
	// SetCookie pins the docs session cookie so /docs works via top-level
	// browser navigation. Only populated when the response carries a real
	// session token (i.e. not on the TOTP/passkey-needed intermediate).
	SetCookie string `header:"Set-Cookie"`
	Body      apitypes.LoginResponse
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
		out.Body.User = s.currentUserWithSecurity(ctx, u)
		return out, nil
	}

	token, err := s.Store.IssueSession(ctx, u, "", "", 0)
	if err != nil {
		return nil, internalErr(ctx, "session create failed", err)
	}
	out := &loginOutput{}
	out.SetCookie = docsCookieSetHeader(token, s.docsCookieTTL(ctx))
	out.Body.Token = token
	out.Body.ExpiresAt = s.sessionExpiresAt(ctx)
	out.Body.User = s.currentUserWithSecurity(ctx, u)
	return out, nil
}

// --- 2FA challenge after password login ------------------------------------

type totpChallengeInput struct {
	Body apitypes.TOTPChallengeRequest
}
type totpChallengeOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      apitypes.LoginResponse
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
	out.SetCookie = docsCookieSetHeader(token, s.docsCookieTTL(ctx))
	out.Body.Token = token
	out.Body.ExpiresAt = s.sessionExpiresAt(ctx)
	out.Body.User = s.currentUserWithSecurity(ctx, u)
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
	// audit 2026-05-12 F-3: pass the caller's session token so the store can
	// revoke every other session while keeping this device logged in.
	currentSessionToken, _ := tokenFromContext(ctx)
	if err := s.Store.ChangePassword(ctx, u.ID, in.Body.CurrentPassword, in.Body.NewPassword, currentSessionToken); err != nil {
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

// --- Self-service UI language preference -----------------------------------

type setLanguageInput struct {
	Body apitypes.SetLanguageRequest
}

func (s *Server) handleSetLanguage(ctx context.Context, in *setLanguageInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	if err := s.Store.SetLanguage(ctx, u.ID, in.Body.Language); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, huma.Error404NotFound("user not found")
		}
		// Invalid language value (or other validation error) — surface the
		// store's message to the caller so the SPA can display it verbatim.
		return nil, huma.Error400BadRequest(err.Error())
	}
	s.audit(ctx, "user.language.set", u.ID.String(), in.Body.Language)
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

type (
	emptyInput  struct{}
	emptyOutput struct {
		Body struct {
			OK bool `json:"ok"`
		}
	}
)

type logoutOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      struct {
		OK bool `json:"ok"`
	}
}

func (s *Server) handleLogout(ctx context.Context, _ *emptyInput) (*logoutOutput, error) {
	tok, _ := tokenFromContext(ctx)
	if tok != "" {
		_ = s.Store.RevokeSession(ctx, tok)
	}
	out := &logoutOutput{}
	out.SetCookie = docsCookieClearHeader()
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
	return &meOutput{Body: s.currentUserWithSecurity(ctx, full)}, nil
}

type authConfigOutput struct {
	Body apitypes.AuthConfig
}

// handleAuthConfig reports server-wide readiness flags so non-admin pages can
// surface "create-an-email-channel won't work yet" warnings without exposing
// the admin SMTP settings themselves. SSO is a placeholder for future
// Pocket-ID work; SMTP readiness is true iff the singleton row has both Host
// and FromAddress set. Best-effort: any unexpected error is logged and we
// return false rather than failing the call.
func (s *Server) handleAuthConfig(ctx context.Context, _ *emptyInput) (*authConfigOutput, error) {
	out := &authConfigOutput{}
	out.Body.SSOEnabled = false

	if s.Store == nil {
		return out, nil
	}
	settings, err := s.Store.GetSmtpSettings(ctx)
	switch {
	case err == nil:
		out.Body.SmtpConfigured = settings.Host != "" && settings.FromAddress != ""
	case errors.Is(err, store.ErrSmtpNotConfigured):
		out.Body.SmtpConfigured = false
	default:
		slog.Warn("auth-config: smtp settings lookup failed", "err", err)
		out.Body.SmtpConfigured = false
	}
	return out, nil
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

// requireMethodCompliance is a huma middleware that runs after requireUser
// and enforces the active force-mode policy. Compliant users and users
// still within their grace window pass through; users past grace get a 403
// with a stable error code the UI can intercept to render the "you must
// enroll" interstitial.
//
// audit 2026-05-12 F-9: do not fail open on a policy lookup error if we
// have a recent cached answer for this user. The process-local cache (TTL
// 60 s) is consulted on both success (refresh) and error (fall back). Only
// when the cache misses entirely do we keep today's fail-open behaviour —
// that is the boot-transient case for a user we have never seen.
func (s *Server) requireMethodCompliance(c huma.Context, next func(huma.Context)) {
	if s.Store == nil {
		next(c)
		return
	}
	u, ok := c.Context().Value(ctxKeyUser).(store.User)
	if !ok {
		// Defensive: requireUser should have already 401'd, but if the
		// chain was assembled wrong we shouldn't crash here.
		next(c)
		return
	}

	// audit 2026-05-12 F-9: fresh cache hit short-circuits the DB lookup.
	if entry, ok := s.policyComplianceLookup(u.ID); ok && time.Now().Before(entry.expiresAt) {
		s.applyComplianceDecision(c, next, entry.complies, entry.grace)
		return
	}

	complies, grace, err := s.Store.UserCompliesWithPolicy(c.Context(), u.ID)
	if err != nil {
		// audit 2026-05-12 F-9: prefer any cached answer (even expired) to
		// failing open. Only when we have no record at all do we let the
		// request through with a warning — that's the boot-transient case.
		if entry, ok := s.policyComplianceLookup(u.ID); ok {
			slog.Warn("policy compliance lookup failed; reusing cached decision",
				"user_id", u.ID, "err", err,
				"cache_age", time.Since(entry.expiresAt.Add(-60*time.Second)).String())
			s.applyComplianceDecision(c, next, entry.complies, entry.grace)
			return
		}
		slog.Warn("policy compliance lookup failed; no cached decision; failing open",
			"user_id", u.ID, "err", err)
		next(c)
		return
	}

	// audit 2026-05-12 F-9: cache the fresh answer.
	s.policyComplianceStore(u.ID, complies, grace)
	s.applyComplianceDecision(c, next, complies, grace)
}

// applyComplianceDecision is the shared tail of requireMethodCompliance: it
// either invokes next(c) or writes a 403 based on (complies, grace).
// audit 2026-05-12 F-9.
func (s *Server) applyComplianceDecision(c huma.Context, next func(huma.Context), complies bool, grace *time.Time) {
	if complies {
		next(c)
		return
	}
	if grace != nil && time.Now().Before(*grace) {
		next(c)
		return
	}
	_ = huma.WriteErr(s.API, c, http.StatusForbidden,
		"must_enroll_2fa: your administrator requires a second authentication factor; enroll a passkey or TOTP")
}

// policyComplianceLookup returns any cached entry for userID, fresh or
// stale. The caller decides whether to honor the TTL. audit 2026-05-12 F-9.
func (s *Server) policyComplianceLookup(userID uuid.UUID) (policyComplianceCacheEntry, bool) {
	s.policyComplianceCacheMu.RLock()
	defer s.policyComplianceCacheMu.RUnlock()
	entry, ok := s.policyComplianceCache[userID]
	return entry, ok
}

// policyComplianceStore writes a fresh 60 s entry for userID.
// audit 2026-05-12 F-9.
func (s *Server) policyComplianceStore(userID uuid.UUID, complies bool, grace *time.Time) {
	s.policyComplianceCacheMu.Lock()
	defer s.policyComplianceCacheMu.Unlock()
	s.policyComplianceCache[userID] = policyComplianceCacheEntry{
		complies:  complies,
		grace:     grace,
		expiresAt: time.Now().Add(60 * time.Second),
	}
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
	// audit 2026-05-12 F-19: only accept password_reset or invite kinds.
	// An empty expectedKind here would let an email_change / login_2fa /
	// webauthn_register token pivot into a password change.
	userID, _, err := s.Store.ConsumeActionToken(ctx, in.Body.Token, "password_reset")
	if errors.Is(err, store.ErrActionTokenInvalid) {
		userID, _, err = s.Store.ConsumeActionToken(ctx, in.Body.Token, "invite")
	}
	if err != nil {
		return nil, huma.Error401Unauthorized("token invalid or expired")
	}
	if err := s.Store.SetPasswordByAdmin(ctx, userID, in.Body.NewPassword); err != nil {
		return nil, internalErr(ctx, "set password failed", err)
	}
	// audit 2026-05-12 F-8: revoke all sessions on password reset confirm.
	_ = s.Store.RevokeUserSessions(ctx, userID)
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
		s.audit(ctx, "user.create", u.Email, role)
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
	s.audit(ctx, "user.create", u.Email, role)
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
	s.audit(ctx, "user.delete", in.ID, "")
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
	s.audit(ctx, "user.lock", in.ID, "")
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
	s.audit(ctx, "user.unlock", in.ID, "")
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
	url := inviteURL(tok)
	if err := s.sendInviteMail(ctx, u.Email, url); err == nil {
		// Mail delivery succeeded — withhold the URL so admins can't
		// shoulder-surf it from the UI. The user clicks the link in their
		// inbox; no other path exists.
		out.Body.InviteSent = true
	} else {
		// SMTP not configured or send failed — surface the URL as a
		// fallback so the admin can still hand it off manually. Logged
		// loud enough that an investigator can tell apart "we mailed it"
		// vs "we exposed it in the response".
		slog.WarnContext(ctx, "admin reset-password: mail send failed; returning URL fallback",
			"err", err, "target_id", in.ID)
		out.Body.ResetURL = url
	}
	s.audit(ctx, "user.reset_password", in.ID, u.Email)
	return out, nil
}

func (s *Server) handleAdminRevokeUserSessions(ctx context.Context, in *hostIDInput) (*emptyOutput, error) {
	id, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	caller, _ := userFromContext(ctx)
	if caller.ID == id {
		// Self-targeting from the admin user list is refused — admins who
		// want to drop their own session use logout. Prevents the surprise
		// of an admin locking themselves out via a misclick.
		return nil, huma.Error400BadRequest("cannot revoke your own sessions from the user list; use logout instead")
	}
	if err := s.Store.RevokeUserSessions(ctx, id); err != nil {
		return nil, internalErr(ctx, "revoke user sessions", err)
	}
	s.audit(ctx, "user.session.revoke_all", in.ID, "")
	out := &emptyOutput{}
	out.Body.OK = true
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
	// audit 2026-05-12 F-8: revoke all sessions on admin 2FA reset so any
	// session that authenticated with the now-stripped factor is invalidated.
	_ = s.Store.RevokeUserSessions(ctx, id)
	s.audit(ctx, "user.reset_2fa", in.ID, "")
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
	s.audit(ctx, "smtp.update", saved.Host, saved.FromAddress)
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
		// SMTP "test send" returns 200 + body.ok=false so the UI renders
		// the operator-facing failure reason; the HTTP exchange itself
		// succeeded. Same contract as the per-channel test handler above.
		out.Body.OK = false
		out.Body.Error = dispatchErr.Error()
		s.audit(ctx, "smtp.test", in.Body.To, "error: "+dispatchErr.Error())
		return out, nil //nolint:nilerr // surfaced in out.Body.Error by API contract
	}
	out.Body.OK = true
	s.audit(ctx, "smtp.test", in.Body.To, "ok")
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
	detail := fmt.Sprintf("%v %s-%s tz=%s days=%v", in.Body.QuietEnabled, in.Body.QuietStart, in.Body.QuietEnd, in.Body.QuietTZ, in.Body.QuietDays)
	s.audit(ctx, "quiet_hours.update", "", detail)
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
	s.audit(ctx, "agent_config.upsert", c.ID, c.Scope+":"+c.TargetID)
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
	s.audit(ctx, "agent_config.delete", in.ID, "")
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
		Total   int               `json:"total"   doc:"matching entries before paging"`
		Limit   int               `json:"limit"`
		Offset  int               `json:"offset"`
		Entries []serverlog.Entry `json:"entries"`
		Seq     uint64            `json:"seq"     doc:"monotonic write counter; gaps mean entries were dropped"`
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
	s.audit(ctx, "policy.update", "password_policy", "")
	return &passwordPolicyOutput{Body: p}, nil
}

// --- Admin: audit log -----------------------------------------------------

// audit writes an audit_log row best-effort: failures are downgraded to
// slog warnings so a missing audit insert never blocks the underlying
// admin action. Actor is taken from the request user when available.
func (s *Server) audit(ctx context.Context, action, target, detail string) {
	if s.Store == nil {
		return
	}
	actor := ""
	if u, ok := userFromContext(ctx); ok {
		actor = u.Email
	}
	if err := s.Store.AuditLog(ctx, actor, action, target, detail); err != nil {
		slog.Warn("audit log insert failed", "action", action, "target", target, "err", err)
	}
}

type adminListAuditInput struct {
	Limit  int    `query:"limit"  minimum:"1" maximum:"500" doc:"page size; default 100"`
	Offset int    `query:"offset" minimum:"0"               doc:"row offset; default 0"`
	Actor  string `query:"actor"                            doc:"exact actor match (e.g. user email)"`
	Action string `query:"action"                           doc:"exact action match (e.g. user.create)"`
}

type adminListAuditOutput struct {
	Body struct {
		Entries []apitypes.AuditEntry `json:"entries"`
		Total   int                   `json:"total"`
	}
}

func (s *Server) handleAdminListAudit(ctx context.Context, in *adminListAuditInput) (*adminListAuditOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := in.Offset
	if offset < 0 {
		offset = 0
	}
	entries, err := s.Store.ListAuditLog(ctx, limit, offset, in.Actor, in.Action)
	if err != nil {
		return nil, internalErr(ctx, "audit list failed", err)
	}
	total, err := s.Store.CountAuditLog(ctx, in.Actor, in.Action)
	if err != nil {
		return nil, internalErr(ctx, "audit count failed", err)
	}
	out := &adminListAuditOutput{}
	out.Body.Entries = entries
	out.Body.Total = total
	return out, nil
}

// --- WebAuthn: register/login ceremonies -----------------------------------

type webAuthnRegBeginInput struct {
	Body apitypes.WebAuthnRegisterBeginRequest
}
type webAuthnRegBeginOutput struct {
	Body apitypes.WebAuthnRegisterBeginResponse
}

func (s *Server) handleWebAuthnRegisterBegin(ctx context.Context, _ *webAuthnRegBeginInput) (*webAuthnRegBeginOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	resp, err := s.Store.BeginPasskeyRegistration(ctx, u)
	if err != nil {
		if errors.Is(err, store.ErrPasskeyNotConfigured) {
			return nil, huma.Error503ServiceUnavailable("passkey support not configured")
		}
		return nil, internalErr(ctx, "passkey register begin", err)
	}
	return &webAuthnRegBeginOutput{Body: resp}, nil
}

type webAuthnRegFinishInput struct {
	Body apitypes.WebAuthnRegisterFinishRequest
}
type webAuthnRegFinishOutput struct {
	Body apitypes.Passkey
}

func (s *Server) handleWebAuthnRegisterFinish(ctx context.Context, in *webAuthnRegFinishInput) (*webAuthnRegFinishOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	if in.Body.ChallengeToken == "" || in.Body.Credential == nil {
		return nil, huma.Error400BadRequest("challenge_token and credential required")
	}
	// The store layer parses raw bytes; round-trip the map back to JSON.
	credJSON, err := json.Marshal(in.Body.Credential)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid credential payload")
	}
	pk, err := s.Store.FinishPasskeyRegistration(ctx, u.ID, in.Body.ChallengeToken, credJSON, in.Body.Name)
	if err != nil {
		if errors.Is(err, store.ErrPasskeyNotConfigured) {
			return nil, huma.Error503ServiceUnavailable("passkey support not configured")
		}
		if errors.Is(err, store.ErrActionTokenInvalid) {
			return nil, huma.Error401Unauthorized("challenge invalid or expired")
		}
		return nil, internalErr(ctx, "passkey register finish", err)
	}
	return &webAuthnRegFinishOutput{Body: pk}, nil
}

type webAuthnLoginBeginOutput struct {
	Body apitypes.WebAuthnLoginBeginResponse
}

func (s *Server) handleWebAuthnLoginBegin(ctx context.Context, _ *struct{}) (*webAuthnLoginBeginOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	resp, err := s.Store.BeginPasskeyLogin(ctx)
	if err != nil {
		if errors.Is(err, store.ErrPasskeyNotConfigured) {
			return nil, huma.Error503ServiceUnavailable("passkey support not configured")
		}
		return nil, internalErr(ctx, "passkey login begin", err)
	}
	return &webAuthnLoginBeginOutput{Body: resp}, nil
}

type webAuthnLoginFinishInput struct {
	Body apitypes.WebAuthnLoginFinishRequest
}
type webAuthnLoginFinishOutput struct {
	SetCookie string `header:"Set-Cookie"`
	Body      apitypes.LoginResponse
}

func (s *Server) handleWebAuthnLoginFinish(ctx context.Context, in *webAuthnLoginFinishInput) (*webAuthnLoginFinishOutput, error) {
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	if in.Body.ChallengeToken == "" || in.Body.Credential == nil {
		return nil, huma.Error400BadRequest("challenge_token and credential required")
	}
	credJSON, err := json.Marshal(in.Body.Credential)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid credential payload")
	}
	u, err := s.Store.FinishPasskeyLogin(ctx, in.Body.ChallengeToken, credJSON)
	if err != nil {
		if errors.Is(err, store.ErrPasskeyNotConfigured) {
			return nil, huma.Error503ServiceUnavailable("passkey support not configured")
		}
		if errors.Is(err, store.ErrUserDisabled) {
			return nil, huma.Error403Forbidden("user is disabled")
		}
		if errors.Is(err, store.ErrUserNotFound) || errors.Is(err, store.ErrActionTokenInvalid) {
			return nil, huma.Error401Unauthorized("passkey not recognised")
		}
		return nil, internalErr(ctx, "passkey login finish", err)
	}
	token, err := s.Store.IssueSession(ctx, u, "", "", 0)
	if err != nil {
		return nil, internalErr(ctx, "session create failed", err)
	}
	out := &webAuthnLoginFinishOutput{}
	out.SetCookie = docsCookieSetHeader(token, s.docsCookieTTL(ctx))
	out.Body.Token = token
	out.Body.ExpiresAt = s.sessionExpiresAt(ctx)
	out.Body.User = s.currentUserWithSecurity(ctx, u)
	return out, nil
}

// --- Passkey self-management ----------------------------------------------

type listPasskeysOutput struct {
	Body apitypes.ListPasskeysResponse
}

func (s *Server) handleListPasskeys(ctx context.Context, _ *struct{}) (*listPasskeysOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	pks, err := s.Store.ListPasskeys(ctx, u.ID)
	if err != nil {
		return nil, internalErr(ctx, "list passkeys", err)
	}
	out := &listPasskeysOutput{}
	out.Body.Passkeys = pks
	return out, nil
}

type renamePasskeyInput struct {
	ID   string `path:"id" format:"uuid"`
	Body apitypes.RenamePasskeyRequest
}

func (s *Server) handleRenamePasskey(ctx context.Context, in *renamePasskeyInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	credID, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}
	if err := s.Store.RenamePasskey(ctx, u.ID, credID, in.Body.Name); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, huma.Error404NotFound("passkey not found")
		}
		return nil, huma.Error400BadRequest(err.Error())
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type deletePasskeyInput struct {
	ID string `path:"id" format:"uuid"`
}

func (s *Server) handleDeletePasskey(ctx context.Context, in *deletePasskeyInput) (*emptyOutput, error) {
	u, ok := userFromContext(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("no session")
	}
	credID, err := uuid.Parse(in.ID)
	if err != nil {
		return nil, huma.Error400BadRequest("invalid id")
	}

	// Policy gate: refuse to drop the user's last passkey when the active
	// force-mode is passkey_required AND they have no TOTP fallback AND
	// they aren't currently inside a grace window. The store call would
	// otherwise succeed and we'd lock them out on next refresh.
	pol, err := s.Store.GetSecurityPolicy(ctx)
	if err == nil && pol.ForceMode == store.ForceModePasskeyRequired {
		full, gerr := s.Store.GetUser(ctx, u.ID)
		if gerr == nil && !full.TOTPActive {
			pks, lerr := s.Store.ListPasskeys(ctx, u.ID)
			if lerr == nil && len(pks) <= 1 {
				// Determine grace from the per-user column. If grace has
				// expired or never existed, refuse.
				var grace *time.Time
				_ = s.Store.Pool.QueryRow(ctx,
					`SELECT force_grace_until FROM users WHERE id = $1`, u.ID,
				).Scan(&grace)
				if grace == nil || !time.Now().Before(*grace) {
					return nil, huma.Error409Conflict(
						"cannot delete last passkey under current policy; enroll another method first")
				}
			}
		}
	}

	if err := s.Store.DeletePasskey(ctx, u.ID, credID); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return nil, huma.Error404NotFound("passkey not found")
		}
		return nil, internalErr(ctx, "delete passkey", err)
	}
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

// --- Admin: security policy -----------------------------------------------

type securityPolicyOutput struct {
	Body apitypes.SecurityPolicy
}

func (s *Server) handleGetSecurityPolicy(ctx context.Context, _ *struct{}) (*securityPolicyOutput, error) {
	p, err := s.Store.GetSecurityPolicy(ctx)
	if err != nil {
		return nil, internalErr(ctx, "get security policy", err)
	}
	return &securityPolicyOutput{Body: p}, nil
}

type setSecurityPolicyInput struct {
	Body apitypes.SecurityPolicy
}

func (s *Server) handleSetSecurityPolicy(ctx context.Context, in *setSecurityPolicyInput) (*emptyOutput, error) {
	actor, _ := userFromContext(ctx)

	// audit 2026-05-12 F-20: refuse a policy change that would lock the
	// calling admin out the moment it persists. If the new policy is
	// stricter than `off` and the caller has no compliant method enrolled,
	// they would be redirected to the enrollment surface on the next
	// request — but with grace_days=0 they'd have no grace either.
	if in.Body.ForceMode != store.ForceModeOff && in.Body.GraceDays == 0 {
		complies, _, cerr := s.Store.UserCompliesWithPolicyKind(ctx, actor.ID, in.Body.ForceMode)
		if cerr == nil && !complies {
			return nil, huma.Error400BadRequest(
				"this policy would lock you out immediately — enroll a passkey (or TOTP, for 2fa_any) first, or set grace_days > 0")
		}
	}

	if err := s.Store.SetSecurityPolicy(ctx, in.Body, actor.Email); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	detail := fmt.Sprintf("force_mode=%s grace_days=%d max_session_hours=%d idle_timeout_minutes=%d",
		in.Body.ForceMode, in.Body.GraceDays, in.Body.MaxSessionHours, in.Body.IdleTimeoutMinutes)
	s.audit(ctx, "admin.security.policy.update", "security_policy", detail)
	out := &emptyOutput{}
	out.Body.OK = true
	return out, nil
}

type revokeAllSessionsOutput struct {
	Body apitypes.RevokeAllSessionsResponse
}

func (s *Server) handleRevokeAllSessions(ctx context.Context, _ *struct{}) (*revokeAllSessionsOutput, error) {
	// Spare the caller's own session so they don't kick themselves out
	// before they see the response.
	tok, _ := tokenFromContext(ctx)
	n, err := s.Store.RevokeAllSessions(ctx, tok)
	if err != nil {
		return nil, internalErr(ctx, "revoke all sessions", err)
	}
	s.audit(ctx, "admin.session.revoke_all", "", fmt.Sprintf("revoked=%d", n))
	out := &revokeAllSessionsOutput{}
	out.Body.Revoked = n
	return out, nil
}
