package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/lock"
)

// ErrServerRunning is what `migrate up` and `migrate down` return while another
// process (normally `serve`) holds the instance lock. `migrate status` never
// takes it.
var ErrServerRunning = errors.New("uruni is already running against this database")

// ErrBaselineDown is what `migrate down` returns when the step would roll the
// baseline back, i.e. leave the database empty (#494).
var ErrBaselineDown = errors.New("migrate down refuses to roll back the baseline migration, it would empty the database - restore a backup instead (Pengaturan > Cadangan, see SELF-HOSTING.md)")

// migrateUsage lists the three verbs ADR-019 pins. `down` is one step, never a
// range: it exists to back out a bad upgrade, and a flag away from dropping
// the ledger is the wrong shape.
const migrateUsage = "try: uruni migrate up | down | status"

// appliedAtFormat is local time to the second: `migrate status` is read by an
// operator comparing it to the upgrade, not parsed.
const appliedAtFormat = "2006-01-02 15:04:05"

// migrateTimeout bounds a CLI migration run. Generous: it exists so a lock held
// by another process fails with a message instead of hanging a terminal, not
// to police real work.
const migrateTimeout = 2 * time.Minute

// migrate runs one of the three migration verbs against the configured
// database, writing operator output to out. It opens the database itself: each
// verb is a whole process invocation, so there is no connection to inherit.
func migrate(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w - %s", ErrNoCommand, migrateUsage)
	}
	verb := args[0]
	// Checked before opening the database, so a typo doesn't create a file before
	// being rejected.
	if verb != "up" && verb != "down" && verb != "status" {
		return fmt.Errorf("%w: %q - %s", ErrUnknownCommand, verb, migrateUsage)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg, os.Stderr)

	// up and down change the schema, so they take the instance lock `serve` holds
	// at boot and refuse while it is held. status only reads and an operator runs
	// it while a server is up, so it stays lock-free.
	if verb != "status" {
		instanceLock, err := lock.Acquire(lock.PathFor(cfg.DBPath))
		if errors.Is(err, lock.ErrLocked) {
			return fmt.Errorf("%w: stop it before running migrate %s (migrate status works while it runs)", ErrServerRunning, verb)
		}
		if err != nil {
			return err
		}
		defer func() { _ = instanceLock.Release() }()
	}

	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	switch verb {
	case "up":
		return migrateUp(ctx, sqlDB, logger, out, cfg.DBPath)
	case "down":
		return migrateDown(ctx, sqlDB, nil, logger, out, dumpBeforeDown(sqlDB, cfg))
	default:
		return printMigrationStatus(ctx, sqlDB, out, cfg.DBPath)
	}
}

// migrateUp applies what is pending and says so either way. `serve` stays
// quiet when there is nothing; the CLI is the opposite, as a command printing
// nothing leaves the operator unsure it ran.
func migrateUp(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger, out io.Writer, dbPath string) error {
	applied, err := db.Up(ctx, sqlDB, logger)
	if err != nil {
		return err
	}

	if applied == 0 {
		_, err := fmt.Fprintf(out, "%s is already up to date\n", dbPath)
		return err
	}
	_, err = fmt.Fprintf(out, "applied %d migration(s) to %s\n", applied, dbPath)
	return err
}

// dumpBeforeDown is the dump `migrate down` takes before changing anything. It
// goes to `serve`'s dump directory so it shows in the Cadangan list; a missing
// or read-only directory fails here, before the rollback.
func dumpBeforeDown(sqlDB *sql.DB, cfg config.Config) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := config.EnsureBackupDirWritable(cfg.BackupDir); err != nil {
			return "", err
		}
		return backup.WritePreRestoreDump(ctx, sqlDB, cfg.UploadsDir, cfg.BackupDir, time.Now())
	}
}

// migrateDown reports an already-empty database as a non-event: rolling back
// nothing is not a failure, and a non-zero exit would break scripts that roll
// back before restoring a backup. Rolling back the baseline is refused
// (ErrBaselineDown). Any rollback that runs writes a dump first via dump, and
// a failing dump refuses it. fsys is the migration set, nil for the embedded
// one (see db.Down).
func migrateDown(ctx context.Context, sqlDB *sql.DB, fsys fs.FS, logger *slog.Logger, out io.Writer, dump func(context.Context) (string, error)) error {
	before := func(version int64) error {
		name, err := dump(ctx)
		if err != nil {
			return fmt.Errorf("could not write a backup before rolling back migration %d, so nothing was rolled back: %w", version, err)
		}
		_, err = fmt.Fprintf(out, "wrote backup %s before rolling back\n", name)
		return err
	}

	version, err := db.Down(ctx, sqlDB, fsys, logger, before)
	if errors.Is(err, db.ErrNothingToRollBack) {
		_, err := fmt.Fprintln(out, "nothing to roll back - the database is at version 0")
		return err
	}
	if errors.Is(err, db.ErrBaseline) {
		return ErrBaselineDown
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "rolled back migration %d\n", version)
	return err
}

// printMigrationStatus writes every migration the binary carries, applied or
// not. It names the database file first: the commonest surprise is having
// migrated a different one (URUNI_DB unset, or `.env` not exported).
func printMigrationStatus(ctx context.Context, sqlDB *sql.DB, out io.Writer, dbPath string) error {
	migrations, err := db.Status(ctx, sqlDB)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(out, "database: %s\n\n", dbPath); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "%-8s %-8s %-20s %s\n", "version", "state", "applied at", "name"); err != nil {
		return err
	}
	for _, m := range migrations {
		appliedAt := "-"
		if !m.AppliedAt.IsZero() {
			appliedAt = m.AppliedAt.Local().Format(appliedAtFormat)
		}
		state := "pending"
		if m.Applied {
			state = "applied"
		}
		if _, err := fmt.Fprintf(out, "%-8d %-8s %-20s %s\n", m.Version, state, appliedAt, m.Name); err != nil {
			return err
		}
	}

	return printSchemaStatus(ctx, sqlDB, out)
}

// printSchemaStatus says whether the live schema is the one this binary
// creates. goose's table knows only version numbers and ADR-025 edits the one
// file in place, so this line is the only place a stale database shows. A
// mismatch is reported, not returned as an error: status is a diagnostic.
func printSchemaStatus(ctx context.Context, sqlDB *sql.DB, out io.Writer) error {
	diff, err := db.CheckSchema(ctx, sqlDB)
	if err != nil {
		return err
	}
	if diff.Matches() {
		_, err = fmt.Fprintln(out, "\nschema: matches this version")
		return err
	}
	_, err = fmt.Fprintf(out, "\nschema: DIFFERS from this version (%s)\n"+
		"        see the release notes and the Upgrading section of SELF-HOSTING.md (ADR-025)\n",
		diff.Summary(schemaDiffNamesShown))
	return err
}
