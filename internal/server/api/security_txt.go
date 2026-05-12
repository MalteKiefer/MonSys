package api

import "net/http"

// securityTxt is the RFC 9116-compliant security.txt body served at
// /.well-known/security.txt. Baked in as a constant so deployments without
// the SPA's static dist still resolve the file via the Go server.
//
// The same content is shipped under web/public/.well-known/security.txt so
// the Vite build copies it verbatim into dist/ for the static-served path.
// If the two ever drift, the Go-served route wins for any deployment that
// hits the API directly; the SPA's copy is a convenience for CDN-only
// deployments. Keep them in sync.
//
// Expires MUST be in the future per RFC 9116 §2.5.5; rotate this string and
// the matching file in web/public/.well-known/ before the date below.
const securityTxt = `Contact: https://github.com/MalteKiefer/MonSys/security/advisories
Expires: 2027-05-12T00:00:00.000Z
Preferred-Languages: en, de
Canonical: https://mon.kiefer-networks.de/.well-known/security.txt
Policy: https://github.com/MalteKiefer/MonSys/blob/main/SECURITY.md
Acknowledgments: https://github.com/MalteKiefer/MonSys/blob/main/SECURITY.md#acknowledgments
`

// handleSecurityTxt serves the RFC 9116 security.txt. Public — no auth, no
// rate limit. The content is identical for every request, so we set a
// generous Cache-Control: the file is small and operators may rotate it,
// but stale-for-a-day is fine for the well-known channel.
func (s *Server) handleSecurityTxt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(securityTxt))
}
