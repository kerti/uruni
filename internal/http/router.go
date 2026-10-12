// Package http wires the server's routes. One origin serves everything: the
// JSON API under /api, the server-rendered public report under /report, and the
// React SPA everywhere else (ADR-001).
//
// chi replaces stdlib http.ServeMux as of M4 (ADR-021): the API brings route
// groups and middleware worth composing - one /api mount for M5's session
// middleware to wrap, request logging, panic recovery - that stdlib routing
// would otherwise hand-roll.
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
	"github.com/go-chi/chi/v5/middleware"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// Build identifies the running binary. It is a struct rather than two string
// arguments because two adjacent strings are trivially passed in the wrong
// order, and the version is an operator contract (ADR-018) - a silently swapped
// one would be worse than none.
type Build struct {
	Version string
	Commit  string
}

// Go's mime table has no .webmanifest entry, so http.FileServerFS sniffs the
// PWA manifest as text/plain and browsers are entitled to refuse it (ADR-008,
// M6.7). Registering it here rather than special-casing the path in spa()
// keeps the file server the one thing that decides content types.
func init() {
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		panic("registering the .webmanifest mime type: " + err.Error())
	}
}

// New builds the router over the embedded SPA assets. build is what /healthz
// reports, so which binary is live can be read over HTTP without shell access
// to the container.
//
// l and q are both handed in rather than l alone: store.Queries is a stateless
// wrapper over the shared *sql.DB (ADR-004's single connection), so a second,
// independent store.New(sqlDB) alongside ledger.New(sqlDB) costs nothing and
// avoids adding a Querier accessor to ADR-027's already-implemented boundary -
// direct-CRUD routes (members, accounts, purposes - ADR-027's "no domain
// wrapper" list) call q directly; routes with a derived invariant call l.
//
// au and baseURL are M5's addition (issue #114): au is the bootstrap-account
// service POST /api/register calls, and baseURL is read once here, purely to
// derive the session cookie's Secure flag from its scheme (session.go) -
// never stored or exposed beyond that.
//
// uploadsDir is #153's addition: the local volume receipt photos are read
// from and written to (ADR-011). Config.EnsureUploadsDirWritable already
// proved it exists and is writable before `serve` ever builds a router; nothing
// here re-checks that.
//
// sqlDB is #325's addition, alongside l and q: restore.go's own confirm
// route calls internal/backup.Restore, which needs to open its own write
// transaction spanning virtually every table - something neither a
// *ledger.Ledger nor a store.Querier alone can do (see backup.Restore's own
// doc comment for why it is not routed through the Ledger). It is the same
// *sql.DB l and q already wrap; passing it a third time costs nothing
// (ADR-004's single connection).
//
// dbFileCheck is #278's addition: /healthz calls it to confirm the
// configured database path still names the file this process opened (see
// db.FileIdentity). nil skips the check, which is what tests that do not
// exercise it pass.
//
// trustedProxies is #447's addition (Config.TrustedProxies): the peers whose
// X-Forwarded-For clientIP believes. nil believes none.
func New(assets fs.FS, build Build, l *ledger.Ledger, q store.Querier, sqlDB *sql.DB, dbFileCheck func() error, logger *slog.Logger, au *auth.Auth, baseURL string, uploadsDir string, backupDir string, trustedProxies []netip.Prefix) http.Handler {
	return newWithClock(assets, build, l, q, sqlDB, dbFileCheck, logger, au, baseURL, uploadsDir, backupDir, trustedProxies, time.Now)
}

// newWithClock is New with the wall clock injectable (#379): the report and
// every route that reckons a calendar day or month read now, so a test can
// pick the instant. New passes time.Now.
func newWithClock(assets fs.FS, build Build, l *ledger.Ledger, q store.Querier, sqlDB *sql.DB, dbFileCheck func() error, logger *slog.Logger, au *auth.Auth, baseURL string, uploadsDir string, backupDir string, trustedProxies []netip.Prefix, now func() time.Time) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(requestLogger(logger))
	r.Use(securityHeaders)

	r.Get("/healthz", healthz(build, dbFileCheck, pingStore(q), lastDumpIn(backupDir), logger))

	// Every /api route lives under this one mount, so M5's session middleware
	// (LoadAndSave, then sessionRequired for every route but the four public
	// ones - api.go's two groups) has exactly one seam to wrap instead of
	// routes scattered across the tree (ADR-021).
	sm := newSessionManager(q, baseURL, logger)

	// The limiters and the device-token store read the same injectable clock
	// as the routes (#500), so a test moves every lockout with one fake.
	loginLimiter := newEscalatingRateLimiter(loginRateLimitMaxAttempts, loginRateLimitWindow, loginLockouts, lockoutDecay)
	loginLimiter.now = now
	registerLimiter := newEscalatingRateLimiter(registerRateLimitMaxAttempts, registerRateLimitWindow, loginLockouts, lockoutDecay)
	registerLimiter.now = now
	devices := newDeviceTokens()
	devices.now = now

	r.Route("/api", (&api{
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
	}).routes)

	// The public report (ADR-035): outside /api, so no session manager ever
	// wraps it - no cookie is read or set - and the slug alone names the fund
	// (ADR-030's carve-out).
	r.Get("/report/{slug}", reportHandler(l, q, logger, now))
	r.Get("/report/{slug}/pdf", reportPDFHandler(l, q, logger, now))

	// The SPA fallback is chi's NotFound handler (ADR-021): chi checks every
	// registered route first, so /api and /report still 404 instead of falling
	// through to the shell.
	r.NotFound(spa(assets))

	return r
}

