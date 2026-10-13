package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"

	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/lock"
)

// Tests for #494: `migrate down` must not empty the database by accident. See
// migrate.go.

// migrateEnv points the CLI at a fresh database, backup and uploads directory,
// and returns the database path and the backup directory.
func migrateEnv(t *testing.T) (dbPath, backupDir string) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "uruni.db")
	backupDir = filepath.Join(dir, "backups")
	uploadsDir := filepath.Join(dir, "uploads")
	for _, d := range []string{backupDir, uploadsDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("URUNI_DB", dbPath)
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_BACKUP_DIR", backupDir)
	t.Setenv("URUNI_UPLOADS_DIR", uploadsDir)
	return dbPath, backupDir
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestMigrateDownOnAnEmptyDatabaseIsNotAFailure(t *testing.T) {
	migrateEnv(t)
	var out bytes.Buffer
	if err := migrate(context.Background(), []string{"down"}, &out); err != nil {
		t.Fatalf("migrate([down]) on version 0 = %v, want no error", err)
	}
	if !strings.Contains(out.String(), "nothing to roll back") {
		t.Errorf("migrate([down]) printed %q, want the non-event reported", out.String())
	}
}

func TestMigrateDownRefusesTheBaseline(t *testing.T) {
	dbPath, backupDir := migrateEnv(t)
	ctx := context.Background()
	if err := migrate(ctx, []string{"up"}, io.Discard); err != nil {
		t.Fatalf("migrate([up]) = %v", err)
	}

	err := migrate(ctx, []string{"down"}, io.Discard)
	if !errors.Is(err, ErrBaselineDown) {
		t.Fatalf("migrate([down]) = %v, want ErrBaselineDown", err)
	}
	if strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "restore a backup") {
		t.Errorf("refusal = %q, want one line pointing at restoring a backup", err)
	}

	// Nothing was touched: the baseline is still applied, its tables are there,
	// and no dump was written for a rollback that never ran.
	sqlDB, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if !tableExists(t, sqlDB, "user") || !tableExists(t, sqlDB, "fund") {
		t.Errorf("the baseline's tables are gone after a refused down")
	}
	statuses, err := db.Status(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) == 0 || !statuses[0].Applied {
		t.Errorf("status after a refused down = %+v, want the baseline still applied", statuses)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused down wrote %d file(s) to the backup dir, want none", len(entries))
	}
}

func TestMigrateUpAndDownRefuseWhileTheInstanceLockIsHeld(t *testing.T) {
	dbPath, _ := migrateEnv(t)
	ctx := context.Background()
	if err := migrate(ctx, []string{"up"}, io.Discard); err != nil {
		t.Fatalf("migrate([up]) = %v", err)
	}

	held, err := lock.Acquire(lock.PathFor(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	for _, verb := range []string{"up", "down"} {
		err := migrate(ctx, []string{verb}, io.Discard)
		if !errors.Is(err, ErrServerRunning) {
			t.Errorf("migrate([%s]) under a held lock = %v, want ErrServerRunning", verb, err)
			continue
		}
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("migrate([%s]) refusal = %q, want one line", verb, err)
		}
	}

	// status only reads, and an operator runs it while a server is up.
	var out bytes.Buffer
	if err := migrate(ctx, []string{"status"}, &out); err != nil {
		t.Fatalf("migrate([status]) under a held lock = %v, want no error", err)
	}
	if !strings.Contains(out.String(), "00001_schema.sql") {
		t.Errorf("migrate([status]) printed %q, want the migration list", out.String())
	}

	// The refusal is about the holder, not the file: released, up works again.
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, []string{"up"}, io.Discard); err != nil {
		t.Errorf("migrate([up]) after the lock was released = %v, want no error", err)
	}
}

