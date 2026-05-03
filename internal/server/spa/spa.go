// Package spa embeds the production build of the React SPA and serves it
// from mon-server.
//
// The dist/ subdir is populated by `make web` (or the Dockerfile build
// stage). A placeholder index.html ships in the repo so a clean checkout
// still produces a working binary that explains how to build the UI.
package spa

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA. It falls
// back to /index.html for any path that doesn't map to a real file so
// client-side routes (React Router) work after a hard refresh.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// fs.Sub never errors for a string arg; this is purely to satisfy
		// the linter's concern about a swallowed return.
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			clean = "index.html"
		}
		// If the requested file exists in the bundle, serve it. Otherwise
		// rewrite to /index.html so React Router can handle the route.
		if _, err := fs.Stat(sub, clean); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				r2 := *r
				r2.URL.Path = "/"
				fileServer.ServeHTTP(w, &r2)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
