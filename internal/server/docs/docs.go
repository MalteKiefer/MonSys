// Package docs serves the interactive OpenAPI viewer (Scalar) bundled with
// mon-server.
//
// Why a custom handler instead of huma's built-in /docs route?
//
//   - huma defaults to Stoplight Elements served from unpkg.com. That works
//     but couples the docs UI to a public CDN at request time. We want the
//     viewer to come from the same binary as the API so air-gapped deploys
//     keep working and so the supply chain is explicit.
//   - huma exposes DocsRenderer = "scalar" but its Scalar template still
//     points at unpkg.com. We disable huma's built-in (Config.DocsPath = "")
//     and serve a tiny HTML shell here that loads the vendored
//     scalar.standalone.js sibling file from the same origin.
//
// The bundle is vendored at internal/server/docs/scalar.standalone.js
// (Scalar @scalar/api-reference v1.44.20, SHA-384 matches huma's hardcoded
// SRI integrity for the same upstream file). To upgrade, replace the file
// and update SRI in handlerHTML.
package docs

import (
	_ "embed"
	"net/http"
)

// ScalarVersion is the pinned @scalar/api-reference release the embedded
// scalar.standalone.js was vendored from. Bump together with the bundle.
const ScalarVersion = "1.44.20"

// SRI for the vendored bundle. Pre-computed at vendor time; matches the
// upstream unpkg.com hash for @scalar/api-reference@1.44.20 standalone.
const scalarSRI = "sha384-tMz7GAo6dMy55x9tLFtH+sHtogji6Scmb+feBR31TAHmvSPRUTboK9H3M5NFaP4R"

//go:embed scalar.standalone.js
var scalarJS []byte

// indexHTML is the page rendered at /docs. It loads the vendored Scalar
// bundle from /docs/scalar.js and points it at the OpenAPI JSON we already
// serve at /openapi.json.
//
// The Content-Security-Policy is set per-response in IndexHandler so it
// overrides the strict global CSP installed by securityHeaders middleware.
// Scalar needs 'unsafe-eval' (it ships a runtime template engine) and
// 'unsafe-inline' for styles. The script-src restricts execution to our
// own origin (no third-party script-src 'self' would be needed because the
// JS is bundled).
const indexHTML = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <meta name="referrer" content="no-referrer">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>MonSys API Reference</title>
  </head>
  <body>
    <script
      id="api-reference"
      data-url="/openapi.json"
      data-configuration='{"theme":"default","layout":"modern","hideDownloadButton":false}'
    ></script>
    <script src="/docs/scalar.js" integrity="` + scalarSRI + `"></script>
  </body>
</html>`

// IndexHandler returns an http.Handler that serves the /docs HTML shell.
//
// The handler installs a per-response CSP that allows the inline data-*
// config attributes and the vendored Scalar bundle. Callers should mount
// this on chi BEFORE the SPA catch-all so the SPA's index.html does not
// shadow /docs.
func IndexHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		// Tight CSP for the docs viewer. Scalar requires unsafe-eval for
		// its runtime renderer and unsafe-inline styles. script-src is
		// 'self' because the bundle is served from /docs/scalar.js, not a
		// CDN. connect-src allows fetching /openapi.json + any "Try it
		// out" calls into /v1/*.
		h.Set("Content-Security-Policy",
			"default-src 'none'; base-uri 'none'; "+
				"script-src 'self' 'unsafe-eval' 'unsafe-inline'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; font-src 'self' data:; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'")
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(indexHTML))
	})
}

// AssetHandler serves the vendored Scalar JS bundle at /docs/scalar.js.
//
// The bundle is embedded into the binary so an air-gapped deployment can
// render docs without outbound network access. Cache-Control is "no-store"
// so a Scalar upgrade is immediately visible after a server restart and
// operators don't have to think about stale caches on the admin UI.
func AssetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/javascript; charset=utf-8")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		_, _ = w.Write(scalarJS)
	})
}
