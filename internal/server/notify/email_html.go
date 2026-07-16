package notify

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// buildEmail renders the RFC 5322 message for an alert. When an HTML body can
// be produced it emits a multipart/alternative (plain + HTML); otherwise it
// falls back to the plain-text-only message. Both parts are base64-encoded so
// arbitrary content (long inline-styled HTML lines, leading dots) transfers
// safely regardless of the SMTP writer's line handling.
func buildEmail(from, to string, m Message) string {
	htmlBody, err := renderAlertHTML(m)
	if err != nil || strings.TrimSpace(htmlBody) == "" {
		return buildRFC5322(from, to, m.Subject, m.Body)
	}

	boundary := mimeBoundary()
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", m.Subject)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)

	writePart := func(contentType, body string) {
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s; charset=UTF-8\r\n", contentType)
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(base64Wrapped(body))
		b.WriteString("\r\n")
	}
	writePart("text/plain", m.Body)
	writePart("text/html", htmlBody)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String()
}

func mimeBoundary() string {
	var buf [18]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// A fixed fallback is acceptable: collision with body content is only
		// a formatting risk, and the base64 encoding of parts avoids it.
		return "mon-alert-boundary-fixed"
	}
	return "mon_" + base64.RawURLEncoding.EncodeToString(buf[:])
}

// base64Wrapped encodes s and hard-wraps at 76 columns per MIME.
func base64Wrapped(s string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76])
		b.WriteString("\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	return b.String()
}

// alertColors maps a severity to (accent, pill-bg, pill-fg).
func alertColors(severity string) (accent, pillBG, pillFG string) {
	switch strings.ToLower(severity) {
	case "critical":
		return "#b91c1c", "#fbdada", "#8a1414"
	case "info":
		return "#3aa3e8", "#d7ecfb", "#155e88"
	default: // warning
		return "#e8a23a", "#fbe7cd", "#8a5a12"
	}
}

type alertTemplateData struct {
	Message
	Accent      string
	PillBG      string
	PillFG      string
	Title       string
	Severity    string
	FiredStr    string
	RAMHuman    string
	UptimeStr   string
	LastSeenStr string
	HasHost     bool
	HasMeta     bool
}

var alertTmpl = template.Must(template.New("alert").Parse(alertHTML))

