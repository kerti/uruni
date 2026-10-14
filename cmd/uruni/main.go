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
	"syscall"
	"time"

	"github.com/kerti/uruni"
	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
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
const usage = "try: uruni serve | migrate up|down|status | create-user <email> <password> | version | healthcheck | seed-e2e"

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
		return createUser(context.Background(), args[1:], os.Stdout)
	case "seed-e2e":
		return seedE2E(context.Background())
	default:
		return fmt.Errorf("%w: %q - %s", ErrUnknownCommand, args[0], usage)
	}
}

func serve() error {
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The store, opened once for the process (ADR-004: one connection, so
	// everything serializes).
	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	// Which file the path names now, so /healthz can tell if it is later deleted
	// or replaced under this process (#278).
	dbFile, err := db.IdentifyFile(cfg.DBPath)
	if err != nil {
		return err
	}

	// Migrations run on boot, so self-hosting stays `docker compose up` with no
	// migration step (ADR-019). The cost is a slower first boot after an
	// upgrade, the right side of that trade for one small instance.
	if _, err := db.Up(ctx, sqlDB, logger); err != nil {
		return err
	}

	// ADR-025: goose cannot see that the one migration file was edited in place,
	// so an older database migrates "successfully" and stays on the old schema.
	// Refuse before serving or writing, rather than let /healthz say ok and the
	// treasurer meet 500s.
	if err := verifySchema(ctx, sqlDB, cfg.DBPath); err != nil {
		return err
	}

	// A damaged file is refused too (#505): serving it would put new entries on
	// pages SQLite can't read, and the boot dump would write the damage into the
	// backup directory.
	if err := db.QuickCheck(ctx, sqlDB); err != nil {
		return fmt.Errorf("database integrity check failed for %s - restore a backup (see SELF-HOSTING.md): %w", cfg.DBPath, err)
	}

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
	// Every return path, not only the signal: a listener that fails at boot
	// (port in use) returns through errc with the scheduler still running.
	// Deferred after sqlDB.Close, so it runs first. On the signal path shutdown
	// has already joined, and this returns at once.
	defer func() {
		stop()
		waitFor(schedulerDone, schedulerShutdownTimeout)
	}()

	srv := &http.Server{
		Addr: fmt.Sprintf(":%d", cfg.Port),
		Handler: uruniHTTP.New(assets, uruniHTTP.Build{Version: version, Commit: buildCommit()}, l, q, sqlDB, dbFile.Check, logger,
			au, cfg.BaseURL, cfg.UploadsDir, cfg.BackupDir, cfg.TrustedProxies),
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
		return shutdown(srv, schedulerDone, httpShutdownTimeout, schedulerShutdownTimeout, logger)
	}
}

// The server's deadlines for an ordinary request (see the http.Server literal
// in serve).
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
// before SIGKILL, so the two bounded waits below sum to 20s and leave 10s for
// the database close, the instance lock release and the runtime's own exit.
const (
	httpShutdownTimeout      = 10 * time.Second
	schedulerShutdownTimeout = 10 * time.Second
)

// shutdown stops the server, then waits for the scheduler to finish its tick,
// each for a bounded time. The scheduler waits second but has been stopping the
// whole time: it shares serve's ctx, which the signal already cancelled. The
// caller's deferred sqlDB.Close runs after this returns, which is the point:
// nothing should still be writing a dump when the database closes.
//
// A timeout on either side is logged and shutdown carries on to the other, so a
// hung drain cannot also skip the join. The HTTP error is the one returned.
func shutdown(srv *http.Server, schedulerDone <-chan struct{}, httpTimeout, schedulerTimeout time.Duration, logger *slog.Logger) error {
	httpCtx, cancel := context.WithTimeout(context.Background(), httpTimeout)
	defer cancel()
	httpErr := srv.Shutdown(httpCtx)
	if httpErr != nil {
		logger.Error("http server did not drain in time", "timeout", httpTimeout.String(), "error", httpErr)
	}

	if !waitFor(schedulerDone, schedulerTimeout) {
		logger.Error("backup scheduler still running at shutdown, closing the database anyway", "timeout", schedulerTimeout.String())
	}
	return httpErr
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
