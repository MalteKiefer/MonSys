package notify

import (
	"strings"
	"testing"
	"time"
)

func TestBuildEmailMultipartAndEscaping(t *testing.T) {
	m := Message{
		Subject:  "High memory usage on web-01",
		Body:     "Memory above 80%.",
		Severity: "warning",
		FiredAt:  time.Date(2026, 7, 16, 8, 33, 0, 0, time.UTC),
		RuleName: "RAM > 80%",
		Host: &HostContext{
			Name:         `web-01<script>alert(1)</script>`,
			IP:           "10.0.0.5",
			OS:           "Debian 13",
			Kernel:       "7.0.13-amd64",
			Arch:         "x86_64",
			CPUCores:     8,
			RAMBytes:     16 << 30,
			AgentVersion: "v0.1.4",
			Status:       "online",
			LastSeen:     time.Date(2026, 7, 16, 8, 32, 40, 0, time.UTC),
			UptimeSec:    6*86400 + 4*3600,
		},
	}
	msg := buildEmail("mon@example.com", "ops@example.com", m)

	if !strings.Contains(msg, "multipart/alternative") {
		t.Fatal("expected multipart/alternative message")
	}
	if !strings.Contains(msg, "text/plain") || !strings.Contains(msg, "text/html") {
		t.Fatal("expected both text/plain and text/html parts")
	}

	// The HTML part is base64; decode by pulling the rendered template directly
	// and asserting the hostile hostname is escaped.
	html, err := renderAlertHTML(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("hostname was not HTML-escaped (XSS/injection risk)")
	}
	if !strings.Contains(html, "web-01&lt;script&gt;") {
		t.Fatal("expected escaped hostname in HTML")
	}
	for _, want := range []string{"Debian 13", "7.0.13-amd64", "16.0 GiB", "v0.1.4", "6d 4h", "RAM &gt; 80%"} {
		if !strings.Contains(html, want) {
			t.Fatalf("HTML missing %q", want)
		}
	}
}

func TestRenderAlertHTMLOmitsAbsentBlocks(t *testing.T) {
	// No Host / Metric: template must render without panic and omit those blocks.
	html, err := renderAlertHTML(Message{Subject: "Something happened", Body: "detail", Severity: "info"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, ">Host<") || strings.Contains(html, ">Metric<") {
		t.Fatal("expected host/metric blocks omitted when data absent")
	}
	if !strings.Contains(html, "Something happened") {
		t.Fatal("subject missing from HTML")
	}
}

func TestBuildEmailFallsBackToPlain(t *testing.T) {
	// Empty subject+body → nothing to render → plain-text-only message.
	msg := buildEmail("a@x", "b@x", Message{})
	if strings.Contains(msg, "multipart") {
		t.Fatal("empty message should fall back to plain text, not multipart")
	}
	if !strings.Contains(msg, "text/plain") {
		t.Fatal("expected plain text content type")
	}
}
