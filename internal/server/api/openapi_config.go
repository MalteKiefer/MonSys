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
// Documentation surface (Scalar):
//
//   - Disable huma's built-in /docs renderer (it points at unpkg.com) and
//     serve a vendored Scalar bundle from internal/server/docs instead.
//     See api.registerRoutes() and internal/server/docs/docs.go.
//   - Populate Info.Contact, Info.Description, top-level Tags (with one
//     sentence per tag) and ExternalDocs so the rendered page surfaces
//     useful navigation and points at the GitHub repo.
//
// The function takes the resolved version (already merged with build-time
// ldflags) so callers don't have to import the version package twice.
// block inline; splitting just renames variables without improving clarity.
//
//nolint:funlen // metadata literal carries the Description / Tags / Servers
func openAPIConfig(title, ver string) huma.Config {
	cfg := huma.DefaultConfig(title, ver)
	cfg.Info.Description = "" +
		"MonSys is a self-hosted server-monitoring stack: a Go backend " +
		"(chi + huma + TimescaleDB), a React SPA, and a per-host agent " +
		"daemon (`mon-agent`).\n\n" +
		"Agents authenticate with a per-host bearer key issued by " +
		"`POST /v1/agents/register` and push metrics, package " +
		"inventories, log lines, and process snapshots to " +
		"`POST /v1/ingest`. Users authenticate with a session token " +
		"issued by `POST /v1/auth/login` (with optional WebAuthn / TOTP " +
		"step-up) and consume the rest of the surface from the SPA.\n\n" +
		"This document is generated directly from the live API code via " +
		"huma's reflection; it stays in sync with `mon-server` by " +
		"definition. Drift between this spec and `api/openapi.yaml` in " +
		"the repository is caught by CI."
	cfg.Info.Contact = &huma.Contact{
		Name: "MonSys on GitHub",
		URL:  "https://github.com/MalteKiefer/MonSys",
	}
	cfg.Info.License = &huma.License{
		Name: "MIT",
		URL:  "https://github.com/MalteKiefer/MonSys/blob/main/LICENSE",
	}

	// Tag descriptions — these surface as the section headings in the
	// interactive docs viewer. Keep each one to one short sentence so the
	// Scalar sidebar stays readable. The names must match the strings
	// used in Operation.Tags across the registerXxxRoutes files.
	cfg.Tags = []*huma.Tag{
		{
			Name:        "auth",
			Description: "Session login/logout, password reset, WebAuthn + TOTP step-up, language preference, /me.",
		},
		{
			Name:        "agents",
			Description: "Bootstrap-token lifecycle: register a host, issue per-agent keys, surface agent-update metadata.",
		},
		{
			Name:        "ingest",
			Description: "Single high-volume endpoint that agents push metrics, packages, logs, and process samples to.",
		},
		{
			Name:        "hosts",
			Description: "Per-host inventory and time-series read APIs consumed by the SPA dashboards.",
		},
		{
			Name:        "groups",
			Description: "Host-group memberships used to scope agent config, alert rules, and notification routing.",
		},
		{
			Name:        "monitors",
			Description: "Synthetic checks (HTTP, TCP, ICMP, TLS) executed server-side; results land in metrics_monitor_*.",
		},
		{
			Name:        "packages",
			Description: "OS-package inventory + pending-update views derived from agent ingest.",
		},
		{
			Name:        "notifications",
			Description: "Notification channels (email, Slack, webhook) and rule routing across hosts/groups/severities.",
		},
		{
			Name:        "admin",
			Description: "Operator-only surfaces: user management, agent config, security policy, audit log, agent-update bundle.",
		},
		{
			Name:        "security",
			Description: "Public security surface — well-known paths, security policy disclosure helpers.",
		},
	}

	// Point the interactive docs viewer at the GitHub repo so the
	// renderer's "external docs" link goes somewhere useful instead of
	// being absent.
	cfg.ExternalDocs = &huma.ExternalDocs{
		Description: "MonSys source code, issue tracker, and release notes.",
		URL:         "https://github.com/MalteKiefer/MonSys",
	}

	// Disable huma's built-in /docs renderer. We serve our own vendored
	// Scalar bundle from internal/server/docs at the same path so the
	// supply chain stays explicit (no runtime CDN dependency) and the
	// page works in air-gapped deployments. The /openapi.{yaml,json}
	// routes remain registered by huma — only the HTML shell is ours.
	cfg.DocsPath = ""

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
