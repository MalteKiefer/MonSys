// Package registry implements a tiny OCI/Docker registry v2 client used by
// the docker workload collector to detect "update available" containers.
//
// Scope is intentionally narrow: given an image reference (e.g.
// "nginx:1.27.0", "ghcr.io/foo/bar:v1", "lscr.io/linuxserver/sonarr:latest")
// we resolve the registry's current manifest digest for the same tag via
// HEAD /v2/<repo>/manifests/<tag>. The caller compares that against the
// container's runtime image digest to decide whether a newer image exists.
//
// Why standalone instead of pulling in the full docker/distribution
// dependency tree:
//
//   * Keeps the agent binary small and the supply chain narrow (no new go
//     deps — net/http stdlib only).
//   * Mirrors the constraints of the rest of the agent collectors which
//     deliberately speak the Docker Engine API directly.
//
// The OAuth2 "WWW-Authenticate" challenge flow is implemented for the few
// public registries we expect to encounter (Docker Hub, GHCR, Quay,
// linuxserver, etc.). All requests are anonymous: we send no credentials
// and rely on the registry's "pull"-scoped public token. Private registries
// are out of scope for now — see the TODO at the bottom of this file.
//
// Network failures are non-fatal for callers: every method returns an error
// that the caller is expected to log-and-continue on. The cache below is a
// sync.Map keyed on (registry, repo, tag) with a 1h TTL so we don't hammer
// rate-limited registries (Docker Hub anonymous = ~100 manifest pulls/6h).
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CacheTTL is how long a successful digest lookup is cached before the
// agent will hit the registry again. Keep this generous: digests rarely
// change between agent ticks, and Hub anonymous rate-limits are tight.
const CacheTTL = 1 * time.Hour

// Result captures a single manifest probe outcome. LatestDigest is the
// canonical "Docker-Content-Digest" header returned by the registry, e.g.
// "sha256:abcdef…". Err is set on transport / auth / parse failure; the
// caller should treat any non-nil Err as "no upstream info available" and
// proceed to ingest the workload anyway.
type Result struct {
	Registry     string
	Repo         string
	Tag          string
	LatestDigest string
	Err          error
}

// Client is a goroutine-safe registry client with built-in caching. Construct
// it once per agent process; the underlying http.Client is shared across all
// lookups.
type Client struct {
	hc    *http.Client
	cache sync.Map // key string -> cacheEntry
}

type cacheEntry struct {
	digest    string
	expiresAt time.Time
}

// New returns a Client with sensible timeouts. Callers should reuse a single
// instance — the auth-token roundtrip benefits from connection reuse.
func New() *Client {
	return &Client{
		hc: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// LatestDigest returns the upstream manifest digest for the same image:tag
// the container was started from. It detects the registry from imageRef,
// negotiates a pull-scoped bearer token if the registry requires one, and
// caches successes for CacheTTL.
//
// imageRef may be in any of the canonical Docker forms:
//
//	nginx                                 → docker.io/library/nginx:latest
//	nginx:1.27.0                          → docker.io/library/nginx:1.27.0
//	library/nginx:1.27.0                  → docker.io/library/nginx:1.27.0
//	ghcr.io/foo/bar:v1                    → ghcr.io/foo/bar:v1
//	lscr.io/linuxserver/sonarr:latest     → lscr.io/linuxserver/sonarr:latest
//	repo@sha256:…                         → returns ErrPinnedDigest
//
// Pinned-digest references are rejected: there's nothing to compare against.
func (c *Client) LatestDigest(ctx context.Context, imageRef string) (string, error) {
	ref, err := parseRef(imageRef)
	if err != nil {
		return "", err
	}
	cacheKey := ref.cacheKey()
	if v, ok := c.cache.Load(cacheKey); ok {
		ce := v.(cacheEntry)
		if time.Now().Before(ce.expiresAt) {
			return ce.digest, nil
		}
	}

	digest, err := c.fetchDigest(ctx, ref)
	if err != nil {
		return "", err
	}
	c.cache.Store(cacheKey, cacheEntry{
		digest:    digest,
		expiresAt: time.Now().Add(CacheTTL),
	})
	return digest, nil
}

// ErrPinnedDigest signals that the input image reference is already a
// digest-pinned reference (image@sha256:…). There's no concept of "newer
// digest" for these — the caller can short-circuit safely.
var ErrPinnedDigest = errors.New("registry: image is digest-pinned, cannot compare upstream")

// imageRef is the parsed form of an image reference, normalised to the
// canonical (registry, repo, tag) triple used by the v2 manifest endpoint.
type imageRef struct {
	registry string // host:port — e.g. registry-1.docker.io, ghcr.io, lscr.io
	repo     string // repository path — e.g. library/nginx, foo/bar
	tag      string // tag — e.g. 1.27.0, v1, latest
}

func (r imageRef) cacheKey() string { return r.registry + "/" + r.repo + ":" + r.tag }

// parseRef normalises an image reference. The Docker convention is:
//
//   - If the part before the first '/' contains a '.' or ':' or equals
//     "localhost", it's treated as a registry hostname; otherwise the
//     reference is implicitly on Docker Hub.
//   - Single-component references (e.g. "nginx") are mapped to the "library"
//     namespace on Hub (so "nginx" → "library/nginx").
//   - Tag defaults to "latest" when missing.
//   - Digest-pinned references (containing '@sha256:') return ErrPinnedDigest.
func parseRef(s string) (imageRef, error) {
	if s == "" {
		return imageRef{}, errors.New("registry: empty image reference")
	}
	if strings.Contains(s, "@sha256:") || strings.Contains(s, "@sha512:") {
		return imageRef{}, ErrPinnedDigest
	}

	// Split off optional registry component.
	registry := "registry-1.docker.io"
	rest := s
	if i := strings.Index(s, "/"); i > 0 {
		head := s[:i]
		if strings.ContainsAny(head, ".:") || head == "localhost" {
			registry = head
			rest = s[i+1:]
		}
	}

	// Split tag.
	tag := "latest"
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		// Watch out: "host:port/repo" without tag is not possible here because
		// we already stripped the registry above.
		tag = rest[i+1:]
		rest = rest[:i]
	}

	// On Docker Hub, single-component references live under "library/".
	if registry == "registry-1.docker.io" && !strings.Contains(rest, "/") {
		rest = "library/" + rest
	}

	if rest == "" {
		return imageRef{}, fmt.Errorf("registry: malformed image reference %q", s)
	}
	return imageRef{registry: registry, repo: rest, tag: tag}, nil
}

// fetchDigest performs the HEAD on v2/<repo>/manifests/<tag>, transparently
// handling the auth challenge round-trip if the registry asks for one.
func (c *Client) fetchDigest(ctx context.Context, ref imageRef) (string, error) {
	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", ref.registry, ref.repo, ref.tag)

	// First attempt: anonymous. Many proxies (and lscr.io's CDN) succeed here.
	digest, status, wwwAuth, err := c.headManifest(ctx, manifestURL, "")
	if err != nil {
		return "", err
	}
	if status == http.StatusOK && digest != "" {
		return digest, nil
	}
	if status == http.StatusUnauthorized {
		token, terr := c.fetchToken(ctx, wwwAuth, ref)
		if terr != nil {
			return "", fmt.Errorf("registry token: %w", terr)
		}
		digest, status, _, err = c.headManifest(ctx, manifestURL, token)
		if err != nil {
			return "", err
		}
		if status == http.StatusOK && digest != "" {
			return digest, nil
		}
	}
	return "", fmt.Errorf("registry: unexpected status %d for %s", status, manifestURL)
}

// headManifest issues the HEAD with the manifest accept headers documented
// in the OCI / Docker distribution spec. It returns the Docker-Content-Digest
// (when present), the HTTP status, the raw WWW-Authenticate header (for
// challenge parsing), and any transport error.
func (c *Client) headManifest(ctx context.Context, manifestURL, bearer string) (string, int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", 0, "", err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
	}, ", "))
	req.Header.Set("User-Agent", "mon-agent/registry-probe")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer resp.Body.Close()
	return resp.Header.Get("Docker-Content-Digest"), resp.StatusCode, resp.Header.Get("WWW-Authenticate"), nil
}

