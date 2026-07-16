// Package notify dispatches alerts to operator-configured channels.
//
// Each backend (SMTP, Slack, Mattermost, ntfy) is a small Sender. Backends
// don't talk to the database; they accept a parsed channel record and a
// Message and report success/failure. The store layer wraps Send() to also
// record last_used_at / last_error for observability.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Message is the channel-agnostic shape an alert takes before formatting.
type Message struct {
	Subject string
	Body    string
	// Severity is informational and may be used by backends that support
	// priority (ntfy, Slack color). Optional.
	Severity string // info | warning | critical

	// The fields below are optional structured context. They drive the rich
	// HTML email (SMTP channel); Subject/Body remain the canonical plain-text
	// form every channel uses. All are safe to leave zero — the HTML template
	// omits any block whose data is absent.
	FiredAt  time.Time     // when the alert fired (zero = omit)
	RuleName string        // the rule that tripped
	Metric   *MetricDetail // populated for metric-threshold alerts
	Host     *HostContext  // populated for host-scoped alerts
	HostURL  string        // deep link to the host page (set when MON_PUBLIC_URL is configured)
}

// MetricDetail describes the metric that tripped a threshold rule.
type MetricDetail struct {
	Name      string // e.g. "ram_used_pct"
	Current   string // formatted current value, e.g. "87.4%"
	Threshold string // formatted threshold, e.g. "> 80%"
}

// HostContext carries human-readable facts about the affected host for the
// email body. Zero fields are omitted from the rendered table.
type HostContext struct {
	Name         string
	IP           string
	OS           string // distro, e.g. "Debian 13"
	Kernel       string
	Arch         string
	CPUCores     int
	RAMBytes     int64
	AgentVersion string
	Status       string // "online" | "offline" | "unknown"
	LastSeen     time.Time
	UptimeSec    int64
}

// Channel is the minimal projection of a notification_channels row that
// dispatch needs. Use store.notificationChannelToChannel to construct it.
type Channel struct {
	ID     string
	Type   string
	Name   string
	Config map[string]any
}

type Sender interface {
	Send(ctx context.Context, ch Channel, m Message) error
}

// Dispatch picks the right Sender for ch.Type. Unknown types return an error
// rather than silently succeeding — operators want loud failures.
//
// "email" is the per-user channel type; the dispatch layer is expected to
// merge the global SMTP settings into ch.Config before calling. "smtp" stays
// supported as an alias so older clients keep working.
func Dispatch(ctx context.Context, ch Channel, m Message) error {
	switch strings.ToLower(ch.Type) {
	case "email", "smtp":
		return SMTP{}.Send(ctx, ch, m)
	case "slack":
		return Slack{}.Send(ctx, ch, m)
	case "mattermost":
		return Mattermost{}.Send(ctx, ch, m)
	case "discord":
		return Discord{}.Send(ctx, ch, m)
	case "ntfy":
		return Ntfy{}.Send(ctx, ch, m)
	}
	return fmt.Errorf("notify: unsupported channel type %q", ch.Type)
}

// webhookHardDenyCIDRs are destinations an outbound webhook must never reach:
// loopback and link-local, most importantly the cloud instance metadata
// endpoint (169.254.169.254). RFC1918/ULA is intentionally NOT blocked —
// self-hosted Slack/Mattermost/ntfy on private networks is a supported target
// for this self-hosted tool (audit 2026-07-16 H3).
var webhookHardDenyCIDRs = func() []*net.IPNet {
	out := make([]*net.IPNet, 0, 4)
	for _, c := range []string{"127.0.0.0/8", "169.254.0.0/16", "::1/128", "fe80::/10"} {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}()

// denyDial rejects a connection whose resolved address is in a hard-denied
// range. Because it runs on the post-resolution address, it also defeats DNS
// rebinding and re-checks every redirect hop's target.
func denyDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	for _, n := range webhookHardDenyCIDRs {
		if n.Contains(ip) {
			return fmt.Errorf("webhook: destination %s is in a denied range (SSRF guard)", host)
		}
	}
	return nil
}

// validateWebhookScheme rejects any URL that is not plain http/https, so a
// channel cannot be pointed at file://, gopher://, etc.
func validateWebhookScheme(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid webhook url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook url scheme %q not allowed (use http or https)", u.Scheme)
	}
	return nil
}

// httpClient is shared so HTTP-based backends benefit from connection reuse.
// Its dialer blocks SSRF-prone targets and it caps redirects at 10, re-checking
// each hop through the same dialer.
var httpClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	},
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, Control: denyDial}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// --- SMTP ------------------------------------------------------------------

type SMTP struct{}

