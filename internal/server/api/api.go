package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

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
	// Liveness / readiness — outside huma so they stay tiny and never validate.
	s.Router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	s.Router.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.Store.Pool.Ping(ctx); err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})

	// Agent registration: trades a bootstrap token for an agent_key.
	huma.Register(s.API, huma.Operation{
		OperationID: "agent-register",
		Method:      http.MethodPost,
		Path:        "/v1/agents/register",
		Summary:     "Register a new agent",
		Description: "Trade a one-time bootstrap token (Authorization: Bearer …) for a per-host agent_key.",
		Tags:        []string{"agents"},
	}, s.handleAgentRegister)

	// Ingest: agent pushes batch of inventory + metric samples.
	huma.Register(s.API, huma.Operation{
		OperationID: "ingest",
		Method:      http.MethodPost,
		Path:        "/v1/ingest",
		Summary:     "Ingest metrics + inventory",
		Description: "Agents push samples here. Auth: Authorization: Bearer <agent_key>.",
		Tags:        []string{"ingest"},
	}, s.handleIngest)
}

// --- Register ---------------------------------------------------------------

type registerInput struct {
	Authorization string `header:"Authorization" required:"true" doc:"Bearer <bootstrap-token>"`
	Body          apitypes.AgentRegisterRequest
}

type registerOutput struct {
	Body apitypes.AgentRegisterResponse
}

func (s *Server) handleAgentRegister(_ context.Context, in *registerInput) (*registerOutput, error) {
	if !hasBearer(in.Authorization) {
		return nil, huma.Error401Unauthorized("missing bootstrap token")
	}
	// M1 stub: real exchange happens in M2; surface contract is stable.
	return nil, huma.Error501NotImplemented("agent registration not yet implemented (M2)")
}

// --- Ingest -----------------------------------------------------------------

type ingestInput struct {
	Authorization string `header:"Authorization" required:"true" doc:"Bearer <agent_key>"`
	Body          apitypes.IngestRequest
}

type ingestOutput struct {
	Body apitypes.IngestResponse
}

func (s *Server) handleIngest(_ context.Context, in *ingestInput) (*ingestOutput, error) {
	if !hasBearer(in.Authorization) {
		return nil, huma.Error401Unauthorized("missing agent key")
	}
	// M1 stub: accept-and-discard so agents under development don't error.
	return &ingestOutput{Body: apitypes.IngestResponse{
		Accepted:   true,
		ServerTime: time.Now().UTC(),
	}}, nil
}

func hasBearer(h string) bool {
	const p = "Bearer "
	return len(h) > len(p) && h[:len(p)] == p
}

// Errors helper kept to avoid unused-import drift if huma is touched.
var _ = errors.New