// fetchToken parses the WWW-Authenticate Bearer challenge and exchanges it
// for a pull-scoped token. Format per RFC 6750 / distribution spec:
//
//	Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/nginx:pull"
//
// We round-trip the realm endpoint with the service+scope params and read
// the JSON body's "token" (or "access_token") field.
func (c *Client) fetchToken(ctx context.Context, wwwAuth string, ref imageRef) (string, error) {
	const prefix = "Bearer "
	if !strings.HasPrefix(wwwAuth, prefix) {
		return "", fmt.Errorf("registry: unsupported auth scheme %q", wwwAuth)
	}
	params := parseChallenge(wwwAuth[len(prefix):])
	realm := params["realm"]
	if realm == "" {
		return "", errors.New("registry: missing realm in challenge")
	}
	service := params["service"]
	scope := params["scope"]
	if scope == "" {
		// Fallback: synthesise the standard pull scope. ghcr.io sometimes omits
		// the scope parameter in the challenge but still requires it in the
		// token request.
		scope = "repository:" + ref.repo + ":pull"
	}

	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("registry: bad realm url: %w", err)
	}
	q := u.Query()
	if service != "" {
		q.Set("service", service)
	}
	q.Set("scope", scope)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "mon-agent/registry-probe")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry: token endpoint status %d", resp.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("registry: decode token: %w", err)
	}
	tok := body.Token
	if tok == "" {
		tok = body.AccessToken
	}
	if tok == "" {
		return "", errors.New("registry: empty token")
	}
	return tok, nil
}

// parseChallenge parses a comma-separated list of key="value" pairs as found
// in a Bearer WWW-Authenticate challenge. Whitespace tolerant; quotes are
// optional per RFC 7235 but in practice always present.
func parseChallenge(s string) map[string]string {
	out := map[string]string{}
	// Split top-level by commas, but values may contain commas inside the
	// scope — defensively walk char-by-char and respect quotes.
	var current strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			current.WriteRune(r)
		case r == ',' && !inQuote:
			parsePair(current.String(), out)
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	parsePair(current.String(), out)
	return out
}

func parsePair(p string, into map[string]string) {
	p = strings.TrimSpace(p)
	if p == "" {
		return
	}
	i := strings.IndexByte(p, '=')
	if i < 0 {
		return
	}
	k := strings.TrimSpace(p[:i])
	v := strings.TrimSpace(p[i+1:])
	v = strings.Trim(v, `"`)
	if k != "" {
		into[k] = v
	}
}

// TODO(operator-config): expose an opt-out toggle in agent config so this
// collector can be disabled on hosts behind air-gapped networks. For now it
// degrades gracefully — every network failure short-circuits to "no
// upstream info" and the workload is still ingested.
