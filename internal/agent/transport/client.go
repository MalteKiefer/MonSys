// Package transport implements the agent's outbound HTTP client to the
// mon control-plane. It exposes a small surface used by the agent core:
//
//   - Register   — exchange a bootstrap token for a per-host agent_key.
//   - FetchConfig — pull resolved per-host AgentConfig.
//   - Ingest     — push a JSON metrics/inventory payload.
//
// All requests share a single *http.Client with an explicit Timeout and a
// reused *http.Transport (HTTP/2, capped idle pool, TLS 1.2+ floor, no TLS
// session resumption so VerifyPeerCertificate fires on every connection).
//
// Idempotent verbs (GET / HEAD) are retried with exponential backoff per
// RFC 9110 §6.6, honouring the Retry-After response header on 429/503.
// POST requests are never retried by the client — the spool layer
// (internal/agent/buffer) handles durability for ingest.
package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MalteKiefer/MonSys/internal/shared/apitypes"
	"github.com/MalteKiefer/MonSys/internal/shared/version"
)

// Tunables. Exposed as constants so behaviour is greppable and reviewable.
const (
	// defaultHTTPTimeout bounds the total per-request wall time, including
	// connection, TLS, request write and response read. Chosen to be wider
	// than the server-side ingest p99 (a few hundred ms) plus headroom for
	// slow links.
	defaultHTTPTimeout = 30 * time.Second

	// defaultIdleConnTimeout closes idle keep-alive connections after this
	// period. Mirrors net/http's stdlib default but explicit here for review.
	defaultIdleConnTimeout = 60 * time.Second

	// defaultMaxIdleConns caps the global idle-conn pool. The agent only
	// talks to one server host so a small pool suffices.
	defaultMaxIdleConns = 4

	// defaultMaxConnsPerHost caps concurrent dials to the same host. The
	// agent serialises ticks, so 2 is enough for a tick + concurrent
	// FetchConfig.
	defaultMaxConnsPerHost = 2

	// maxRetryAttempts is the total number of retries on idempotent
	// requests (not counting the initial attempt). Keep small to avoid
	// queueing onto a struggling server.
	maxRetryAttempts = 3

	// minRetryBackoff / maxRetryBackoff bound the exponential backoff.
	// Schedule (without jitter): 500ms, 1s, 2s.
	minRetryBackoff = 500 * time.Millisecond
	maxRetryBackoff = 8 * time.Second

	// maxRetryAfter caps any server-supplied Retry-After value. Even if the
	// server says "come back in 10 minutes", we bound our wait so the
	// scheduler can move on.
	maxRetryAfter = 30 * time.Second

	// registerBodyReadCap limits how many bytes of a non-2xx register
	// response we slurp into the error message.
	registerBodyReadCap = 1 << 20 // 1 MiB

	// errorBodyReadCap limits how many bytes of a non-2xx response body we
	// slurp for inclusion in error messages.
	errorBodyReadCap = 4 << 10 // 4 KiB
)

// userAgent is the value sent in the User-Agent header on every request.
var userAgent = "mon-agent/" + version.Version

// pinHexRe matches a 64-char lowercase hex string (sha256 digest).
var pinHexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Client is the agent-side HTTP client to the mon control-plane. It is
// safe for concurrent use; callers should build one per agent process.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	pin     string // hex sha256 of expected server cert leaf, optional
}

// Option configures a Client at construction time. See WithCAFile,
// WithPin and WithInsecureSkipVerify.
type Option func(*Client) error

// WithCAFile loads a PEM bundle and uses it as the RootCAs for TLS
// verification. An empty path is a no-op (system roots are used).
func WithCAFile(path string) Option {
	return func(c *Client) error {
		if path == "" {
			return nil
		}
		pem, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("transport: read ca file %q: %w", path, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("transport: ca file contained no certs")
		}
		tr := defaultTransport()
		tr.TLSClientConfig.RootCAs = pool
		c.HTTP.Transport = tr
		return nil
	}
}

