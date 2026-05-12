package api

import "testing"

// BenchmarkBearerParse measures the per-request cost of the Authorization
// header parser. bearer() is invoked on every request that hits an
// authenticated route (every /v1/* call, /metrics, /debug/pprof/*), so any
// regression here multiplies across the whole API surface.
//
// Three cases: the common valid path, an empty header, and a header with
// surrounding whitespace (TrimSpace fast/slow paths).
func BenchmarkBearerParse(b *testing.B) {
	cases := []struct {
		name string
		hdr  string
	}{
		{"valid", "Bearer mon_sess_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{"empty", ""},
		{"whitespace", "Bearer   mon_ag_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx   "},
		{"wrong-scheme", "Basic dXNlcjpwYXNz"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = bearer(c.hdr)
			}
		})
	}
}
