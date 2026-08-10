// Command uruni is the whole product: one binary that serves the API, the
// public report and the embedded SPA (ADR-001). Its subcommand surface is
// pinned by ADR-019 — the Makefile and the container HEALTHCHECK are written
// against that table, so it is a contract, not a convenience.
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
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
	uruniHTTP "github.com/kerti/uruni/internal/http"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "uruni: %v\n", err)
		os.Exit(1)
	}
}

// The CLI and the server logs are the *operator's* surface, so they are in
// English like the rest of the self-hosting documentation. Indonesian is for
// the treasurer's surface — the SPA and the public report (ADR-014).
var (
	// ErrNoCommand and ErrUnknownCommand are sentinels so callers and tests can
	// branch on identity rather than on message text.
	ErrNoCommand      = errors.New("no command given")
	ErrUnknownCommand = errors.New("unknown command")
)

// usage lists the subcommands that exist *today*. ADR-019's table also holds
// `create-user` and `seed-e2e`; each lands with the milestone that gives it
// something to do (M5, and whenever fixtures exist), and until then it is not
// advertised here.
const usage = "try: uruni serve | migrate up|down|status | version | healthcheck"

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w — %s", ErrNoCommand, usage)
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
	default:
		return fmt.Errorf("%w: %q — %s", ErrUnknownCommand, args[0], usage)
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	assets, err := uruni.WebAssets()
	if err != nil {
		return fmt.Errorf("opening the embedded web assets: %w", err)
	}

	// One logger, built at startup and passed down — no package-level global
	// (ADR-022). Nothing below main needs it yet; request logging arrives as
	// middleware at M4, on chi (ADR-021).
	logger := newLogger(cfg, os.Stderr)

	// SIGINT/SIGTERM close in-flight requests cleanly — `make restart` and
	// `docker compose down` both stop the process this way. Established before
	// the store opens so a Ctrl-C during a long first migration is honoured.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The store, opened once for the process (ADR-004: one connection, so
	// everything serializes). M4's handlers take it; for now it is what the
	// migrations run against.
	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	// Migrations run on boot, so self-hosting stays `docker compose up` with no
	// migration step for the operator (ADR-019). The cost is a slower first boot
	// after an upgrade, which is the right side of that trade for one small
	// instance per community.
	if _, err := db.Up(ctx, sqlDB, logger); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: uruniHTTP.New(assets, uruniHTTP.Build{Version: version, Commit: buildCommit()}),
		// Set explicitly: a server with no header timeout can be held open by a
		// slow client indefinitely.
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		// The version goes in the boot line on purpose: it is the first thing
		// worth knowing when an operator pastes their logs into an issue.
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// newLogger builds the one logger the process uses. Text to stderr by default,
// JSON when the operator ships logs somewhere that parses them (ADR-022).
//
// Whatever ends up here is operator-facing and public: never log a member name,
// a note, or an amount. Log IDs (PRD §6, data minimization).
func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == config.LogFormatJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