// WithPin replaces standard chain validation with a sha256 leaf-cert pin.
// The pin must be a 64-char lowercase hex sha256 digest. An empty pin is
// a no-op.
func WithPin(hexSha256 string) Option {
	return func(c *Client) error {
		pin := strings.ToLower(strings.TrimSpace(hexSha256))
		if pin == "" {
			return nil
		}
		// Validate the pin format: must be a 64-char lowercase hex sha256 digest.
		if !pinHexRe.MatchString(pin) {
			return fmt.Errorf("transport: invalid pin: expected 64-char lowercase hex sha256, got %q", hexSha256)
		}
		c.pin = pin
		tr, ok := c.HTTP.Transport.(*http.Transport)
		if !ok {
			tr = defaultTransport()
		}
		// Pin replaces chain validation entirely. Server cert sha256 must match the configured pin.
		// InsecureSkipVerify disables Go's default chain check so the pin check is the sole authority;
		// without this, self-signed servers fail standard chain validation BEFORE the pin check runs.
		tr.TLSClientConfig.InsecureSkipVerify = true
		// Disable TLS session resumption: resumed sessions skip certificate verification callbacks,
		// allowing a previously-pinned cert's identity to persist past rotation. We require the
		// pin check to run on every connection.
		tr.TLSClientConfig.SessionTicketsDisabled = true
		tr.TLSClientConfig.ClientSessionCache = nil
		tr.TLSClientConfig.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("transport: no peer certs")
			}
			sum := sha256.Sum256(rawCerts[0])
			got := hex.EncodeToString(sum[:])
			if got != c.pin {
				return fmt.Errorf("transport: server cert pin mismatch: got %s", got)
			}
			return nil
		}
		c.HTTP.Transport = tr
		return nil
	}
}

// WithInsecureSkipVerify disables TLS chain validation. Dev-only escape
// hatch; production deployments must use WithCAFile or WithPin instead.
func WithInsecureSkipVerify() Option {
	return func(c *Client) error {
		tr, ok := c.HTTP.Transport.(*http.Transport)
		if !ok {
			tr = defaultTransport()
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
		c.HTTP.Transport = tr
		return nil
	}
}

// defaultTransport returns the shared *http.Transport template used by
// every Client. Each constructor call returns a fresh value because
// Options mutate the transport in-place; the underlying tuning is the
// same for all callers.
func defaultTransport() *http.Transport {
	return &http.Transport{
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        defaultMaxIdleConns,
		MaxIdleConnsPerHost: defaultMaxIdleConns,
		MaxConnsPerHost:     defaultMaxConnsPerHost,
		IdleConnTimeout:     defaultIdleConnTimeout,
		// Disable TLS session resumption: resumed handshakes bypass VerifyPeerCertificate,
		// which would defeat any custom verification (including pinning). Force a full
		// handshake on every connection so cert checks always run.
		TLSClientConfig: &tls.Config{
			MinVersion:             tls.VersionTLS12,
			SessionTicketsDisabled: true,
			ClientSessionCache:     nil,
		},
	}
}

// New constructs a Client with the shared transport tuning baked in and
// applies the supplied Options in order. The returned Client may be
// reused for the lifetime of the agent process.
func New(baseURL string, opts ...Option) (*Client, error) {
	c := &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: defaultTransport(),
		},
	}
	for _, o := range opts {
		if err := o(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// setStandardHeaders applies the headers every request must carry:
// User-Agent (version-stamped) and any caller-supplied bearer.
func setStandardHeaders(req *http.Request, bearer string) {
	req.Header.Set("User-Agent", userAgent)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
}

// Register exchanges a bootstrap token for a per-host agent_key. POSTs
// are not retried by the client; callers re-drive registration from the
// outer loop if needed.
func (c *Client) Register(ctx context.Context, bootstrapToken string, req apitypes.AgentRegisterRequest) (apitypes.AgentRegisterResponse, error) {
	var resp apitypes.AgentRegisterResponse
	body, err := json.Marshal(req)
	if err != nil {
		return resp, fmt.Errorf("transport: marshal register: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/agents/register", bytes.NewReader(body))
	if err != nil {
		return resp, fmt.Errorf("transport: build register request: %w", err)
	}
	setStandardHeaders(httpReq, bootstrapToken)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return resp, fmt.Errorf("transport: register: %w", err)
	}
	defer httpResp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, registerBodyReadCap))
	if httpResp.StatusCode/100 != 2 {
		return resp, fmt.Errorf("transport: register: %d %s", httpResp.StatusCode, string(raw))
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, fmt.Errorf("transport: register decode: %w", err)
	}
	return resp, nil
}

