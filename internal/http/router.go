// Package http wires the server's routes. One origin serves everything: the
// JSON API under /api, the server-rendered public report under /report, and the
// React SPA everywhere else (ADR-001).
package http

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// New builds the router over the embedded SPA assets.
func New(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	mux.Handle("/", spa(assets))
	return mux
}

// healthz is unauthenticated by design: the dev-server readiness poll and the
// container HEALTHCHECK both call it before there is any session (ADR-019).
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// spa serves the built bundle, falling back to index.html so client-side routes
// survive a page reload. /api and /report are the server's own namespaces, so
// they 404 rather than silently returning the SPA shell — a mistyped API path
// should look like a mistake, not like HTML.
func spa(assets fs.FS) http.Handler {
	files := http.FileServerFS(assets)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isServerRoute(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if _, err := fs.Stat(assets, name); err != nil {
				// Not a real asset — a client-side route. Serve the shell.
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	})
}

func isServerRoute(p string) bool {
	for _, prefix := range []string{"/api", "/report"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
