// Package http wires the server's routes. One origin serves everything: the
// JSON API under /api, the server-rendered public report under /report, and the
// React SPA everywhere else (ADR-001).
//
// chi replaces stdlib http.ServeMux as of M4 (ADR-021): the API brings route
// groups and middleware worth composing — one /api mount for M5's session
// middleware to wrap, request logging, panic recovery — that stdlib routing
// would otherwise hand-roll.
package http

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
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
//
// l and q are both handed in rather than l alone: store.Queries is a stateless
// wrapper over the shared *sql.DB (ADR-004's single connection), so a second,
// independent store.New(sqlDB) alongside ledger.New(sqlDB) costs nothing and
// avoids adding a Querier accessor to ADR-027's already-implemented boundary —
// direct-CRUD routes (members, accounts, purposes — ADR-027's "no domain
// wrapper" list) call q directly; routes with a derived invariant call l.
//
// au and baseURL are M5's addition (issue #114): au is the bootstrap-account
// service POST /api/register calls, and baseURL is read once here, purely to
// derive the session cookie's Secure flag from its scheme (session.go) —
// never stored or exposed beyond that.
func New(assets fs.FS, build Build, l *ledger.Ledger, q store.Querier, logger *slog.Logger, au *auth.Auth, baseURL string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(logger))

	r.Get("/healthz", healthz(build))

	// Every /api route lives under this one mount, so M5's session middleware
	// (LoadAndSave, then sessionRequired for every route but the four public
	// ones - api.go's two groups) has exactly one seam to wrap instead of
	// routes scattered across the tree (ADR-021).
	sm := newSessionManager(q, baseURL, logger)
	r.Route("/api", (&api{
		ledger:         l,
		queries:        q,
		logger:         logger,
		auth:           au,
		sessionManager: sm,
		loginLimiter:   newRateLimiter(loginRateLimitMaxAttempts, loginRateLimitWindow),
	}).routes)

	// The SPA fallback is chi's NotFound handler (ADR-021): chi checks every
	// registered route first, so /api and /report still 404 instead of falling
	// through to the shell.
	r.NotFound(spa(assets))

	return r
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
// all the container HEALTHCHECK needs. Considered and kept at M4, not amended:
// M4 puts real load on the single shared connection (ADR-004), so a check that
// also probed the store would misfire *more* often, not less, and a 503 turns a
// momentarily-busy or unwritable file into a restart loop instead of a passing
// liveness check with a slow request behind it. If a dependency is ever worth
// reporting, it belongs here as a non-ok status, not as a second endpoint.
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
// survive a page reload. It is registered as chi's NotFound handler, so /api and
// /report never reach it — a registered route always wins over NotFound, which
// is what keeps those namespaces 404-ing instead of silently returning the SPA
// shell.
func spa(assets fs.FS) http.HandlerFunc {
	files := http.FileServerFS(assets)

	return func(w http.ResponseWriter, r *http.Request) {
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
	}
}

func isServerRoute(p string) bool {
	for _, prefix := range []string{"/api", "/report"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