// FetchConfig pulls the resolved agent config from the server. Returns
// (nil, nil) when the server does not provide one (older server). GETs
// are retried per the package-level retry policy.
func (c *Client) FetchConfig(ctx context.Context, agentKey string) (*apitypes.AgentConfigResolved, error) {
	httpResp, err := c.doIdempotent(ctx, http.MethodGet, c.BaseURL+"/v1/agent/config", nil, agentKey, "")
	if err != nil {
		return nil, fmt.Errorf("transport: fetch config: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if httpResp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, errorBodyReadCap))
		return nil, fmt.Errorf("transport: config fetch: %d %s", httpResp.StatusCode, string(raw))
	}
	var out apitypes.AgentConfigResolved
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("transport: config decode: %w", err)
	}
	return &out, nil
}

// Ingest pushes a payload (already-marshalled JSON) using the agent key.
// POSTs are not retried — the buffer.Spool durably re-drives this on the
// next tick.
func (c *Client) Ingest(ctx context.Context, agentKey string, payload []byte) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/ingest", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("transport: build ingest request: %w", err)
	}
	setStandardHeaders(httpReq, agentKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("transport: ingest: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, errorBodyReadCap))
		return fmt.Errorf("transport: ingest: %d %s", httpResp.StatusCode, string(raw))
	}
	_, _ = io.Copy(io.Discard, httpResp.Body)
	return nil
}

// Deactivate calls POST /v1/agents/self/deactivate so the running agent
// revokes its own key. The POST is *not* retried — the agent CLI runs this
// once on shutdown and the operator can re-invoke on failure.
func (c *Client) Deactivate(ctx context.Context, agentKey string) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/agents/self/deactivate", http.NoBody)
	if err != nil {
		return fmt.Errorf("transport: build deactivate request: %w", err)
	}
	setStandardHeaders(httpReq, agentKey)
	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("transport: deactivate: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, errorBodyReadCap))
		return fmt.Errorf("transport: deactivate: %d %s", httpResp.StatusCode, string(raw))
	}
	_, _ = io.Copy(io.Discard, httpResp.Body)
	return nil
}

// Delete calls DELETE /v1/agents/self so the agent removes its own host
// record. DELETE is idempotent so the shared retry path is used.
func (c *Client) Delete(ctx context.Context, agentKey string) error {
	httpResp, err := c.doIdempotent(ctx, http.MethodDelete, c.BaseURL+"/v1/agents/self", nil, agentKey, "")
	if err != nil {
		return fmt.Errorf("transport: delete: %w", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, errorBodyReadCap))
		return fmt.Errorf("transport: delete: %d %s", httpResp.StatusCode, string(raw))
	}
	_, _ = io.Copy(io.Discard, httpResp.Body)
	return nil
}

// doIdempotent issues an HTTP request that is safe to retry per RFC 9110
// §9.2.2 (GET, HEAD, PUT, DELETE). It retries on transport errors and on
// 429/5xx responses with exponential backoff, honouring a clamped
// Retry-After header when present.
//
// body is intentionally restricted to nil for GET/HEAD; callers wishing
// to retry methods with bodies must materialise the body and rebuild the
// request each attempt themselves.
func (c *Client) doIdempotent(ctx context.Context, method, url string, body io.Reader, bearer, contentType string) (*http.Response, error) {
	if body != nil && (method == http.MethodGet || method == http.MethodHead) {
		// Defensive: catches programmer error early.
		return nil, fmt.Errorf("transport: %s must not have a body", method)
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetryAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return nil, fmt.Errorf("transport: build request: %w", err)
		}
		setStandardHeaders(req, bearer)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			if !shouldRetryErr(ctx, err) || attempt == maxRetryAttempts {
				return nil, err
			}
			if werr := wait(ctx, backoffFor(attempt, 0)); werr != nil {
				return nil, werr
			}
			continue
		}

		if !retryableStatus(resp.StatusCode) || attempt == maxRetryAttempts {
			return resp, nil
		}

		// Drain and close so the connection can be reused for the retry.
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, errorBodyReadCap))
		_ = resp.Body.Close()

		lastErr = fmt.Errorf("transport: status %d (attempt %d)", resp.StatusCode, attempt+1)
		if werr := wait(ctx, backoffFor(attempt, retryAfter)); werr != nil {
			return nil, werr
		}
	}
	if lastErr == nil {
		lastErr = errors.New("transport: retries exhausted")
	}
	return nil, lastErr
}

