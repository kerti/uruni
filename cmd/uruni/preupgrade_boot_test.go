package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/lock"
)

// Tests for #517 through serve itself: the snapshot is taken before the
// migration, and a boot that fails after it does not take another on retry.

// setVersion stamps the running version for one test, as the linker would.
// Tests that use it cannot run in parallel (nor can any that set env).
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// legacyInstance writes a populated database that is not this binary's schema:
// one table of its own and no goose bookkeeping. serve migrates it and then
// refuses it (ErrSchemaMismatch), so serve returns instead of listening, and
// the migration is visible as the live file gaining the fund table.
func legacyInstance(t *testing.T) (dbPath, backupDir string) {
	t.Helper()
	dbPath, backupDir = migrateEnv(t)
	sqlDB, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE legacy (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath, backupDir
}

func tablesOf(t *testing.T, path string) map[string]bool {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("db.Open(%s) = %v", path, err)
	}
	defer func() { _ = sqlDB.Close() }()
	rows, err := sqlDB.Query(`SELECT name FROM sqlite_schema WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	tables := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables[n] = true
	}
	return tables
}

func TestServeSnapshotsBeforeMigratingAndNotAgainWhenTheBootIsRetried(t *testing.T) {
	dbPath, backupDir := legacyInstance(t)
	if err := backup.MarkBooted(backupDir, "v1"); err != nil {
		t.Fatal(err)
	}
	setVersion(t, "v2")

	if err := serve(); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("serve() = %v, want ErrSchemaMismatch", err)
	}

	snaps, err := backup.ListPreUpgradeSnapshots(backupDir)
	if err != nil || len(snaps) != 1 || snaps[0].From != "v1" || snaps[0].To != "v2" {
		t.Fatalf("snapshots = %+v, %v, want one from v1 to v2", snaps, err)
	}
	before := tablesOf(t, filepath.Join(backupDir, snaps[0].Name))
	if !before["legacy"] || before["fund"] {
		t.Errorf("snapshot tables = %v, want the state before the migration (legacy, no fund)", before)
	}
	if live := tablesOf(t, dbPath); !live["fund"] {
		t.Errorf("live tables = %v, want the migration to have run after the snapshot", live)
	}
	marker, err := os.ReadFile(filepath.Join(backupDir, "uruni-boot-version")) //nolint:gosec // the test's own temp dir
	if err != nil || !strings.Contains(string(marker), "booted v1") {
		t.Errorf("marker = %q, %v, want booted to stay v1 after a failed boot", marker, err)
	}

	// Docker restarts the refusing container: the same version, no new copy.
	for range 3 {
		if err := serve(); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("retry serve() = %v, want ErrSchemaMismatch", err)
		}
	}
	if again, _ := backup.ListPreUpgradeSnapshots(backupDir); len(again) != 1 || again[0].Name != snaps[0].Name {
		t.Errorf("snapshots after retries = %+v, want only the first", again)
	}
}

func TestServeTakesNoSnapshotOfADamagedDatabase(t *testing.T) {
	path := damagedDatabase(t)
	backupDir := t.TempDir()
	t.Setenv("URUNI_DB", path)
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_UPLOADS_DIR", t.TempDir())
	t.Setenv("URUNI_BACKUP_DIR", backupDir)
	setVersion(t, "v2")

	if err := serve(); !errors.Is(err, db.ErrIntegrity) {
		t.Fatalf("serve() = %v, want db.ErrIntegrity", err)
	}
	if snaps, _ := backup.ListPreUpgradeSnapshots(backupDir); len(snaps) != 0 {
		t.Errorf("snapshots = %+v, want none of a damaged file", snaps)
	}
}

// onlySnapshot asserts the backup dir holds exactly one snapshot, labelled to
// the given version, and that it still shows the pre-migration state of a
// legacyInstance (the legacy table, none of the schema's).
func onlySnapshot(t *testing.T, backupDir, wantTo string) {
	t.Helper()
	snaps, err := backup.ListPreUpgradeSnapshots(backupDir)
	if err != nil || len(snaps) != 1 || snaps[0].To != wantTo {
		t.Fatalf("snapshots = %+v, %v, want exactly one to %s", snaps, err, wantTo)
	}
	if tables := tablesOf(t, filepath.Join(backupDir, snaps[0].Name)); !tables["legacy"] || tables["fund"] {
		t.Errorf("snapshot tables = %v, want the state before any migration", tables)
	}
}

func TestMigrateUpTakesTheSnapshotAndTheLaterServeDoesNotRepeatIt(t *testing.T) {
	dbPath, backupDir := legacyInstance(t)
	setVersion(t, "v2")

	if err := migrate(context.Background(), []string{"up"}, io.Discard); err != nil {
		t.Fatalf("migrate([up]) = %v", err)
	}
	if !tablesOf(t, dbPath)["fund"] {
		t.Fatal("migrate up did not migrate")
	}
	onlySnapshot(t, backupDir, "v2")

	// serve now finds a migrated file and the snapshot already taken.
	if err := serve(); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("serve() = %v, want ErrSchemaMismatch (the legacy table)", err)
	}
	onlySnapshot(t, backupDir, "v2")
}

func TestCreateUserTakesTheSnapshotAndTheLaterServeDoesNotRepeatIt(t *testing.T) {
	dbPath, backupDir := legacyInstance(t)
	setVersion(t, "v2")

	err := createUser(context.Background(), []string{"treasurer@example.org", "correct-horse-battery"}, io.Discard)
	if !errors.Is(err, ErrSchemaMismatch) { // it migrated, then refused the legacy table
		t.Fatalf("createUser() = %v, want ErrSchemaMismatch", err)
	}
	if !tablesOf(t, dbPath)["fund"] {
		t.Fatal("create-user did not migrate")
	}
	onlySnapshot(t, backupDir, "v2")

	if err := serve(); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("serve() = %v, want ErrSchemaMismatch", err)
	}
	onlySnapshot(t, backupDir, "v2")
}

func TestMigrateUpAndCreateUserStopWithNoBackupDirectory(t *testing.T) {
	dbPath, backupDir := legacyInstance(t)
	setVersion(t, "v2")
	if err := os.RemoveAll(backupDir); err != nil {
		t.Fatal(err)
	}

	// The same refusal serve gives, before anything is copied or migrated.
	if err := migrate(context.Background(), []string{"up"}, io.Discard); !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("migrate([up]) = %v, want ErrInvalidConfig", err)
	}
	if err := createUser(context.Background(), []string{"treasurer@example.org", "correct-horse-battery"}, io.Discard); !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("createUser() = %v, want ErrInvalidConfig", err)
	}
	if tablesOf(t, dbPath)["fund"] {
		t.Error("the database was migrated with no snapshot")
	}
}

// create-user is also the password reset on a live deployment, where serve
// holds the lock and has already migrated: that must keep working, and take
// no snapshot. A pending migration under a live server is refused.
func TestCreateUserWorksUnderALiveServerOnlyWhenNothingIsPending(t *testing.T) {
	dbPath, backupDir := migrateEnv(t)
	setVersion(t, "v2")
	held, err := lock.Acquire(lock.PathFor(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	err = createUser(context.Background(), []string{"treasurer@example.org", "correct-horse-battery"}, io.Discard)
	if !errors.Is(err, ErrServerRunning) {
		t.Fatalf("createUser() with a migration pending under a live server = %v, want ErrServerRunning", err)
	}

	// The server migrated (as serve would), then the operator resets a password.
	sqlDB, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Up(context.Background(), sqlDB, quietLogger()); err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	if err := createUser(context.Background(), []string{"treasurer@example.org", "correct-horse-battery"}, io.Discard); err != nil {
		t.Fatalf("createUser() with nothing pending = %v, want nil", err)
	}
	if snaps, _ := backup.ListPreUpgradeSnapshots(backupDir); len(snaps) != 0 {
		t.Errorf("snapshots = %+v, want none", snaps)
	}
}

func markerText(t *testing.T, backupDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(backupDir, "uruni-boot-version")) //nolint:gosec // the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func openDatabaseForTest(t *testing.T) (bytes.Buffer, error) {
	t.Helper()
	var logs bytes.Buffer
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _, err := openDatabase(context.Background(), cfg, newLogger(cfg, &logs))
	if err == nil {
		_ = sqlDB.Close()
	}
	return logs, err
}

// A successful boot is what advances "booted"; the restart after it writes
// nothing and logs no snapshot. Dropping MarkBooted from the boot fails this.
func TestASuccessfulBootAdvancesTheMarkerAndARestartWritesNothing(t *testing.T) {
	dbPath, backupDir := migrateEnv(t)
	// A previous release's database: migrated, and a marker from its last boot.
	prev, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Up(context.Background(), prev, quietLogger()); err != nil {
		t.Fatal(err)
	}
	_ = prev.Close()
	if err := backup.MarkBooted(backupDir, "v1"); err != nil {
		t.Fatal(err)
	}
	setVersion(t, "v2")

	logs, err := openDatabaseForTest(t)
	if err != nil {
		t.Fatalf("openDatabase() = %v", err)
	}
	if !strings.Contains(logs.String(), "wrote pre-upgrade snapshot") {
		t.Errorf("log = %q, want the snapshot line on the upgrade", logs.String())
	}
	if got := markerText(t, backupDir); got != "booted v2\n" {
		t.Fatalf("marker after a successful boot = %q, want only booted v2", got)
	}
	snaps, _ := backup.ListPreUpgradeSnapshots(backupDir)
	if len(snaps) != 1 || snaps[0].From != "v1" || snaps[0].To != "v2" {
		t.Fatalf("snapshots = %+v, want one from v1 to v2", snaps)
	}

	before := dirNames(t, backupDir)
	logs, err = openDatabaseForTest(t)
	if err != nil {
		t.Fatalf("restart openDatabase() = %v", err)
	}
	if after := dirNames(t, backupDir); strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("backup dir changed on a restart: %v -> %v", before, after)
	}
	if strings.Contains(logs.String(), "snapshot") {
		t.Errorf("restart log = %q, want no snapshot line", logs.String())
	}
}

// The server is fine and must keep starting when the marker cannot be updated.
func TestAMarkerThatCannotBeWrittenDoesNotStopTheBoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	dbPath, backupDir := migrateEnv(t)
	prev, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Up(context.Background(), prev, quietLogger()); err != nil {
		t.Fatal(err)
	}
	_ = prev.Close()
	if err := backup.MarkBooted(backupDir, "v1"); err != nil {
		t.Fatal(err)
	}
	setVersion(t, "v2")

	// A first attempt took the snapshot and recorded it, then the directory
	// stopped accepting writes: the guard has nothing to write, MarkBooted does.
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pre, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := backup.EnsurePreUpgradeSnapshot(context.Background(), pre, cfg.BackupDir, "v2", time.Now(), quietLogger()); err != nil {
		t.Fatal(err)
	}
	_ = pre.Close()
	if err := os.Chmod(backupDir, 0o500); err != nil { //nolint:gosec // a read-only test directory
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backupDir, 0o700) }) //nolint:gosec // lets t.TempDir clean up

	logs, err := openDatabaseForTest(t)
	if err != nil {
		t.Fatalf("openDatabase() with an unwritable marker = %v, want a successful boot", err)
	}
	if !strings.Contains(logs.String(), "could not record the booted version") {
		t.Errorf("log = %q, want the marker failure reported", logs.String())
	}
}
