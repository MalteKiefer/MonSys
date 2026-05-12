package api

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/MalteKiefer/MonSys/internal/server/store"
)

// TestBearer walks the public bearer() helper through every branch the
// fuzz harness left implicit: missing prefix, mixed-case prefix (rejected
// — the spec wants exact "Bearer "), whitespace-only token, the canonical
// happy path. The fuzz test already covers UTF-8 invariants and panic
// freedom; this lock-down spells out the exact wire contract.
func TestBearer(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		wantTok string
		wantOK  bool
	}{
		{"happy", "Bearer mon_ag_abc", "mon_ag_abc", true},
		{"trims trailing space", "Bearer mon_ag_abc  ", "mon_ag_abc", true},
		{"trims leading tabs", "Bearer \tabc", "abc", true},
		{"empty header", "", "", false},
		{"missing prefix", "mon_ag_abc", "", false},
		{"lowercase prefix rejected", "bearer abc", "", false},
		{"prefix only", "Bearer ", "", false},
		{"prefix + whitespace only", "Bearer   \t  ", "", false},
		{"Basic instead", "Basic dXNlcjpwYXNz", "", false},
	}
	for _, c := range cases {
		gotTok, gotOK := bearer(c.header)
		if gotOK != c.wantOK {
			t.Errorf("[%s] bearer(%q) ok=%v, want %v (token=%q)", c.name, c.header, gotOK, c.wantOK, gotTok)
		}
		if gotTok != c.wantTok {
			t.Errorf("[%s] bearer(%q) token=%q, want %q", c.name, c.header, gotTok, c.wantTok)
		}
	}
}

// TestUserFromContext_RoundTrip stashes a store.User under ctxKeyUser and
// pulls it back out via the public accessor. Mirrors the requireUser→
// handler hand-off.
func TestUserFromContext_RoundTrip(t *testing.T) {
	want := store.User{
		ID:    uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Email: "alice@example.com",
		Role:  "admin",
	}

	ctx := context.WithValue(context.Background(), ctxKeyUser, want)
	got, ok := userFromContext(ctx)
	if !ok {
		t.Fatal("userFromContext returned ok=false on populated context")
	}
	if got.ID != want.ID || got.Email != want.Email || got.Role != want.Role {
		t.Errorf("user mismatch: got %+v, want %+v", got, want)
	}
}

// TestUserFromContext_Missing returns (zero, false) on an empty context.
func TestUserFromContext_Missing(t *testing.T) {
	if u, ok := userFromContext(context.Background()); ok {
		t.Errorf("expected ok=false on empty context, got user %+v", u)
	}
}

// TestUserFromContext_WrongType returns ok=false when something other than
// a store.User is stashed under the key — defensive cast keeps a panic out
// of the request hot path if the chain is assembled wrong.
func TestUserFromContext_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyUser, "not a user")
	if _, ok := userFromContext(ctx); ok {
		t.Errorf("expected ok=false when context value is wrong type")
	}
}

// TestTokenFromContext_RoundTrip mirrors TestUserFromContext_RoundTrip for
// the session-token slot.
func TestTokenFromContext_RoundTrip(t *testing.T) {
	want := "mon_sess_abc123"
	ctx := context.WithValue(context.Background(), ctxKeyToken, want)

	got, ok := tokenFromContext(ctx)
	if !ok {
		t.Fatal("tokenFromContext returned ok=false on populated context")
	}
	if got != want {
		t.Errorf("token = %q, want %q", got, want)
	}
}

// TestTokenFromContext_Missing returns ("", false) on bare context.
func TestTokenFromContext_Missing(t *testing.T) {
	if tok, ok := tokenFromContext(context.Background()); ok {
		t.Errorf("expected ok=false on empty context, got %q", tok)
	}
}

// TestTokenFromContext_WrongType refuses non-string values stashed under
// ctxKeyToken.
func TestTokenFromContext_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyToken, 42)
	if _, ok := tokenFromContext(ctx); ok {
		t.Errorf("expected ok=false when token is non-string")
	}
}

// TestCtxKey_Isolation confirms ctxKeyUser and ctxKeyToken are distinct
// values — accidentally collapsing them would let a session token be read
// as a User struct in production.
func TestCtxKey_Isolation(t *testing.T) {
	if ctxKeyUser == ctxKeyToken {
		t.Fatal("ctxKeyUser and ctxKeyToken collide")
	}
}

// TestPolicyComplianceCache_StoreThenLookup exercises the F-9 process-local
// cache. A successful store must surface a fresh entry; an unrelated lookup
// must miss.
func TestPolicyComplianceCache_StoreThenLookup(t *testing.T) {
	srv := &Server{policyComplianceCache: make(map[uuid.UUID]policyComplianceCacheEntry)}
	uid := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	srv.policyComplianceStore(uid, true, nil)

	entry, ok := srv.policyComplianceLookup(uid)
	if !ok {
		t.Fatal("expected cache hit after store")
	}
	if !entry.complies {
		t.Errorf("complies = false, want true")
	}
	if entry.grace != nil {
		t.Errorf("grace should be nil, got %v", entry.grace)
	}

	// Miss for a different user.
	other := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	if _, ok := srv.policyComplianceLookup(other); ok {
		t.Errorf("expected miss for unrelated user")
	}
}
