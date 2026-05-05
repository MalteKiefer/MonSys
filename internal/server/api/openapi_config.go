package api

import (
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// openAPIConfig builds the customized huma.Config used by api.New().
//
// Hardening goals (AUDIT-201/202/207/208/209):
//
//   - Populate components.securitySchemes (bearer tokens) so consumers know
//     how to authenticate. AUDIT-201.
//   - Set a root-level security: requirement so every operation defaults to
//     a session-token bearer; unauthenticated routes opt out by setting
//     Operation.Security = []map[string][]string{} (empty slice). AUDIT-202.
//   - List a real production server URL plus a relative "/" entry so the
//     spec is useful both from the public deployment and from local dev.
//     The MON_PUBLIC_URL env var lets operators override the hostname for
//     air-gapped deployments. AUDIT-209.
//   - Attach a license so spec linters stop flagging the file. AUDIT-208.
//
// The function takes the resolved version (already merged with build-time
// ldflags) so callers don't have to import the version package twice.
func openAPIConfig(title, ver string) huma.Config {
	cfg := huma.DefaultConfig(title, ver)
	cfg.Info.Description = "Self-hosted server-monitoring API. Agents push metrics; users query."
	cfg.Info.License = &huma.License{
		Name: "MIT",
		URL:  "https://github.com/MalteKiefer/MonSys/blob/main/LICENSE",
	}

	// Servers: production first (overridable), then a relative "/" so local
	// dev / port-forwards work out of the box. AUDIT-209.
	publicURL := strings.TrimRight(os.Getenv("MON_PUBLIC_URL"), "/")
	if publicURL == "" {
		publicURL = "https://mon.kiefer-networks.de"
	}
	cfg.Servers = []*huma.Server{
		{URL: publicURL, Description: "production"},
		{URL: "/", Description: "current (relative)"},
	}

	// Security schemes (AUDIT-201). All three are HTTP bearer tokens but
	// scoped to different audiences so spec consumers can tell which token
	// belongs where:
	//
	//   sessionToken    — opaque session token issued by /v1/auth/login.
	//                     Required for every admin/user route.
	//   agentKey        — per-host key issued by /v1/agents/register.
	//                     Used only on /v1/ingest.
	//   bootstrapToken  — single-use token consumed by /v1/agents/register.
	if cfg.Components == nil {
		cfg.Components = &huma.Components{}
	}
	if cfg.Components.SecuritySchemes == nil {
		cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	cfg.Components.SecuritySchemes["sessionToken"] = &huma.SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "opaque",
		Description:  "Opaque session token returned by /v1/auth/login. Pass as Authorization: Bearer <token>.",
	}
	cfg.Components.SecuritySchemes["agentKey"] = &huma.SecurityScheme{
		Type:        "http",
		Scheme:      "bearer",
		Description: "Per-host agent key issued by /v1/agents/register. Required on /v1/ingest only.",
	}
	cfg.Components.SecuritySchemes["bootstrapToken"] = &huma.SecurityScheme{
		Type:        "http",
		Scheme:      "bearer",
		Description: "Single-use bootstrap token consumed by /v1/agents/register.",
	}

	// Root-level security: default every operation to session auth.
	// Operations that need a different scheme (or none) override this in
	// their huma.Operation.Security field. AUDIT-202.
	cfg.Security = []map[string][]string{
		{"sessionToken": {}},
	}

	return cfg
}

// secAgentRegister, secIngest, secNoAuth are reusable per-operation security
// requirements so the registration site stays terse.
var (
	secAgentRegister = []map[string][]string{{"bootstrapToken": {}}}
	secIngest        = []map[string][]string{{"agentKey": {}}}
	// secNoAuth is "explicitly unauthenticated" — an empty slice (not nil)
	// so OpenAPI emits security: [] which overrides the root requirement.
	secNoAuth = []map[string][]string{}
)
