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
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pr0ph37/mon/internal/shared/apitypes"
)

// pinHexRe matches a 64-char lowercase hex string (sha256 digest).
var pinHexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	pin     string // hex sha256 of expected server cert leaf, optional
}

type Option func(*Client) error

func WithCAFile(path string) Option {
	return func(c *Client) error {
		if path == "" {
			return nil
		}
		pem, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("ca file contained no certs")
		}
		tr := defaultTransport()
		tr.TLSClientConfig.RootCAs = pool
		c.HTTP.Transport = tr
		return nil
	}
}

func WithPin(hexSha256 string) Option {
	return func(c *Client) error {
		pin := strings.ToLower(strings.TrimSpace(hexSha256))
		if pin == "" {
			return nil
		}
		// Validate the pin format: must be a 64-char lowercase hex sha256 digest.
		if !pinHexRe.MatchString(pin) {
			return fmt.Errorf("invalid pin: expected 64-char lowercase hex sha256, got %q", hexSha256)
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
				return errors.New("no peer certs")
			}
			sum := sha256.Sum256(rawCerts[0])
			got := hex.EncodeToString(sum[:])
			if got != c.pin {
				return fmt.Errorf("server cert pin mismatch: got %s", got)
			}
			return nil
		}
		c.HTTP.Transport = tr
		return nil
	}
}

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

func defaultTransport() *http.Transport {
	return &http.Transport{
		ForceAttemptHTTP2: true,
		MaxIdleConns:      4,
		IdleConnTimeout:   60 * time.Second,
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

func New(baseURL string, opts ...Option) (*Client, error) {
	c := &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout:   30 * time.Second,
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

// Register exchanges a bootstrap token for a per-host agent_key.
func (c *Client) Register(ctx context.Context, bootstrapToken string, req apitypes.AgentRegisterRequest) (apitypes.AgentRegisterResponse, error) {
	var resp apitypes.AgentRegisterResponse
	body, err := json.Marshal(req)
	if err != nil {
		return resp, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/agents/register", bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+bootstrapToken)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return resp, err
	}
	defer httpResp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if httpResp.StatusCode/100 != 2 {
		return resp, fmt.Errorf("register: %d %s", httpResp.StatusCode, string(raw))
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return resp, fmt.Errorf("register decode: %w", err)
	}
	return resp, nil
}

// Ingest pushes a payload (already-marshalled JSON) using the agent key.
// FetchConfig pulls the resolved agent config from the server. Returns
// (nil, nil) when the server does not provide one (older server).
func (c *Client) FetchConfig(ctx context.Context, agentKey string) (*apitypes.AgentConfigResolved, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/agent/config", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+agentKey)
	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if httpResp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4<<10))
		return nil, fmt.Errorf("config fetch: %d %s", httpResp.StatusCode, string(raw))
	}
	var out apitypes.AgentConfigResolved
	if err := json.NewDecoder(httpResp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("config decode: %w", err)
	}
	return &out, nil
}

func (c *Client) Ingest(ctx context.Context, agentKey string, payload []byte) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/ingest", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+agentKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4<<10))
		return fmt.Errorf("ingest: %d %s", httpResp.StatusCode, string(raw))
	}
	_, _ = io.Copy(io.Discard, httpResp.Body)
	return nil
}
