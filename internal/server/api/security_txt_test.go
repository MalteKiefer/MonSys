package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestSecurityTxtHandler asserts the RFC 9116 contract for the public
// security.txt endpoint:
//
//   - 200 OK
//   - Content-Type: text/plain; charset=utf-8
//   - Body contains Contact, Expires, Canonical, Policy.
//
// The handler is stateless (does not touch the store), so we exercise it
// with a zero-value Server and a minimal chi router instead of standing up
// the full registerRoutes() graph.
func TestSecurityTxtHandler(t *testing.T) {
	s := &Server{}
	r := chi.NewRouter()
	r.Get("/.well-known/security.txt", s.handleSecurityTxt)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/security.txt", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if got, want := rec.Header().Get("Content-Type"), "text/plain; charset=utf-8"; got != want {
		t.Fatalf("Content-Type: got %q, want %q", got, want)
	}

	body := rec.Body.String()
	for _, field := range []string{
		"Contact: https://github.com/MalteKiefer/MonSys/security/advisories",
		"Expires: 2027-05-12T00:00:00.000Z",
		"Canonical: https://mon.kiefer-networks.de/.well-known/security.txt",
		"Policy: https://github.com/MalteKiefer/MonSys/blob/main/SECURITY.md",
	} {
		if !strings.Contains(body, field) {
			t.Errorf("body missing required field: %s\n---\n%s", field, body)
		}
	}
}
