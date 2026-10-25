// Command uruni is the whole product: one binary serving the API, the public
// report and the embedded SPA (ADR-001). ADR-019 pins the subcommand surface;
// the Makefile and container HEALTHCHECK are written against it.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/kerti/uruni"
	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/config"
	uruniHTTP "github.com/kerti/uruni/internal/http"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/lock"
	"github.com/kerti/uruni/internal/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "uruni: %v\n", err)
		os.Exit(1)
	}
}

// The CLI and server logs are the operator's surface, so English like the
// self-hosting docs. Indonesian is the treasurer's: the SPA and public report
// (ADR-014).
var (
	// ErrNoCommand and ErrUnknownCommand are sentinels, matched by identity.
	ErrNoCommand      = errors.New("no command given")
	ErrUnknownCommand = errors.New("unknown command")
)

// usage lists every subcommand in ADR-019's table.
const usage = "try: uruni serve | migrate up|down|status | create-user <email> [password] | rebuild --from <backup.zip> | version | healthcheck | seed-e2e"

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w - %s", ErrNoCommand, usage)
	}

	switch args[0] {
	case "serve":
		return serve()
	case "migrate":
		ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
		defer cancel()
		return migrate(ctx, args[1:], os.Stdout)
	case "version":
		return printVersion(os.Stdout)
	case "healthcheck":
		return healthcheck()
	case "create-user":
		return createUser(context.Background(), args[1:], os.Stdin, os.Stdout, os.Stderr)
	case "rebuild":
		// No timeout: a large backup takes as long as it takes. An interrupt
		// cancels the context, and the rebuild's own cleanup leaves uruni.db as
		// it was.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return rebuild(ctx, args[1:], os.Stdout)
	case "seed-e2e":
		return seedE2E(context.Background())
	default:
		return fmt.Errorf("%w: %q - %s", ErrUnknownCommand, args[0], usage)
	}
}

func serve() error { return serveContext(context.Background()) }