// twoVersionMigrations is the real baseline plus a throwaway second migration;
// the real set has one file through 0.x (ADR-025), so a down allowed to run is
// reachable no other way.
func twoVersionMigrations(t *testing.T) fs.FS {
	t.Helper()
	baseline, err := os.ReadFile(filepath.Join("..", "..", "internal", "db", "migrations", "00001_schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{
		"00001_schema.sql": {Data: baseline},
		"00002_extra.sql": {Data: []byte("-- +goose Up\nCREATE TABLE extra (id INTEGER PRIMARY KEY) STRICT;\n" +
			"-- +goose Down\nDROP TABLE extra;\n")},
	}
}

// openAtTwoVersions returns a database with both versions of fsys applied.
func openAtTwoVersions(t *testing.T, fsys fs.FS) *sql.DB {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "uruni.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	p, err := goose.NewProvider(goose.DialectSQLite3, sqlDB, fsys, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return sqlDB
}

func tableExists(t *testing.T, sqlDB *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := sqlDB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrateDownAboveTheBaselineWritesADumpFirst(t *testing.T) {
	_, backupDir := migrateEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	fsys := twoVersionMigrations(t)
	sqlDB := openAtTwoVersions(t, fsys)

	calls := 0
	dump := func(ctx context.Context) (string, error) {
		// The rollback must not have happened yet when the dump runs.
		if !tableExists(t, sqlDB, "extra") {
			t.Error("the dump ran after the rollback, want it before")
		}
		calls++
		return dumpBeforeDown(sqlDB, cfg)(ctx)
	}

	var out bytes.Buffer
	if err := migrateDown(context.Background(), sqlDB, fsys, quietLogger(), &out, dump); err != nil {
		t.Fatalf("migrateDown() = %v, want no error", err)
	}
	if calls != 1 {
		t.Fatalf("dump ran %d times, want once", calls)
	}
	if !strings.Contains(out.String(), "rolled back migration 2") {
		t.Errorf("printed %q, want the rollback reported", out.String())
	}
	if tableExists(t, sqlDB, "extra") {
		t.Errorf("migration 2 was not rolled back")
	}
	if !tableExists(t, sqlDB, "user") {
		t.Errorf("the baseline's tables were dropped, want one step only")
	}
	dumps, err := backup.ListDumps(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dumps) != 1 || dumps[0].Kind != backup.KindPreRestore {
		t.Errorf("backup dir holds %+v, want exactly one pre-restore dump", dumps)
	}

	// The next down would be the baseline: refused, and no second dump.
	err = migrateDown(context.Background(), sqlDB, fsys, quietLogger(), io.Discard, dump)
	if !errors.Is(err, ErrBaselineDown) {
		t.Errorf("second migrateDown() = %v, want ErrBaselineDown", err)
	}
	if calls != 1 {
		t.Errorf("a refused baseline down still ran the dump")
	}
}

func TestMigrateDownRefusesWhenTheDumpFails(t *testing.T) {
	migrateEnv(t)
	fsys := twoVersionMigrations(t)
	sqlDB := openAtTwoVersions(t, fsys)

	t.Run("dump returns an error", func(t *testing.T) {
		boom := errors.New("disk full")
		err := migrateDown(context.Background(), sqlDB, fsys, quietLogger(), io.Discard,
			func(context.Context) (string, error) { return "", boom })
		if !errors.Is(err, boom) {
			t.Fatalf("migrateDown() = %v, want the dump's error", err)
		}
		if !tableExists(t, sqlDB, "extra") {
			t.Errorf("migration 2 was rolled back although the dump failed")
		}
	})

	t.Run("backup dir is missing", func(t *testing.T) {
		cfg, err := config.Load()
		if err != nil {
			t.Fatal(err)
		}
		cfg.BackupDir = filepath.Join(t.TempDir(), "no-such-dir")
		err = migrateDown(context.Background(), sqlDB, fsys, quietLogger(), io.Discard, dumpBeforeDown(sqlDB, cfg))
		if err == nil {
			t.Fatal("migrateDown() with an unwritable backup dir = nil, want a refusal")
		}
		if !tableExists(t, sqlDB, "extra") {
			t.Errorf("migration 2 was rolled back although no dump could be written")
		}
	})
}
