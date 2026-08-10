// Package http wires the server's routes. One origin serves everything: the
// JSON API under /api, the server-rendered public report under /report, and the
// React SPA everywhere else (ADR-001).
//
// The router is stdlib http.ServeMux while there are two routes to serve. chi
// takes over at M4, when the API brings route groups and middleware worth
// composing — decided in ADR-021, which also explains why not sooner.
package http

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Build identifies the running binary. It is a struct rather than two string
// arguments because two adjacent strings are trivially passed in the wrong
// order, and the version is an operator contract (ADR-018) — a silently swapped
// one would be worse than none.
type Build struct {
	Version string
	Commit  string
}

// New builds the router over the embedded SPA assets. build is what /healthz
// reports, so which binary is live can be read over HTTP without shell access
// to the container.
func New(assets fs.FS, build Build) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(build))
	mux.Handle("/", spa(assets))
	return mux
}

// health is what /healthz returns. Operator-facing, so English, like the CLI and
// the logs — Indonesian is the treasurer's surface (ADR-014).
//
// version identifies a tagged deploy; commit identifies an untagged one, where
// version is only ever `dev` and the SHA is the sole thing naming what runs.
type health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// healthz is unauthenticated by design: the dev-server readiness poll and the
// container HEALTHCHECK both call it before there is any session (ADR-019).
//
// This is a *liveness* check — it answers "is this process serving?", which is
// all the container HEALTHCHECK needs. The store exists as of M1.3 but is
// deliberately not probed here: ADR-019 pins this endpoint to 200-when-up, and
// the container HEALTHCHECK calls it, so reporting a broken database as a 503
// would turn an unwritable file into a restart loop. Revisit at M4, when there
// are real queries behind it, as an amendment to ADR-019 — and if a dependency
// does get reported it belongs here as a non-ok status, not as a second endpoint.
func healthz(build Build) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(health{
			Status:  "ok",
			Version: build.Version,
			Commit:  build.Commit,
		})
	}
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
