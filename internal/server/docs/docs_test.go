package docs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIndexHandler asserts the /docs HTML shell contains the vendored
// asset reference and a CSP locked to 'self' for scripts. Any future
// refactor that lets the page load Scalar from a CDN should fail this
// test loudly.
func TestIndexHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	IndexHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/docs", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `src="/docs/scalar.js"`) {
		t.Errorf("body missing vendored scalar src; got %q", body)
	}
	if !strings.Contains(body, `data-url="/openapi.json"`) {
		t.Errorf("body missing OpenAPI data-url; got %q", body)
	}
	if !strings.Contains(body, scalarSRI) {
		t.Errorf("body missing SRI integrity hash %q; got %q", scalarSRI, body)
	}

	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP missing script-src 'self'; got %q", csp)
	}
	if strings.Contains(csp, "unpkg.com") || strings.Contains(csp, "cdn") {
		t.Errorf("CSP must not reference a CDN; got %q", csp)
	}
	if rr.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("unexpected content-type: %q", rr.Header().Get("Content-Type"))
	}
}

// TestAssetHandler asserts /docs/scalar.js serves the embedded bundle
// with the right content-type and a non-trivial body.
func TestAssetHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	AssetHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/docs/scalar.js", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("content-type = %q, want application/javascript", ct)
	}
	// The Scalar standalone bundle is several MB. Anything under 100 KB
	// means the embed lost the file.
	if rr.Body.Len() < 100_000 {
		t.Errorf("scalar bundle suspiciously small: %d bytes", rr.Body.Len())
	}
}