// health is what /healthz returns. Operator-facing, so English, like the CLI and
// the logs - Indonesian is the treasurer's surface (ADR-014).
//
// version identifies a tagged deploy; commit identifies an untagged one, where
// version is only ever `dev` and the SHA is the sole thing naming what runs.
type health struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Error   string `json:"error,omitempty"`
	// LastDumpAt is when the newest backup dump in URUNI_BACKUP_DIR was
	// written (#505). Informational only: it never changes Status, because
	// the container HEALTHCHECK and Caddy's start-up dependency read this
	// route as liveness (ADR-019), and a missed dump is not a reason to stop
	// serving. Absent when there is no dump yet or the directory can't be read.
	LastDumpAt *time.Time `json:"last_dump_at,omitempty"`
}

// healthzPingTimeout bounds /healthz's database query (#505). It sits under
// the Dockerfile HEALTHCHECK's 3s timeout so a slow store answers 503 with a
// body rather than a probe that times out with none; and it is long enough
// that the single shared connection (ADR-004) being held by a normal write or
// a backup's read transaction - milliseconds at this app's size - does not
// read as down. One slow probe is not a restart either: the HEALTHCHECK needs
// three failures in a row.
const healthzPingTimeout = 2 * time.Second

// pingStore is /healthz's database probe: the generated Ping query (SELECT 1)
// through the same store the handlers use, so it proves the connection can
// answer a query, not only that the file exists.
func pingStore(q store.Querier) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := q.Ping(ctx)
		return err
	}
}

// lastDumpIn reads the newest dump's time from backupDir's file names - a
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
// container HEALTHCHECK both call it before there is any session (ADR-019).
//
// This is a *liveness* check - it answers "is this process serving?", which is
// all the container HEALTHCHECK needs. Considered and kept at M4, not amended:
// M4 puts real load on the single shared connection (ADR-004), so a check that
// also probed the store would misfire *more* often, not less, and a 503 turns a
// momentarily-busy or unwritable file into a restart loop instead of a passing
// liveness check with a slow request behind it. If a dependency is ever worth
// reporting, it belongs here as a non-ok status, not as a second endpoint.
//
// One is (#278): the database path no longer naming the file this process
// opened. That is not a busy store - it is a stat of a path, which cannot
// queue behind a write - and a process in that state is serving data that
// vanishes at its next restart, so it answers 503 and logs at error level.
// The container HEALTHCHECK turning red is the alarm; nothing here refuses
// writes, since the fix is a restart onto the file the operator meant.
//
// The other is #505's: a store that cannot answer SELECT 1 within
// healthzPingTimeout. The M4 worry above was a busy store misfiring; the
// timeout is what answers it (see healthzPingTimeout). A database that can't
// answer a trivial query is not serving the treasurer either, and saying ok
// over it is the failure the assessment found.
//
// Either way the body says only "database unavailable": the detail - which
// can carry the database path - goes to the log, never to an unauthenticated
// caller (#505).
func healthz(build Build, dbFileCheck func() error, ping func(context.Context) error, lastDump func() (time.Time, bool), logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := health{Status: "ok", Version: build.Version, Commit: build.Commit}
		code := http.StatusOK
		fail := func(msg string, err error) {
			logger.Error(msg, "error", err)
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

// spa serves the built bundle, falling back to index.html so client-side routes
// survive a page reload. It is registered as chi's NotFound handler, so /api and
// /report never reach it - a registered route always wins over NotFound, which
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

		servesShell := name == "" || name == "index.html"
		hashed := strings.HasPrefix(name, "assets/")
		if name != "" {
			// A directory is never listed (#506): it reads as a client-side
			// route like any other path that names no file.
			if info, err := fs.Stat(assets, name); err != nil || info.IsDir() {
				// A missing file under assets/ is a chunk an older shell still
				// names after a deploy. The shell would arrive as JavaScript
				// and fail confusingly; a 404 is what the browser can report.
				if hashed && err != nil {
					http.NotFound(w, r)
					return
				}
				// Not a real asset - a client-side route. Serve the shell.
				r = r.Clone(r.Context())
				r.URL.Path = "/"
				servesShell = true
				hashed = false
			}
		}

		// The two responses that must never come from a stale browser cache
		// (ADR-008, M6.7). Every other asset is content-hashed and safe to
		// cache indefinitely; these keep their names across deploys, so a
		// cached copy is exactly how "I updated the server but my phone still
		// shows the old app" happens. index.html names the current hashed
		// assets, and sw.js is what notices there is a new build at all.
		if servesShell || name == "sw.js" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		// Everything under assets/ is Vite's content-hashed output: a new
		// build gets new names, so a cached copy can never be stale (#506).
		// Embedded files carry no modification time, so without this there
		// is no Last-Modified or ETag either, and every load re-downloaded
		// the whole bundle over mobile data.
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
