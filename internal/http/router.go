// Package http wires the server's routes. One origin serves everything: the
// JSON API under /api, the server-rendered public report under /report, and the
// React SPA everywhere else (ADR-001).
//
// chi replaces stdlib http.ServeMux (ADR-021): the API wants route groups and
// middleware (one /api mount for session middleware, request logging, panic
// recovery) that stdlib routing would make us hand-roll.
package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni"
	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// Build identifies the running binary. It is a struct because two adjacent
// strings are easily passed in the wrong order, and the version is an operator
// contract (ADR-018).
type Build struct {
	Version string
	Commit  string
}

// Go's mime table has no .webmanifest entry, so http.FileServerFS sniffs the
// PWA manifest as text/plain and browsers may refuse it (ADR-008). Registering
// it here keeps the file server the one thing that decides content types.
func init() {
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		panic("registering the .webmanifest mime type: " + err.Error())
	}
}

// New builds the router over the embedded SPA assets. build is what /healthz
// reports, so which binary is live can be read over HTTP.
//
// l and q are both passed: store.Queries is a stateless wrapper over the
// shared *sql.DB (ADR-004's single connection), so a second store.New(sqlDB)
// beside ledger.New(sqlDB) costs nothing and avoids a Querier accessor on
// ADR-027's boundary. Direct-CRUD routes (members, accounts, purposes -
// ADR-027's "no domain wrapper" list) call q; routes with a derived invariant
// call l.
//
// au is the bootstrap-account service POST /api/register calls. baseURL is
// read once, to derive the session cookie's Secure flag from its scheme
// (session.go).
//
// uploadsDir is the local volume receipt photos live on (ADR-011);
// Config.EnsureUploadsDirWritable proved it before `serve` builds a router.
//
// sqlDB is the same *sql.DB l and q wrap, passed because restore.go's confirm
// route calls internal/backup.Restore, which needs its own write transaction
// spanning nearly every table (see backup.Restore).
//
// dbFileCheck is /healthz's check that the configured database path still
// names the file this process opened (see db.FileIdentity). nil skips it.
//
// trustedProxies (Config.TrustedProxies) are the peers whose X-Forwarded-For
// clientIP believes. nil believes none.
func New(assets fs.FS, build Build, l *ledger.Ledger, q store.Querier, sqlDB *sql.DB, dbFileCheck func() error, logger *slog.Logger, au *auth.Auth, baseURL string, uploadsDir string, backupDir string, trustedProxies []netip.Prefix) http.Handler {
	// The ledger's clock is the process's only one: the report and every route
	// that reckons a calendar day or month read it here, so a handler and the
	// ledger it calls read one clock, not two that could be set apart.
	now := l.Now

	r := chi.NewRouter()
	// Order matters: requestID first, so everything below logs with the ID;
	// requestLogger next, so the request line still appears when a handler
	// panics; recoverer inside it, so the panic is answered (as a 500) before
	// the request line reads the status.
	r.Use(requestID(logger))
	r.Use(requestLogger(logger))
	r.Use(recoverer(logger))
	if len(trustedProxies) == 0 {
		r.Use(untrustedForwardedFor(logger))
	}
	r.Use(securityHeaders)

	r.Get("/healthz", healthz(build, dbFileCheck, pingStore(q), lastDumpIn(backupDir), logger))

	// The third-party licence notices the binary carries (#521), public like
	// the signed-out footer that links to it. Registered here so the SPA
	// fallback never answers it with the shell.
	r.Get("/third-party-notices", thirdPartyNotices)

	// Every /api route lives under this one mount, so session middleware
	// (LoadAndSave, then sessionRequired for all but the four public routes, see
	// api.go) has one seam to wrap (ADR-021).
	sm := newSessionManager(q, baseURL, logger)

	// The limiters and device-token store read the same injectable clock as the
	// routes (#500), so a test moves every lockout with one fake.
	loginLimiter := newEscalatingRateLimiter(loginRateLimitMaxAttempts, loginRateLimitWindow, loginLockouts, lockoutDecay)
	loginLimiter.now = now
	registerLimiter := newEscalatingRateLimiter(registerRateLimitMaxAttempts, registerRateLimitWindow, loginLockouts, lockoutDecay)
	registerLimiter.now = now
	devices := newDeviceTokens()
	devices.now = now

	a := &api{
		ledger:          l,
		queries:         q,
		sqlDB:           sqlDB,
		logger:          logger,
		auth:            au,
		sessionManager:  sm,
		loginLimiter:    loginLimiter,
		registerLimiter: registerLimiter,
		devices:         devices,
		trustedProxies:  trustedProxies,
		uploadsDir:      uploadsDir,
		backupDir:       backupDir,
		baseURL:         strings.TrimRight(baseURL, "/"),
		now:             now,
		restoreStage:    newRestoreStage(),
	}
	r.Route("/api", a.routes)

	// The public report (ADR-035): outside /api, so no session manager wraps it
	// (no cookie read or set); the slug alone names the fund (ADR-030).
	//
	// Both routes are throttled per client network (#520, report_limit.go), ahead
	// of the slug lookup, so a probe of unknown slugs is counted too.
	reportLimits := newReportLimiter(a, now)
	r.With(reportLimits.middleware("html", reportHTMLLimit, logger)).Get("/report/{slug}", reportHandler(l, q, logger))
	r.With(reportLimits.middleware("pdf", reportPDFLimit, logger)).Get("/report/{slug}/pdf", reportPDFHandler(l, q, logger))

	// The SPA fallback is chi's NotFound handler (ADR-021): registered routes
	// win, so /api and /report still 404 instead of falling through to the shell.
	r.NotFound(spa(assets))

	return r
}