// serveContext is serve under a caller's context, so a test can stop the server
// without signalling the test process. Production passes Background; SIGINT and
// SIGTERM still end it, as before.
func serveContext(parent context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// The one boot-time filesystem check config.Load (a pure environment parse)
	// does not make: only `serve` writes a receipt, so only it needs the volume
	// present and writable (#153).
	if err := config.EnsureUploadsDirWritable(cfg.UploadsDir); err != nil {
		return err
	}
	// Same check for the daily backup dumps' directory (#324).
	if err := config.EnsureBackupDirWritable(cfg.BackupDir); err != nil {
		return err
	}

	// `serve` and `migrate up` / `migrate down` take this lock, so a migration
	// never runs under a live server (#494). `migrate status` does not: an
	// operator runs it while a server is up. For `serve` the lock stops two
	// long-lived `serve` processes both reaching the domain-level singleton guard
	// (first-run setup refusing a second fund) at once; see internal/lock.
	lockPath := lock.PathFor(cfg.DBPath)
	instanceLock, err := lock.Acquire(lockPath)
	if err != nil {
		return err
	}
	defer func() { _ = instanceLock.Release() }()

	assets, err := uruni.WebAssets()
	if err != nil {
		return fmt.Errorf("opening the embedded web assets: %w", err)
	}

	// One logger, built at startup and passed down, no package-level global
	// (ADR-022); internal/http's request logging and error mappers use it
	// (ADR-021).
	logger := newLogger(cfg, os.Stderr)

	// Load accepts http on purpose (local dev, a LAN-only box), so the cost is
	// said once at boot instead: the base URL, not the request, decides the
	// cookie's Secure flag. The origin is not logged; the operator just set it.
	if config.PlaintextPublicBaseURL(cfg.BaseURL) {
		logger.Warn("URUNI_BASE_URL is http on a non-loopback host: the session cookie is not marked Secure and the treasurer's login travels in clear text; set it to the public https origin")
	}

	// SIGINT/SIGTERM close in-flight requests cleanly (`make restart` and
	// `docker compose down` stop the process this way). Set up before the store
	// opens so a Ctrl-C during a long first migration is honoured.
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The store, opened once for the process (ADR-004: one connection, so
	// everything serializes), brought to this binary's schema and checked.
	sqlDB, dbFile, err := openDatabase(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	// A second store.New(sqlDB) beside ledger.New(sqlDB): store.Queries is a
	// stateless wrapper over the shared *sql.DB (ADR-004), so this costs nothing
	// and avoids a Querier accessor on ADR-027's ledger boundary.
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)
	au := auth.New(sqlDB)

	// The boot-time dump ADR-012 promises: once serve has booted, a restorable
	// backup in the running binary's format exists, even the first time this
	// version runs against an instance whose newest dump is older. Non-fatal
	// (logged): a backup failing must never keep the treasurer from recording a
	// transaction (see EnsureBootDump).
	if err := backup.EnsureBootDump(ctx, sqlDB, cfg.UploadsDir, cfg.BackupDir, time.Now(), logger); err != nil {
		logger.Error("boot backup dump failed", "error", err)
	}

	// The daily scheduler (ADR-013): one goroutine, started after the boot dump
	// has finished (or failed and logged). That ordering, not a lock, keeps the
	// two from racing on one file (internal/backup/scheduler.go). Stopped by the
	// same ctx that gracefully shuts the HTTP server, and joined through
	// schedulerDone before the deferred sqlDB.Close above runs, so a tick never
	// writes a dump against a closed database.
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		backup.RunScheduler(ctx, sqlDB, cfg.UploadsDir, cfg.BackupDir, logger)
	}()
	scheduler := newSchedulerJoin(schedulerDone)
	// Every return path, not only the signal: a listener that fails at boot
	// (port in use) returns through errc with the scheduler still running.
	// Deferred after sqlDB.Close, so it runs first. On the signal path shutdown
	// has already joined, and the join remembers its outcome, so this returns at
	// once even when that join timed out: the scheduler is never waited on twice.
	defer func() {
		stop()
		scheduler.wait(schedulerShutdownTimeout)
	}()

	srv, requests := newServer(fmt.Sprintf(":%d", cfg.Port),
		uruniHTTP.New(uruniHTTP.Deps{
			Assets: assets, Build: uruniHTTP.Build{Version: version, Commit: buildCommit()},
			Ledger: l, Queries: q, DB: sqlDB, DBFileCheck: dbFile.Check, Logger: logger,
			Auth: au, BaseURL: cfg.BaseURL, UploadsDir: cfg.UploadsDir, BackupDir: cfg.BackupDir, TrustedProxies: cfg.TrustedProxies,
		}))

	errc := make(chan error, 1)
	go func() {
		// The version is in the boot line on purpose: the first thing worth knowing
		// when an operator pastes logs into an issue.
		logger.Info("listening", "port", cfg.Port, "version", version, "db", cfg.DBPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		return shutdown(srv, requests, scheduler, shutdownBounds{
			drain:     httpShutdownTimeout,
			handlers:  handlerShutdownTimeout,
			scheduler: schedulerShutdownTimeout,
		}, logger)
	}
}

// newServer builds serve's http.Server around h, with its request tracker
// already attached. The pair comes out of one function so a server cannot be
// started untracked, and shutdown is handed the tracker that belongs to it.
func newServer(addr string, h http.Handler) (*http.Server, *requestTracker) {
	srv := &http.Server{
		Addr:    addr,
		Handler: h,
		// A server with no timeouts can be held open by a slow client, one goroutine
		// and one buffered response each. ReadTimeout and WriteTimeout cover the
		// ordinary routes; the ones that carry a file (backup download, restore,
		// receipt upload) extend their own per request (internal/http,
		// extendDeadlines).
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
	requests := newRequestTracker()
	requests.attach(srv)
	return srv, requests
}

// The server's deadlines for an ordinary request (see newServer).
const (
	// serverReadTimeout bounds the whole request, body included. Every JSON body
	// is capped small (maxJSONBodyBytes), so 30s is generous even on a bad phone
	// connection; the file routes extend it.
	serverReadTimeout = 30 * time.Second
	// serverWriteTimeout bounds handler plus response. Longer than the read side
	// because it also covers the work: the monthly report PDF and a balances walk
	// are seconds at most, but a slow reader of the embedded SPA bundle is not.
	serverWriteTimeout = 60 * time.Second
	// serverIdleTimeout closes a keep-alive connection nobody is using. Above
	// Caddy's 2 minute upstream idle default, so Caddy retires its pooled
	// connection first and never sends a request down one this side just closed.
	serverIdleTimeout = 3 * time.Minute
)

// The shutdown budget. docker-compose gives the container stop_grace_period 30s
// before SIGKILL, so the three bounded waits below sum to 23s at worst and
// leave 7s for the database close, the instance lock release and the runtime's
// own exit. In practice the first wait is the only long one: handlers return in
// milliseconds once their context is cancelled, and the scheduler has been
// stopping since the signal.
const (
	httpShutdownTimeout      = 8 * time.Second
	handlerShutdownTimeout   = 5 * time.Second
	schedulerShutdownTimeout = 10 * time.Second
)

// shutdownBounds are the three waits shutdown makes, in order; the constants
// above are what serve passes, tests pass milliseconds.
type shutdownBounds struct {
	drain     time.Duration // srv.Shutdown: let requests in flight finish
	handlers  time.Duration // after the forced end: wait for handlers to return
	scheduler time.Duration // wait for the backup scheduler's tick to finish
}

// errHandlersOutlivedShutdown is what serve exits non-zero for: a handler was
// still running, past the forced end, when the database was about to close.
var errHandlersOutlivedShutdown = errors.New("requests still running at shutdown")

// shutdown stops the server, ends whatever the drain could not finish, then
// waits for the scheduler to finish its tick, each for a bounded time. The
// scheduler waits last but has been stopping the whole time: it shares serve's
// ctx, which the signal already cancelled. The caller's deferred sqlDB.Close
// runs after this returns, which is the point: nothing should still be serving
// a request or writing a dump when the database closes.
//
// Past the drain bound the requests are ended, not abandoned: their contexts
// are cancelled (a restore's transaction rolls back and the handler answers
// with an error) and the connections are closed. shutdown then waits, bounded,
// for the handlers to actually return, because Close does not.
//
// The result is nil when every handler returned, even if the drain needed the
// forced end (logged as a warning): the database closed with no request on it,
// which is a clean stop with an interrupted request, not a failed one. It is
// errHandlersOutlivedShutdown when a handler ignored the cancellation and was
// still running at the end of the handler bound. Waiting longer would only get
// the container SIGKILLed mid-close, so shutdown logs it, carries on to the
// scheduler, lets the caller close the database under that handler, and the
// non-zero exit tells the operator a request was cut off.
func shutdown(srv *http.Server, requests *requestTracker, scheduler *schedulerJoin, bounds shutdownBounds, logger *slog.Logger) error {
	httpCtx, cancel := context.WithTimeout(context.Background(), bounds.drain)
	defer cancel()
	if err := srv.Shutdown(httpCtx); err != nil {
		logger.Warn("http server did not drain in time, ending the requests still running", "timeout", bounds.drain.String(), "error", err)
		requests.end()
		_ = srv.Close() // drops the connections; the handlers are waited for below
	}

	var result error
	if !waitFor(requests.wait(), bounds.handlers) {
		logger.Error("requests still running at shutdown, closing the database anyway", "timeout", bounds.handlers.String())
		result = errHandlersOutlivedShutdown
	}

	if !scheduler.wait(bounds.scheduler) {
		logger.Error("backup scheduler still running at shutdown, closing the database anyway", "timeout", bounds.scheduler.String())
	}
	return result
}

// schedulerJoin is the one wait on the backup scheduler. shutdown makes it on
// the signal path and serve's deferred cleanup makes it on every path, so the
// first call does the waiting and later calls return its outcome at once.
// Without that, a join that timed out in shutdown would be waited on a second
// time and the worst case would pass docker's stop_grace_period.
type schedulerJoin struct {
	done <-chan struct{}
	once sync.Once
	ok   bool
}

func newSchedulerJoin(done <-chan struct{}) *schedulerJoin { return &schedulerJoin{done: done} }

// wait reports whether the scheduler finished, waiting at most d the first time.
func (j *schedulerJoin) wait(d time.Duration) bool {
	j.once.Do(func() { j.ok = waitFor(j.done, d) })
	return j.ok
}

// waitFor reports whether done closed within d.
func waitFor(done <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// newLogger builds the process's one logger. Text to stderr by default, JSON
// when the operator ships logs somewhere that parses them (ADR-022).
//
// Output is operator-facing and public: never log a member name, a note or an
// amount. Log IDs (PRD section 6, data minimization).
func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == config.LogFormatJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
