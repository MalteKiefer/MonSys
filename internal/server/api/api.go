package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/pr0ph37/mon/internal/server/notify"
	"github.com/pr0ph37/mon/internal/server/spa"
	"github.com/pr0ph37/mon/internal/server/store"
	"github.com/pr0ph37/mon/internal/shared/apitypes"
	"github.com/pr0ph37/mon/internal/shared/version"
)

type Server struct {
	Store  *store.Store
	Router chi.Router
	API    huma.API
}

func New(s *store.Store) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	cfg := huma.DefaultConfig("mon", version.Version)
	cfg.Info.Description = "Self-hosted server-monitoring API. Agents push metrics; users query."
	cfg.Servers = []*huma.Server{{URL: "/", Description: "current"}}

	api := humachi.New(r, cfg)

	srv := &Server{Store: s, Router: r, API: api}
	srv.registerRoutes()
	return srv
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
		OperationID: "host-system-metrics",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/metrics/system",
		Summary:     "Time-range query of system metrics for a host",
		Tags:        []string{"hosts"},
		Middlewares: protected,
	}, s.handleSystemMetrics)

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
		return nil, huma.Error401Unauthorized("missing bootstrap token")
	}
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	resp, err := s.Store.RegisterAgent(ctx, token, in.Body, in.RawHost)
	if err != nil {
		if errors.Is(err, store.ErrTokenInvalid) {
			return nil, huma.Error401Unauthorized("bootstrap token invalid or expired")
		}
		return nil, huma.Error500InternalServerError("registration failed", err)
	}
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
		return nil, huma.Error401Unauthorized("missing agent key")
	}
	if s.Store == nil {
		return nil, huma.Error503ServiceUnavailable("server has no store configured")
	}
	hostID, err := s.Store.AuthenticateAgent(ctx, key)
	if err != nil {
		if errors.Is(err, store.ErrAgentKeyInvalid) {
			return nil, huma.Error401Unauthorized("agent key invalid")
		}
		return nil, huma.Error500InternalServerError("auth failed", err)
	}
	if err := s.Store.SaveIngest(ctx, hostID, in.Body); err != nil {
		return nil, huma.Error500InternalServerError("ingest persist failed", err)
	}
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
		return nil, huma.Error500InternalServerError("list hosts failed", err)
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
		return nil, huma.Error500InternalServerError("query failed", err)
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
		return nil, huma.Error500InternalServerError("query failed", err)
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
		return nil, huma.Error500InternalServerError("query failed", err)
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
		return nil, huma.Error500InternalServerError("list failed", err)
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
		return nil, huma.Error500InternalServerError("delete failed", err)
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
		return nil, huma.Error500InternalServerError("query failed", err)
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
		return nil, huma.Error500InternalServerError("list failed", err)
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
		return nil, huma.Error500InternalServerError("get failed", err)
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
		return nil, huma.Error500InternalServerError("delete failed", err)
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
		return nil, huma.Error500InternalServerError("query failed", err)
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
	cs, err := s.Store.ListChannels(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("list failed", err)
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
	c, err := s.Store.CreateChannel(ctx, in.Body, u.Email)
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
	c, err := s.Store.GetChannel(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrChannelNotFound) {
			return nil, huma.Error404NotFound("channel not found")
		}
		return nil, huma.Error500InternalServerError("get failed", err)
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
	c, err := s.Store.UpdateChannel(ctx, id, in.Body)
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
	if err := s.Store.DeleteChannel(ctx, id); err != nil {
		if errors.Is(err, store.ErrChannelNotFound) {
			return nil, huma.Error404NotFound("channel not found")
		}
		return nil, huma.Error500InternalServerError("delete failed", err)
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
		return nil, huma.Error500InternalServerError("login failed", err)
	}
	token, err := s.Store.IssueSession(ctx, u, "", "", 0)
	if err != nil {
		return nil, huma.Error500InternalServerError("session create failed", err)
	}
	out := &loginOutput{}
	out.Body.Token = token
	out.Body.ExpiresAt = time.Now().Add(12 * time.Hour).UTC()
	out.Body.User = apitypes.CurrentUser{
		ID: u.ID.String(), Email: u.Email, Role: u.Role,
	}
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
	return &meOutput{Body: apitypes.CurrentUser{
		ID: u.ID.String(), Email: u.Email, Role: u.Role,
	}}, nil
}

// --- Session middleware ----------------------------------------------------

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
	ctxKeyToken
)

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
