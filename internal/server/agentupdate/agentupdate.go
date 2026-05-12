// Package agentupdate publishes the metadata mon-agent needs to self-update:
// the latest version, the download URL per OS/arch, and the SHA256 of each
// binary. The data is sourced either from operator-provided env vars (static
// mode, fully air-gapped friendly) or from the public GitHub Releases API
// (default mode, polled at most once per hour).
//
// The endpoint exposing this data is intentionally public — the metadata is
// not a secret, and pre-bootstrap install scripts may need it before they
// have an agent_key. Operators who want to gate it can do so via reverse
// proxy.
package agentupdate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Binary describes one downloadable artefact.
type Binary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Manifest is what the API returns to a caller.
type Manifest struct {
	Version   string            `json:"version"`
	Channel   string            `json:"channel"`
	CheckedAt time.Time         `json:"checked_at"`
	Binaries  map[string]Binary `json:"binaries"` // key = "linux/amd64", "linux/arm64", …
	Source    string            `json:"source"`   // "github" | "static"
}

// Resolver fetches and caches the latest manifest.
type Resolver struct {
	mu             sync.Mutex
	cached         *Manifest
	lastForceFetch time.Time

	repo      string // owner/repo for github mode (default "MalteKiefer/MonSys")
	tag       string // "latest" rolling pre-release, or a specific tag like "v0.1.5"
	staticVer string // if set, static mode — env-driven manifest
	cli       *http.Client
}

// forceMinInterval rate-limits ?fresh=1 callers so an agent stuck in a SHA
// mismatch loop can't drown the upstream API with refresh requests.
const forceMinInterval = 60 * time.Second

// NewFromEnv reads operator overrides and returns a configured resolver.
//
//	MON_AGENT_UPDATE_REPO   default "MalteKiefer/MonSys"
//	MON_AGENT_UPDATE_TAG    default "latest"
//	MON_AGENT_LATEST_VERSION (any value triggers static mode)
//	MON_AGENT_LATEST_URL_LINUX_AMD64
//	MON_AGENT_LATEST_URL_LINUX_ARM64
//	MON_AGENT_LATEST_SHA256_LINUX_AMD64
//	MON_AGENT_LATEST_SHA256_LINUX_ARM64
func NewFromEnv() *Resolver {
	r := &Resolver{
		repo: firstNonEmpty(os.Getenv("MON_AGENT_UPDATE_REPO"), "MalteKiefer/MonSys"),
		tag:  firstNonEmpty(os.Getenv("MON_AGENT_UPDATE_TAG"), "latest"),
		cli:  &http.Client{Timeout: 20 * time.Second},
	}
	if v := os.Getenv("MON_AGENT_LATEST_VERSION"); v != "" {
		r.staticVer = v
	}
	return r
}

// Latest returns the cached manifest, refreshing if older than 1h or absent.
// `force` bypasses the cache entirely and re-fetches upstream, but is itself
// rate-limited to one forced refresh per forceMinInterval to keep a flapping
// agent from hammering the GitHub API.
func (r *Resolver) Latest(ctx context.Context, force bool) (*Manifest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if force && time.Since(r.lastForceFetch) < forceMinInterval {
		// Within the cooldown window: act as if force=false. The caller still
		// gets the freshest data we have without us reaching out again.
		force = false
	}
	if !force && r.cached != nil && time.Since(r.cached.CheckedAt) < time.Hour {
		return r.cached, nil
	}
	if force {
		r.lastForceFetch = time.Now()
	}
	var (
		m   *Manifest
		err error
	)
	if r.staticVer != "" {
		m = r.staticManifest()
	} else {
		m, err = r.githubManifest(ctx)
	}
	if err != nil {
		// Keep stale cache around if a refresh fails — better than 5xx.
		if r.cached != nil {
			return r.cached, nil
		}
		return nil, err
	}
	r.cached = m
	return m, nil
}

func (r *Resolver) staticManifest() *Manifest {
	m := &Manifest{
		Version:   r.staticVer,
		Channel:   "static",
		CheckedAt: time.Now().UTC(),
		Source:    "static",
		Binaries:  map[string]Binary{},
	}
	for _, key := range []string{"linux/amd64", "linux/arm64"} {
		envKey := strings.ReplaceAll(strings.ToUpper(key), "/", "_")
		url := os.Getenv("MON_AGENT_LATEST_URL_" + envKey)
		sum := os.Getenv("MON_AGENT_LATEST_SHA256_" + envKey)
		if url != "" && validHex(sum, 64) {
			m.Binaries[key] = Binary{URL: url, SHA256: strings.ToLower(sum)}
		}
	}
	return m
}

// githubManifest reads the requested release from GitHub's REST API and
// parses the SHA256SUMS asset to recover per-binary hashes. Network failures
// are surfaced; the caller may keep returning a stale cache.
func (r *Resolver) githubManifest(ctx context.Context) (*Manifest, error) {
	relURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", r.repo, r.tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, relURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := r.cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github releases: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel struct {
		Name    string `json:"name"`
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("github releases decode: %w", err)
	}

	m := &Manifest{
		Version:   firstNonEmpty(rel.TagName, rel.Name),
		Channel:   r.tag,
		CheckedAt: time.Now().UTC(),
		Source:    "github",
		Binaries:  map[string]Binary{},
	}

	// Locate SHA256SUMS asset and parse it. Format:
	//   <hash>  <filename>
	var sumsURL string
	urlByName := map[string]string{}
	for _, a := range rel.Assets {
		urlByName[a.Name] = a.BrowserDownloadURL
		if a.Name == "SHA256SUMS" {
			sumsURL = a.BrowserDownloadURL
		}
	}
	if sumsURL == "" {
		return nil, errors.New("github releases: SHA256SUMS asset missing")
	}

	sumReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	sumResp, err := r.cli.Do(sumReq)
	if err != nil {
		return nil, fmt.Errorf("SHA256SUMS fetch: %w", err)
	}
	defer sumResp.Body.Close()
	if sumResp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("SHA256SUMS fetch: HTTP %d", sumResp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(sumResp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("SHA256SUMS read: %w", err)
	}

	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		hash, name := strings.ToLower(fields[0]), fields[1]
		if !validHex(hash, 64) {
			continue
		}
		key, ok := agentKeyFromName(name)
		if !ok {
			continue
		}
		url, ok := urlByName[name]
		if !ok {
			continue
		}
		m.Binaries[key] = Binary{URL: url, SHA256: hash}
	}
	if len(m.Binaries) == 0 {
		return nil, errors.New("github releases: no matching mon-agent assets in SHA256SUMS")
	}
	return m, nil
}

// agentKeyFromName maps a release asset filename to its os/arch key. Only
// mon-agent binaries are considered; mon-server is ignored intentionally —
// this resolver serves the agent self-updater.
func agentKeyFromName(name string) (string, bool) {
	switch name {
	case "mon-agent-linux-amd64":
		return "linux/amd64", true
	case "mon-agent-linux-arm64":
		return "linux/arm64", true
	}
	return "", false
}

func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
