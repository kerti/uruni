package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
)

// migrateUsage lists the three verbs ADR-019 pins. `down` is one step, never a
// range: the command exists for backing out a bad upgrade, and a flag away from
// dropping the whole ledger is the wrong shape for that.
const migrateUsage = "try: uruni migrate up | down | status"

// appliedAtFormat is local time, seconds resolution — `migrate status` is read by
// an operator comparing it against when they ran the upgrade, not parsed.
const appliedAtFormat = "2006-01-02 15:04:05"

// migrateTimeout bounds a CLI migration run. Generous on purpose: the bound
// exists so a lock held by another process fails with a message instead of
// hanging a terminal forever, not to police how long real work may take.
const migrateTimeout = 2 * time.Minute

// migrate runs one of the three migration verbs against the configured database,
// writing operator output to out. It opens the database itself rather than taking
// one: each verb is a whole process invocation (`uruni migrate up`), so there is
// no connection to inherit.
func migrate(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w — %s", ErrNoCommand, migrateUsage)
	}
	verb := args[0]
	// Checked before opening the database, so a typo doesn't create a file on the
	// way to being rejected.
	if verb != "up" && verb != "down" && verb != "status" {
		return fmt.Errorf("%w: %q — %s", ErrUnknownCommand, verb, migrateUsage)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg, os.Stderr)

	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	switch verb {
	case "up":
		return migrateUp(ctx, sqlDB, logger, out, cfg.DBPath)
	case "down":
		return migrateDown(ctx, sqlDB, logger, out)
	default:
		return printMigrationStatus(ctx, sqlDB, out, cfg.DBPath)
	}
}

// migrateUp applies what is pending and says so either way. `serve` logs its
// migrations and stays quiet when there are none; the CLI is the opposite — a
// command that prints nothing leaves the operator unsure it ran.
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

// migrateDown reports an already-empty database as the non-event it is: rolling
// back nothing is not a failure, and exiting non-zero for it would break any
// script that rolls back before restoring a backup.
func migrateDown(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger, out io.Writer) error {
	version, err := db.Down(ctx, sqlDB, logger)
	if errors.Is(err, db.ErrNothingToRollBack) {
		_, err := fmt.Fprintln(out, "nothing to roll back — the database is at version 0")
		return err
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "rolled back migration %d\n", version)
	return err
}

// printMigrationStatus writes every migration the binary carries, applied or not.
// It names the database file first: the commonest migration surprise is having
// migrated a different one than you meant to (URUNI_DB unset, or `.env` not
// exported).
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
	return nil
}