//nolint:cyclop // SMTP envelope construction is inherently a sequence of guarded field reads (host/port/user/pass/from/to/tls-mode/auth-mode/timeouts/body) feeding net/smtp. Each branch is a one-line precondition or a header write; splitting would scatter the message-build sequence across helpers that all touch the same builder.
func (SMTP) Send(ctx context.Context, ch Channel, m Message) error {
	host := stringField(ch.Config, "host")
	port := intField(ch.Config, "port", 587)
	user := stringField(ch.Config, "username")
	pass := stringField(ch.Config, "password")
	from := stringField(ch.Config, "from")
	tos := stringSliceField(ch.Config, "to")
	useStarttls := boolField(ch.Config, "starttls", true)
	useTLS := boolField(ch.Config, "tls", false)
	insecure := boolField(ch.Config, "insecure_skip_verify", false)

	if host == "" || from == "" || len(tos) == 0 {
		return errors.New("smtp config requires host, from, to[]")
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	rcptList := strings.Join(tos, ", ")
	body := buildEmail(from, rcptList, m)

	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}

	tlsCfg := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: insecure, //nolint:gosec // opt-in for self-signed dev environments
		MinVersion:         tls.VersionTLS12,
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(20 * time.Second)
	}

	var conn net.Conn
	var err error
	if useTLS {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: tlsCfg}
		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	if useStarttls && !useTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp MAIL: %w", err)
	}
	for _, to := range tos {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("smtp RCPT %s: %w", to, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return fmt.Errorf("smtp body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return c.Quit()
}

func buildRFC5322(from, to, subject, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

// --- Slack-incoming-webhook ------------------------------------------------

type Slack struct{}

func (Slack) Send(ctx context.Context, ch Channel, m Message) error {
	endpoint := stringField(ch.Config, "webhook_url")
	if endpoint == "" {
		return errors.New("slack config requires webhook_url")
	}
	color := severityColor(m.Severity)
	payload := map[string]any{
		"text": m.Subject,
		"attachments": []map[string]any{
			{"color": color, "text": m.Body},
		},
	}
	return postJSON(ctx, endpoint, payload)
}

func severityColor(s string) string {
	switch strings.ToLower(s) {
	case "critical":
		return "#cc0000"
	case "warning":
		return "#e8a23a"
	default:
		return "#3aa3e8"
	}
}

// --- Mattermost-incoming-webhook ------------------------------------------

type Mattermost struct{}

func (Mattermost) Send(ctx context.Context, ch Channel, m Message) error {
	endpoint := stringField(ch.Config, "webhook_url")
	if endpoint == "" {
		return errors.New("mattermost config requires webhook_url")
	}
	username := stringField(ch.Config, "username")
	if username == "" {
		username = "mon"
	}
	payload := map[string]any{
		"username": username,
		"text":     fmt.Sprintf("**%s**\n%s", m.Subject, m.Body),
	}
	return postJSON(ctx, endpoint, payload)
}

// --- Discord (incoming webhook) -------------------------------------------

// Discord webhooks accept either a flat `content` string (max 2000 chars) or
// `embeds` for rich messages. We use a single embed so severity color shows
// as a left-edge bar in the client.
type Discord struct{}

func (Discord) Send(ctx context.Context, ch Channel, m Message) error {
	endpoint := stringField(ch.Config, "webhook_url")
	if endpoint == "" {
		return errors.New("discord config requires webhook_url")
	}
	username := stringField(ch.Config, "username")
	if username == "" {
		username = "mon"
	}
	body := m.Body
	// Discord caps embed.description at 4096 chars; truncate well below.
	if len(body) > 3500 {
		body = body[:3500] + "…"
	}
	payload := map[string]any{
		"username": username,
		"embeds": []map[string]any{
			{
				"title":       m.Subject,
				"description": body,
				"color":       discordColor(m.Severity),
			},
		},
	}
	return postJSON(ctx, endpoint, payload)
}

// discordColor returns the integer color value Discord expects (0xRRGGBB).
func discordColor(s string) int {
	switch strings.ToLower(s) {
	case "critical":
		return 0xCC0000
	case "warning":
		return 0xE8A23A
	case "info":
		return 0x3AA3E8
	}
	return 0x3AA3E8
}

// --- ntfy ------------------------------------------------------------------

type Ntfy struct{}

func (Ntfy) Send(ctx context.Context, ch Channel, m Message) error {
	server := stringField(ch.Config, "server_url")
	if server == "" {
		server = "https://ntfy.sh"
	}
	topic := stringField(ch.Config, "topic")
	if topic == "" {
		return errors.New("ntfy config requires topic")
	}
	endpoint := strings.TrimRight(server, "/") + "/" + topic
	if err := validateWebhookScheme(endpoint); err != nil {
		return err
	}
	priority := ntfyPriority(m.Severity)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(m.Body))
	if err != nil {
		return fmt.Errorf("ntfy: build request: %w", err)
	}
	req.Header.Set("Title", m.Subject)
	req.Header.Set("Priority", priority)
	if tok := stringField(ch.Config, "auth_token"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if user := stringField(ch.Config, "username"); user != "" {
		req.SetBasicAuth(user, stringField(ch.Config, "password"))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("ntfy: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func ntfyPriority(s string) string {
	switch strings.ToLower(s) {
	case "critical":
		return "5"
	case "warning":
		return "4"
	case "info":
		return "3"
	}
	return "3"
}

// --- shared HTTP helper ----------------------------------------------------

func postJSON(ctx context.Context, rawURL string, payload any) error {
	if err := validateWebhookScheme(rawURL); err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webhook: status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// --- config helpers --------------------------------------------------------

func stringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func intField(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return def
}

func boolField(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

func stringSliceField(m map[string]any, key string) []string {
	switch v := m[key].(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case string:
		// Allow comma-separated form for convenience.
		out := []string{}
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}