// renderAlertHTML renders the alert email body. Returns an empty string with a
// nil error when there is nothing to render (caller falls back to plain text).
func renderAlertHTML(m Message) (string, error) {
	if strings.TrimSpace(m.Subject) == "" && strings.TrimSpace(m.Body) == "" {
		return "", nil
	}
	accent, pillBG, pillFG := alertColors(m.Severity)
	sev := strings.ToUpper(m.Severity)
	if sev == "" {
		sev = "ALERT"
	}
	data := alertTemplateData{
		Message:  m,
		Accent:   accent,
		PillBG:   pillBG,
		PillFG:   pillFG,
		Title:    m.Subject,
		Severity: sev,
		HasHost:  m.Host != nil,
		HasMeta:  m.Metric != nil,
	}
	if !m.FiredAt.IsZero() {
		data.FiredStr = m.FiredAt.UTC().Format("2006-01-02 15:04 MST")
	}
	if m.Host != nil {
		if m.Host.RAMBytes > 0 {
			data.RAMHuman = humanBytes(m.Host.RAMBytes)
		}
		data.UptimeStr = humanUptime(m.Host.UptimeSec)
		if !m.Host.LastSeen.IsZero() {
			data.LastSeenStr = m.Host.LastSeen.UTC().Format("2006-01-02 15:04 MST")
		}
	}
	var buf bytes.Buffer
	if err := alertTmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func humanUptime(sec int64) string {
	if sec <= 0 {
		return ""
	}
	d := time.Duration(sec) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
}

// alertHTML is an email-safe, table-based, inline-styled template. All dynamic
// values are auto-escaped by html/template.
const alertHTML = `<div style="background:#eef1f5;padding:24px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:600px;margin:0 auto;background:#ffffff;border-radius:12px;overflow:hidden;">
<tr><td style="height:6px;background:{{.Accent}};"></td></tr>
<tr><td style="padding:20px 28px 0;">
  <table role="presentation" width="100%"><tr>
    <td style="font-weight:700;font-size:15px;color:#0f172a;">&#9670; MonSys</td>
    <td align="right"><span style="display:inline-block;background:{{.PillBG}};color:{{.PillFG}};font-size:11px;font-weight:700;letter-spacing:.6px;padding:4px 10px;border-radius:999px;">{{.Severity}}</span></td>
  </tr></table>
</td></tr>
<tr><td style="padding:14px 28px 4px;">
  <div style="font-size:20px;font-weight:700;color:#0f172a;line-height:1.3;">{{.Title}}</div>
  <p style="margin:10px 0 0;font-size:14px;line-height:1.55;color:#334155;white-space:pre-line;">{{.Body}}</p>
</td></tr>
{{if .HasMeta}}<tr><td style="padding:16px 28px 0;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f8fafc;border:1px solid #e2e8f0;border-radius:10px;"><tr>
    <td style="padding:12px 16px;border-right:1px solid #e2e8f0;"><div style="font-size:11px;color:#64748b;text-transform:uppercase;letter-spacing:.5px;">Metric</div><div style="font-size:14px;color:#0f172a;font-weight:600;margin-top:2px;">{{.Metric.Name}}</div></td>
    <td style="padding:12px 16px;border-right:1px solid #e2e8f0;"><div style="font-size:11px;color:#64748b;text-transform:uppercase;letter-spacing:.5px;">Current</div><div style="font-size:14px;color:{{.Accent}};font-weight:700;margin-top:2px;">{{.Metric.Current}}</div></td>
    <td style="padding:12px 16px;"><div style="font-size:11px;color:#64748b;text-transform:uppercase;letter-spacing:.5px;">Threshold</div><div style="font-size:14px;color:#0f172a;font-weight:600;margin-top:2px;">{{.Metric.Threshold}}</div></td>
  </tr></table>
</td></tr>{{end}}
{{if .HasHost}}<tr><td style="padding:18px 28px 0;">
  <div style="font-size:12px;color:#64748b;text-transform:uppercase;letter-spacing:.5px;margin-bottom:8px;">Host</div>
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:13px;color:#334155;">
    {{with .Host}}
    {{if .Name}}<tr><td style="padding:5px 0;color:#64748b;width:130px;">Hostname</td><td style="padding:5px 0;font-weight:600;color:#0f172a;">{{.Name}}</td></tr>{{end}}
    {{if .IP}}<tr><td style="padding:5px 0;color:#64748b;">IP address</td><td style="padding:5px 0;">{{.IP}}</td></tr>{{end}}
    {{if .OS}}<tr><td style="padding:5px 0;color:#64748b;">OS / kernel</td><td style="padding:5px 0;">{{.OS}}{{if .Kernel}} &middot; {{.Kernel}}{{end}}</td></tr>{{end}}
    {{if .Arch}}<tr><td style="padding:5px 0;color:#64748b;">Arch &middot; CPU &middot; RAM</td><td style="padding:5px 0;">{{.Arch}}{{if .CPUCores}} &middot; {{.CPUCores}} cores{{end}}{{if $.RAMHuman}} &middot; {{$.RAMHuman}}{{end}}</td></tr>{{end}}
    {{if .AgentVersion}}<tr><td style="padding:5px 0;color:#64748b;">Agent</td><td style="padding:5px 0;">{{.AgentVersion}}</td></tr>{{end}}
    {{if .Status}}<tr><td style="padding:5px 0;color:#64748b;">Status</td><td style="padding:5px 0;">{{.Status}}{{if $.LastSeenStr}} &middot; last seen {{$.LastSeenStr}}{{end}}</td></tr>{{end}}
    {{if $.UptimeStr}}<tr><td style="padding:5px 0;color:#64748b;">Uptime</td><td style="padding:5px 0;">{{$.UptimeStr}}</td></tr>{{end}}
    {{end}}
  </table>
</td></tr>{{end}}
{{if .HostURL}}<tr><td style="padding:22px 28px 4px;">
  <a href="{{.HostURL}}" style="display:inline-block;background:#0f172a;color:#ffffff;text-decoration:none;font-size:13px;font-weight:600;padding:10px 18px;border-radius:8px;">View host in MonSys &rarr;</a>
</td></tr>{{end}}
<tr><td style="padding:20px 28px 24px;">
  <hr style="border:none;border-top:1px solid #e2e8f0;margin:0 0 12px;">
  <p style="margin:0;font-size:11px;line-height:1.5;color:#94a3b8;">{{if .RuleName}}Rule <strong style="color:#64748b;">{{.RuleName}}</strong> &middot; {{end}}severity {{.Message.Severity}}{{if .FiredStr}} &middot; fired {{.FiredStr}}{{end}}</p>
</td></tr>
</table>
<p style="max-width:600px;margin:12px auto 0;font-size:11px;color:#94a3b8;text-align:center;">MonSys &middot; self-hosted server monitoring</p>
</div>`
