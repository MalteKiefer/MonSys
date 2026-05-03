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
	"strconv"
	"strings"
	"time"
)

// Message is the channel-agnostic shape an alert takes before formatting.
type Message struct {
	Subject string
	Body    string
	// Severity is informational and may be used by backends that support
	// priority (ntfy, Slack color). Optional.
	Severity string // info | warning | critical
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
func Dispatch(ctx context.Context, ch Channel, m Message) error {
	switch strings.ToLower(ch.Type) {
	case "smtp":
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

// httpClient is shared so HTTP-based backends benefit from connection reuse.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// --- SMTP ------------------------------------------------------------------

type SMTP struct{}

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
	body := buildRFC5322(from, rcptList, m.Subject, m.Body)

	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}

	tlsCfg := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: insecure, //nolint:gosec — opt-in for self-signed dev environments
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
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
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
	url := stringField(ch.Config, "webhook_url")
	if url == "" {
		return errors.New("slack config requires webhook_url")
	}
	color := severityColor(m.Severity)
	payload := map[string]any{
		"text": m.Subject,
		"attachments": []map[string]any{
			{"color": color, "text": m.Body},
		},
	}
	return postJSON(ctx, url, payload)
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
	url := stringField(ch.Config, "webhook_url")
	if url == "" {
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
	return postJSON(ctx, url, payload)
}

// --- Discord (incoming webhook) -------------------------------------------

// Discord webhooks accept either a flat `content` string (max 2000 chars) or
// `embeds` for rich messages. We use a single embed so severity color shows
// as a left-edge bar in the client.
type Discord struct{}

func (Discord) Send(ctx context.Context, ch Channel, m Message) error {
	url := stringField(ch.Config, "webhook_url")
	if url == "" {
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
	return postJSON(ctx, url, payload)
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
	url := strings.TrimRight(server, "/") + "/" + topic
	priority := ntfyPriority(m.Severity)

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(m.Body))
	if err != nil {
		return err
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

func postJSON(ctx context.Context, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
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