// thirdPartyNotices serves THIRD-PARTY-NOTICES, embedded in the binary, as
// plain text: ~160 kB of licence text that is meant to be read, not rendered.
// It changes only with a release, so a short public cache is safe.
func thirdPartyNotices(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(uruni.ThirdPartyNotices())
}

// health is what /healthz returns. Operator-facing, so English like the CLI
// and logs (ADR-014).
//
// version identifies a tagged deploy; commit identifies an untagged one, where
// version is only `dev`.
type health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Error   string `json:"error,omitempty"`
	// LastDumpAt is when the newest backup dump in URUNI_BACKUP_DIR was written
	// (#505). Informational: it never changes Status, because the container
	// HEALTHCHECK and Caddy read this route as liveness (ADR-019) and a missed
	// dump is no reason to stop serving. Absent when there is no dump or the
	// directory can't be read.
	LastDumpAt *time.Time `json:"last_dump_at,omitempty"`
}

// healthzPingTimeout bounds /healthz's database query (#505). It is under the
// Dockerfile HEALTHCHECK's 3s timeout, so a slow store answers 503 with a
// body rather than timing out with none, and long enough that the single
// shared connection (ADR-004) held by a normal write or a backup read
// (milliseconds here) does not read as down. One slow probe is not a restart:
// the HEALTHCHECK needs three failures in a row.
const healthzPingTimeout = 2 * time.Second

// pingStore is /healthz's database probe: the generated Ping query
// (SELECT 1) through the handlers' store, proving the connection can answer,
// not only that the file exists.
func pingStore(q store.Querier) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := q.Ping(ctx)
		return err
	}
}

// lastDumpIn reads the newest dump's time from backupDir's file names: a
// directory listing, no file opened.
func lastDumpIn(backupDir string) func() (time.Time, bool) {
	return func() (time.Time, bool) {
		dumps, err := backup.ListDumps(backupDir)
		if err != nil || len(dumps) == 0 {
			return time.Time{}, false
		}
		return dumps[0].When, true
	}
}

// healthz is unauthenticated by design: the dev-server readiness poll and the
// container HEALTHCHECK call it before any session exists (ADR-019).
//
// It is a liveness check: "is this process serving?". A check that probed the
// store would misfire more often under load on the single shared connection
// (ADR-004), and a 503 turns a busy file into a restart loop. A dependency
// worth reporting belongs here as a non-ok status, not a second endpoint.
//
// Two exceptions. (#278): the database path no longer naming the file this
// process opened. That is a stat of a path, which cannot queue behind a
// write, and the process is serving data that vanishes at its next restart,
// so it answers 503 and logs at error level. The red HEALTHCHECK is the
// alarm; nothing refuses writes, since the fix is a restart onto the right
// file.
//
// (#505): a store that cannot answer SELECT 1 within healthzPingTimeout.
// The timeout answers the busy-store worry above, and a database that can't
// answer a trivial query is not serving the treasurer either.
//
// Either way the body says only "database unavailable"; the detail, which
// can carry the database path, goes to the log, never to an unauthenticated
// caller (#505).
func healthz(build Build, dbFileCheck func() error, ping func(context.Context) error, lastDump func() (time.Time, bool), logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := health{Status: "ok", Version: build.Version, Commit: build.Commit}
		code := http.StatusOK
		fail := func(msg string, err error) {
			loggerFrom(r.Context(), logger).Error(msg, "error", err)
			body.Status = "error"
			body.Error = "database unavailable"
			code = http.StatusServiceUnavailable
		}
		if dbFileCheck != nil {
			if err := dbFileCheck(); err != nil {
				fail("database file check failed", err)
			}
		}
		if code == http.StatusOK && ping != nil {
			ctx, cancel := context.WithTimeout(r.Context(), healthzPingTimeout)
			err := ping(ctx)
			cancel()
			if err != nil {
				fail("database ping failed", err)
			}
		}
		if lastDump != nil {
			if at, ok := lastDump(); ok {
				body.LastDumpAt = &at
			}
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}
}

// spa serves the built bundle, falling back to index.html so client-side
// routes survive a reload. It is chi's NotFound handler, so /api and /report
// never reach it: a registered route always wins, which keeps those
// namespaces 404-ing instead of returning the SPA shell.
func spa(assets fs.FS) http.HandlerFunc {
	files := http.FileServerFS(assets)

	return func(w http.ResponseWriter, r *http.Request) {
		if isServerRoute(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		servesShell := name == "" || name == "index.html"
		hashed := strings.HasPrefix(name, "assets/")
		if name != "" {
			// A directory is never listed (#506); it reads as a client-side route.
			if info, err := fs.Stat(assets, name); err != nil || info.IsDir() {
				// A missing file under assets/ is a chunk an older shell still names after a
				// deploy. The shell would arrive as JavaScript and fail confusingly; a 404 is
				// what the browser can report.
				if hashed && err != nil {
					http.NotFound(w, r)
					return
				}
				// Not a real asset: a client-side route. Serve the shell.
				r = r.Clone(r.Context())
				r.URL.Path = "/"
				servesShell = true
				hashed = false
			}
		}

		// The two responses that must never come from a stale browser cache
		// (ADR-008). Every other asset is content-hashed. These keep their names
		// across deploys, so a cached copy is how "I updated the server but my phone
		// shows the old app" happens: index.html names the current hashed assets, and
		// sw.js is what notices a new build.
		if servesShell || name == "sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		// Everything under assets/ is Vite's content-hashed output, so a cached copy
		// is never stale (#506). Embedded files carry no modification time, so
		// without this there is no Last-Modified or ETag and every load re-downloaded
		// the bundle over mobile data.
		if hashed {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
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
