package api

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzBearer exercises bearer() with arbitrary Authorization header strings.
// Contract: must never panic; on ok=true the returned token is non-empty and
// contains no leading or trailing ASCII whitespace.
func FuzzBearer(f *testing.F) {
	f.Add("Bearer abc.def.ghi")
	f.Add("Bearer mon_ag_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	f.Add("Bearer ")
	f.Add("Bearer\t  spaced  ")
	f.Add("bearer lowercase")
	f.Add("")
	f.Add("Basic dXNlcjpwYXNz")

	f.Fuzz(func(t *testing.T, header string) {
		tok, ok := bearer(header)
		if !utf8.ValidString(tok) {
			// bearer never re-encodes; if input is valid UTF-8 the output must be too.
			if utf8.ValidString(header) {
				t.Fatalf("bearer(%q) returned non-UTF-8 token %q from valid-UTF-8 header", header, tok)
			}
		}
		if !ok {
			if tok != "" {
				t.Fatalf("bearer(%q) returned ok=false but non-empty token %q", header, tok)
			}
			return
		}
		if tok == "" {
			t.Fatalf("bearer(%q) returned ok=true with empty token", header)
		}
		// TrimSpace is applied internally; assert leading/trailing ASCII space
		// classes are stripped. Note: strings.TrimSpace uses unicode.IsSpace,
		// so the assertion uses the same predicate.
		trimmed := strings.TrimSpace(tok)
		if trimmed != tok {
			t.Fatalf("bearer(%q) returned token %q with whitespace edges", header, tok)
		}
	})
}
