package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/lock"
)

// errCreateUserUsage is returned for a wrong argument count, showing the shape
// ADR-019's table gives the command.
var errCreateUserUsage = errors.New("usage: uruni create-user <email> <password>")

// createUser is ADR-019's `create-user <email> <password>` (#287): create the
// instance's login, or reset its password when the email already names it.
// `make dev-user` calls it in dev; on a deployment it is the way back in for a
// treasurer who forgot their password.
//
// Unlike seed-e2e it goes through config.Load, like `serve` and `migrate`: it
// writes the database an operator's server reads, so it resolves URUNI_DB and
// refuses an unconfigured instance the same way. It migrates first, so it also
// works on a database no server has opened.
//
// The password is never printed or logged; the email is echoed only to the
// operator's terminal, on stdout.
func createUser(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 2 {
		return errCreateUserUsage
	}
	// Echoed in the form it is stored and matched in (#500), so the operator reads
	// the address she must log in with, whatever case she typed.
	email, password := auth.NormalizeEmail(args[0]), args[1]

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	logger := newLogger(cfg, os.Stderr)
	if err := migrateForCreateUser(ctx, sqlDB, cfg, logger); err != nil {
		return err
	}
	// It writes into the operator's real database, so it asks `serve`'s question
	// (ADR-025) rather than fail on a missing column.
	if err := verifySchema(ctx, sqlDB, cfg.DBPath); err != nil {
		return err
	}

	created, err := auth.New(sqlDB).SetLogin(ctx, email, password)
	switch {
	case errors.Is(err, auth.ErrOtherAccountExists):
		return fmt.Errorf("%s holds one login and it is not %s - run create-user with that login's email to reset its password", cfg.DBPath, email)
	case err != nil:
		return err
	}

	if created {
		_, err = fmt.Fprintf(out, "created login %s\n", email)
	} else {
		_, err = fmt.Fprintf(out, "reset the password for %s and signed out every session\n", email)
	}
	return err
}

// migrateForCreateUser brings the database to this binary's schema, behind the
// same guard as serve and migrate up. The instance lock is taken only when a
// migration is actually pending: create-user is also how a treasurer who
// forgot their password gets back in, run against a live deployment, and there
// the server holds the lock and has already migrated, so there is nothing to
// guard or to refuse. A pending migration under a live server is exactly what
// the lock exists to stop (#494).
func migrateForCreateUser(ctx context.Context, sqlDB *sql.DB, cfg config.Config, logger *slog.Logger) error {
	migrations, err := db.Status(ctx, sqlDB)
	if err != nil {
		return err
	}
	pending := false
	for _, m := range migrations {
		pending = pending || !m.Applied
	}
	if pending {
		instanceLock, err := lock.Acquire(lock.PathFor(cfg.DBPath))
		if errors.Is(err, lock.ErrLocked) {
			return fmt.Errorf("%w: %s needs migrating first - stop it and run create-user again", ErrServerRunning, cfg.DBPath)
		}
		if err != nil {
			return err
		}
		defer func() { _ = instanceLock.Release() }()

		if err := config.EnsureBackupDirWritable(cfg.BackupDir); err != nil {
			return err
		}
		if err := guardBeforeMigrating(ctx, sqlDB, cfg, logger); err != nil {
			return err
		}
	}
	if _, err := db.Up(ctx, sqlDB, logger); err != nil {
		return fmt.Errorf("migrating %s: %w", cfg.DBPath, err)
	}
	return nil
}
