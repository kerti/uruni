package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"time"

	"github.com/pressly/goose/v3"
)

// The migrations travel inside the binary, so a self-hoster runs
// `docker compose up` and never a migration step: `serve` applies whatever is
// pending on boot (ADR-019). Embedding is what makes that possible — the
// distroless image has no .sql files and no shell to run them with.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNothingToRollBack is returned by Down when the database is already at
// version 0. A sentinel rather than a message, so the CLI can report it as the
// non-event it is instead of failing the command.
var ErrNothingToRollBack = errors.New("no applied migration to roll back")

// Migration is one migration's state, as `migrate status` prints it. It is this
// package's own type rather than goose's so the CLI never imports goose:
// migrations are a store concern, and swapping the runner should not be a change
// to cmd/.
type Migration struct {
	Version int64
	// Name is the migration's file name, e.g. "00001_baseline.sql".
	Name string
	// Applied is false for a migration present in the binary but not yet in this
	// database.
	Applied bool
	// AppliedAt is the zero time when Applied is false.
	AppliedAt time.Time
}

// Up applies every pending migration, in order, logs one line per migration
// applied, and reports how many that was. An already-current database logs
// nothing and returns 0 — this runs on every boot, so silence is the normal case.
func Up(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger) (int, error) {
	p, err := provider(sqlDB)
	if err != nil {
		return 0, err
	}

	results, err := p.Up(ctx)
	if err != nil {
		return 0, fmt.Errorf("applying migrations: %w", err)
	}

	for _, r := range results {
		logger.Info("migration applied", "version", r.Source.Version, "name", path.Base(r.Source.Path))
	}
	return len(results), nil
}

// Down rolls back exactly one migration — the most recently applied. One step,
// because `migrate down` is what an operator reaches for after a bad upgrade and
// a flag away from wiping the ledger is the wrong shape for that command
// (ADR-019).
// It returns the version it rolled back. A database already at version 0 is
// ErrNothingToRollBack, not a failure.
func Down(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger) (int64, error) {
	p, err := provider(sqlDB)
	if err != nil {
		return 0, err
	}

	result, err := p.Down(ctx)
	switch {
	case errors.Is(err, goose.ErrNoNextVersion):
		return 0, ErrNothingToRollBack
	case err != nil:
		return 0, fmt.Errorf("rolling back the last migration: %w", err)
	}

	logger.Info("migration rolled back", "version", result.Source.Version, "name", path.Base(result.Source.Path))
	return result.Source.Version, nil
}

// Status reports every migration the binary carries, applied or not, oldest
// first. It changes nothing — including not migrating the database it is asked
// about, which is the whole point of being able to run it.
func Status(ctx context.Context, sqlDB *sql.DB) ([]Migration, error) {
	p, err := provider(sqlDB)
	if err != nil {
		return nil, err
	}

	statuses, err := p.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading migration status: %w", err)
	}

	migrations := make([]Migration, 0, len(statuses))
	for _, s := range statuses {
		migrations = append(migrations, Migration{
			Version:   s.Source.Version,
			Name:      path.Base(s.Source.Path),
			Applied:   s.State == goose.StateApplied,
			AppliedAt: s.AppliedAt,
		})
	}
	return migrations, nil
}

// provider builds goose over the embedded migrations. Cheap enough to build per
// call, and building it per call keeps the caller's *sql.DB the only long-lived
// thing.
//
// The provider API is deliberate: goose's package-level functions carry global
// state (a dialect, a registry), which makes two databases in one process — the
// dev database and a test's temporary file — able to interfere.
func provider(sqlDB *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("reading the embedded migrations: %w", err)
	}

	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, fsys,
		// goose's own output is off: this package logs through the caller's
		// slog.Logger instead, so migration lines look like every other line in
		// the operator's logs (ADR-022).
		goose.WithVerbose(false),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("preparing the migration runner: %w", err)
	}
	return p, nil
}
