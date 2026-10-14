package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
)

// guardBeforeMigrating is what every path that calls db.Up does first, under
// the instance lock (serve, migrate up, create-user; #517, ADR-012). It
// refuses a damaged file (#505), then takes the raw pre-upgrade snapshot when
// the running version is new. Both come first because once a migration has
// run the copy is no longer the state the previous binary left: a `migrate up`
// or `create-user` run after pulling a new image would otherwise leave the
// later `serve` snapshotting an already migrated file under a "from" label it
// does not match. If the snapshot cannot be written nothing migrates; the
// previous image still runs against the untouched file.
func guardBeforeMigrating(ctx context.Context, sqlDB *sql.DB, cfg config.Config, logger *slog.Logger) error {
	// Before anything is copied or migrated: serving a damaged file would put new
	// entries on pages SQLite can't read, and the snapshot and the boot dump
	// would write the damage into the backup directory.
	if err := db.QuickCheck(ctx, sqlDB); err != nil {
		return fmt.Errorf("database integrity check failed for %s - restore a backup (see SELF-HOSTING.md): %w", cfg.DBPath, err)
	}
	if err := backup.EnsurePreUpgradeSnapshot(ctx, sqlDB, cfg.BackupDir, version, time.Now(), logger); err != nil {
		return fmt.Errorf("not migrating without a snapshot to go back to in %s (free space there, or run the previous URUNI_TAG again): %w", cfg.BackupDir, err)
	}
	return nil
}

// openDatabase is serve's startup up to "safe to serve", under the instance
// lock: open, guard, migrate, check the schema, then record the boot. The
// caller closes the returned handle; on error it is already closed.
func openDatabase(ctx context.Context, cfg config.Config, logger *slog.Logger) (*sql.DB, *db.FileIdentity, error) {
	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*sql.DB, *db.FileIdentity, error) {
		_ = sqlDB.Close()
		return nil, nil, err
	}

	// Which file the path names now, so /healthz can tell if it is later deleted
	// or replaced under this process (#278).
	dbFile, err := db.IdentifyFile(cfg.DBPath)
	if err != nil {
		return fail(err)
	}

	if err := guardBeforeMigrating(ctx, sqlDB, cfg, logger); err != nil {
		return fail(err)
	}

	// Migrations run on boot, so self-hosting stays `docker compose up` with no
	// migration step (ADR-019). The cost is a slower first boot after an
	// upgrade, the right side of that trade for one small instance.
	if _, err := db.Up(ctx, sqlDB, logger); err != nil {
		return fail(err)
	}

	// ADR-025: goose cannot see that the one migration file was edited in place,
	// so an older database migrates "successfully" and stays on the old schema.
	// Refuse before serving or writing, rather than let /healthz say ok and the
	// treasurer meet 500s.
	if err := verifySchema(ctx, sqlDB, cfg.DBPath); err != nil {
		return fail(err)
	}

	// The boot has succeeded: only now does this become the version a later boot
	// is compared with. A failure above leaves the marker saying a snapshot was
	// taken for this version, so the restart loop writes no second one. Not
	// fatal: the server is fine, and a marker that cannot be written is removed
	// (see MarkBooted), which costs at most one extra snapshot at the next upgrade.
	if err := backup.MarkBooted(cfg.BackupDir, version); err != nil {
		logger.Error("could not record the booted version", "error", err)
	}
	return sqlDB, dbFile, nil
}