// retryableStatus reports whether an HTTP status code is worth retrying.
// 429 Too Many Requests and the 5xx range qualify; 501 Not Implemented
// is excluded because a retry will deterministically produce the same
// response.
func retryableStatus(code int) bool {
	if code == http.StatusTooManyRequests {
		return true
	}
	if code >= 500 && code <= 599 && code != http.StatusNotImplemented {
		return true
	}
	return false
}

// shouldRetryErr distinguishes "this might succeed if we try again"
// (network blip) from "no point" (context cancelled, or a permanent
// failure like cert verification or NXDOMAIN). The stdlib does not
// surface a clean predicate so we sniff context cancellation and then
// defer to isPermanentNetError for the deterministic-failure classes.
func shouldRetryErr(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	// audit 2026-05-12 §4.3.11: RFC 9110 §9.2.2 — automatically-retryable
	// failures only. TLS verification failures, unknown CA, invalid cert,
	// and DNS NXDOMAIN won't get better on retry, so spending the retry
	// budget on them just slows down the eventual surfaced error.
	if isPermanentNetError(err) {
		return false
	}
	return true
}

// isPermanentNetError reports whether err is a deterministic, non-retryable
// network failure: a TLS chain verification failure, an unknown-CA or
// invalid-certificate error from crypto/x509, or a DNS not-found result.
// These will produce the same outcome on every retry, so the retry budget
// should be spent elsewhere.
//
// audit 2026-05-12 §4.3.11: introduced.
func isPermanentNetError(err error) bool {
	if err == nil {
		return false
	}
	// TLS chain verification failure. The Go stdlib wraps the underlying
	// x509 cause inside *tls.CertificateVerificationError, so errors.As
	// matches even when the error is several wraps deep.
	var tlsVerifyErr *tls.CertificateVerificationError
	if errors.As(err, &tlsVerifyErr) {
		return true
	}
	// Unknown / untrusted CA. Note: x509.UnknownAuthorityError is a struct
	// type (not a sentinel), so errors.As is the right tool.
	var unknownAuthErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthErr) {
		return true
	}
	// Cert was malformed / expired / not-yet-valid / wrong key usage etc.
	var certInvalidErr x509.CertificateInvalidError
	if errors.As(err, &certInvalidErr) {
		return true
	}
	// Hostname-vs-SAN mismatch. Permanent until the cert or hostname
	// changes; retrying inside the same Run loop won't fix it.
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return true
	}
	// DNS NXDOMAIN. Other *net.DNSError instances (e.g. transient resolver
	// timeouts with IsTemporary true, or IsTimeout) are still retryable.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return true
	}
	return false
}

// backoffFor returns the wait before the next attempt. Server-supplied
// Retry-After always wins (clamped to maxRetryAfter). Otherwise an
// exponential schedule with full jitter, bounded by [minRetryBackoff,
// maxRetryBackoff].
func backoffFor(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		if retryAfter > maxRetryAfter {
			return maxRetryAfter
		}
		return retryAfter
	}
	d := minRetryBackoff << attempt
	if d > maxRetryBackoff {
		d = maxRetryBackoff
	}
	// Full jitter: pick a random duration in [0, d). Avoids thundering
	// herd when many agents retry on the same wall-clock tick.
	//nolint:gosec // jitter does not need crypto randomness
	return time.Duration(rand.Int63n(int64(d) + 1))
}

// parseRetryAfter handles both the delta-seconds and HTTP-date forms of
// Retry-After per RFC 9110 §10.2.3.
func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// wait sleeps for d but bails out early when ctx is cancelled.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
