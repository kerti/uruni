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
// `docker compose up` with no migration step: `serve` applies what is pending
// on boot (ADR-019). The distroless image has no .sql files and no shell.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNothingToRollBack is returned by Down when the database is at version 0.
// A sentinel so the CLI reports it as a non-event, not a failure.
var ErrNothingToRollBack = errors.New("no applied migration to roll back")

// ErrBaseline is returned by Down when the migration it would roll back is the
// lowest the binary carries. Rolling the baseline back leaves goose at version
// 0, an empty database with the account and ledger dropped; that is a
// restore's job, not a migration's (#494).
var ErrBaseline = errors.New("rolling back the baseline migration would empty the database")

// Migration is one migration's state, as `migrate status` prints it. It is
// this package's type, not goose's, so the CLI never imports goose and
// swapping the runner does not touch cmd/.
type Migration struct {
	Version int64
	// Name is the migration's file name, e.g. "00001_baseline.sql".
	Name string
	// Applied is false for a migration in the binary but not yet in this database.
	Applied bool
	// AppliedAt is the zero time when Applied is false.
	AppliedAt time.Time
}

// Up applies every pending migration in order, logs one line per migration
// applied, and reports how many. An already-current database logs nothing and
// returns 0: this runs on every boot, so silence is normal.
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

// Down rolls back exactly one migration, the most recently applied: `migrate
// down` is what an operator reaches for after a bad upgrade, and a flag away
// from wiping the ledger is the wrong shape for it (ADR-019).
//
// It refuses (ErrBaseline) when that migration is the lowest the binary
// carries, i.e. the step would leave version 0. It checks the migration set,
// not the number 1, so it stays right after 1.0 adds files.
//
// before, when non-nil, runs after the guards and before any change; its error
// refuses the rollback (the CLI writes a dump first). fsys is the migration
// set, nil for the embedded one; tests pass a set with more than one version
// because the real one has a single file through 0.x (ADR-025).
//
// It returns the version it rolled back. A database at version 0 is
// ErrNothingToRollBack, not a failure.
func Down(ctx context.Context, sqlDB *sql.DB, fsys fs.FS, logger *slog.Logger, before func(version int64) error) (int64, error) {
	p, err := providerFor(sqlDB, fsys)
	if err != nil {
		return 0, err
	}

	statuses, err := p.Status(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading migration status: %w", err)
	}
	var latest int64
	for _, s := range statuses {
		if s.State == goose.StateApplied && s.Source.Version > latest {
			latest = s.Source.Version
		}
	}
	if latest == 0 {
		return 0, ErrNothingToRollBack
	}
	if latest == statuses[0].Source.Version {
		return 0, fmt.Errorf("%w (migration %d)", ErrBaseline, latest)
	}

	if before != nil {
		if err := before(latest); err != nil {
			return 0, err
		}
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
// first. It changes nothing, not even migrating the database it is asked
// about, which is the point of being able to run it.
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

// provider builds goose over the embedded migrations, per call, so the
// caller's *sql.DB is the only long-lived thing.
//
// The provider API is deliberate: goose's package-level functions carry global
// state (a dialect, a registry), letting two databases in one process (dev and
// a test's temp file) interfere.
func provider(sqlDB *sql.DB) (*goose.Provider, error) {
	return providerFor(sqlDB, nil)
}

// providerFor is provider over an injectable migration set; nil means the
// embedded one. Only Down takes it, for its tests.
func providerFor(sqlDB *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	if fsys == nil {
		sub, err := fs.Sub(migrationsFS, "migrations")
		if err != nil {
			return nil, fmt.Errorf("reading the embedded migrations: %w", err)
		}
		fsys = sub
	}

	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, fsys,
		// goose's output is off: this package logs through the caller's slog.Logger so
		// migration lines look like every other operator log line (ADR-022).
		goose.WithVerbose(false),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("preparing the migration runner: %w", err)
	}
	return p, nil
}
