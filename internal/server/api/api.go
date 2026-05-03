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

	huma.Register(s.API, huma.Operation{
		OperationID: "list-hosts",
		Method:      http.MethodGet,
		Path:        "/v1/hosts",
		Summary:     "List all known hosts",
		Tags:        []string{"hosts"},
	}, s.handleListHosts)

	huma.Register(s.API, huma.Operation{
		OperationID: "host-system-metrics",
		Method:      http.MethodGet,
		Path:        "/v1/hosts/{id}/metrics/system",
		Summary:     "Time-range query of system metrics for a host",
		Tags:        []string{"hosts"},
	}, s.handleSystemMetrics)
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
