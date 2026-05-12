package store

import (
	"strings"
	"testing"
	"time"

	libwa "github.com/go-webauthn/webauthn/webauthn"
)

// TestPasskeyNameHasControlChars locks in the AUDIT-018 contract: ASCII C0
// (< 0x20) and DEL (0x7F) bytes must be rejected. Everything else passes —
// the function is intentionally Unicode-friendly so UTF-8 labels with
// emoji or accents survive.
func TestPasskeyNameHasControlChars(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain ascii", "MacBook", false},
		{"unicode latin", "Maltés", false},
		{"emoji", "phone 📱", false},
		{"with tab", "Mac\tBook", true},
		{"with newline", "Mac\nBook", true},
		{"with NUL", "Mac\x00Book", true},
		{"with DEL", "Mac\x7FBook", true},
		{"with BS", "Mac\x08Book", true},
		{"with ESC", "Mac\x1bBook", true},
		{"empty", "", false},
	}
	for _, c := range cases {
		got := passkeyNameHasControlChars(c.in)
		if got != c.want {
			t.Errorf("[%s] passkeyNameHasControlChars(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// TestTruncate200 verifies the rune-aware audit-log truncation helper. Must
// cap to 200 runes (not bytes), so multi-byte input survives without
// half-cutting code points.
func TestTruncate200(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int // expected rune count after truncation
	}{
		{"short ascii", "hello", 5},
		{"exactly 200 ascii", strings.Repeat("a", 200), 200},
		{"over 200 ascii", strings.Repeat("a", 250), 200},
		{"multi-byte safe", strings.Repeat("ü", 300), 200},
		{"empty", "", 0},
	}
	for _, c := range cases {
		got := truncate200(c.in)
		if r := len([]rune(got)); r != c.want {
			t.Errorf("[%s] truncate200 returned %d runes, want %d", c.name, r, c.want)
		}
	}
}

// TestPasskeyLoginSessions_PutTake exercises the in-memory discoverable-login
// state map: put then take returns the same SessionData; second take returns
// false (single-use). Expiry shorter than zero should also produce a miss
// because the entry is immediately stale.
func TestPasskeyLoginSessions_PutTake(t *testing.T) {
	p := &passkeyLoginSessions{sessions: make(map[string]passkeyLoginEntry)}
	sd := libwa.SessionData{Challenge: "challenge-bytes"}
	p.put("tok1", sd, time.Minute)

	got, ok := p.take("tok1")
	if !ok {
		t.Fatalf("first take returned ok=false")
	}
	if got.Challenge != sd.Challenge {
		t.Fatalf("take returned wrong SessionData: got %+v", got)
	}

	// Second take must miss — sessions are single-use.
	if _, ok := p.take("tok1"); ok {
		t.Fatalf("second take returned ok=true; sessions must be single-use")
	}
}

// TestPasskeyLoginSessions_Expired makes sure take refuses entries past their
// expiry deadline.
func TestPasskeyLoginSessions_Expired(t *testing.T) {
	p := &passkeyLoginSessions{sessions: make(map[string]passkeyLoginEntry)}
	// Insert manually with a past expiry so we don't need to sleep.
	p.sessions["tok"] = passkeyLoginEntry{
		data:      libwa.SessionData{Challenge: "x"},
		expiresAt: time.Now().Add(-time.Second),
	}
	if _, ok := p.take("tok"); ok {
		t.Fatalf("take returned ok=true for expired entry")
	}
}

// TestPasskeyLoginSessions_GC removes only expired sessions.
func TestPasskeyLoginSessions_GC(t *testing.T) {
	p := &passkeyLoginSessions{sessions: make(map[string]passkeyLoginEntry)}
	p.sessions["live"] = passkeyLoginEntry{
		data:      libwa.SessionData{Challenge: "a"},
		expiresAt: time.Now().Add(time.Hour),
	}
	p.sessions["dead"] = passkeyLoginEntry{
		data:      libwa.SessionData{Challenge: "b"},
		expiresAt: time.Now().Add(-time.Hour),
	}
	p.gc()
	if _, ok := p.sessions["dead"]; ok {
		t.Errorf("gc kept expired entry")
	}
	if _, ok := p.sessions["live"]; !ok {
		t.Errorf("gc removed live entry")
	}
}
