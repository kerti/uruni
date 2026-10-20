package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/tz"
)

// Tests for #517: the raw snapshot taken before a new version migrates.

// openFileDB opens a file-backed database in its own temp dir, unmigrated.
func openFileDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "uruni.db")
	sqlDB, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("db.Open(%q) = %v", path, err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB, path
}

// migratedDB is an instance as an older release left it: schema applied.
func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, _ := openFileDB(t)
	if _, err := db.Up(context.Background(), sqlDB, testDiscardLogger()); err != nil {
		t.Fatalf("db.Up() = %v", err)
	}
	return sqlDB
}

// legacyDB holds one table this binary's migration knows nothing about and no
// goose bookkeeping: what a migration would visibly change.
func legacyDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, _ := openFileDB(t)
	if _, err := sqlDB.Exec(`CREATE TABLE legacy (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	return sqlDB
}

func hasTable(t *testing.T, sqlDB *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// openSnapshot opens a snapshot file the way the previous binary would.
func openSnapshot(t *testing.T, dir, name string) *sql.DB {
	t.Helper()
	snap, err := db.Open(context.Background(), filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("opening snapshot %s = %v", name, err)
	}
	t.Cleanup(func() { _ = snap.Close() })
	return snap
}

func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func snapshotNames(t *testing.T, dir string) []string {
	t.Helper()
	snaps, err := ListPreUpgradeSnapshots(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(snaps))
	for i, s := range snaps {
		names[i] = s.Name
	}
	return names
}

func readMarker(t *testing.T, dir string) bootMarker {
	t.Helper()
	m, err := readBootMarker(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func at(minute int) time.Time { return jakartaTime(2026, 10, 14, 10, minute, 0) }

func TestPreUpgradeNameRoundTripsAndCarriesBothVersions(t *testing.T) {
	t.Parallel()
	name := BuildPreUpgradeName(jakartaTime(2026, 10, 14, 10, 15, 0), "v0.9.0-alpha.2", "v0.9.0-alpha.3")
	if want := "uruni-pre-upgrade-20261014-101500_from-v0.9.0-alpha.2_to-v0.9.0-alpha.3.sqlite"; name != want {
		t.Fatalf("BuildPreUpgradeName() = %q, want %q", name, want)
	}
	info, ok := ParsePreUpgradeName(name)
	if !ok || info.From != "v0.9.0-alpha.2" || info.To != "v0.9.0-alpha.3" || !info.When.Equal(jakartaTime(2026, 10, 14, 10, 15, 0)) {
		t.Errorf("ParsePreUpgradeName(%q) = %+v, %v", name, info, ok)
	}
}

func TestPreUpgradeNameKeepsUnsafeVersionsOutOfTheFilename(t *testing.T) {
	t.Parallel()
	name := BuildPreUpgradeName(at(0), "../etc/pass_wd", "1.0.0+build 5\n"+strings.Repeat("x", 100))
	if strings.ContainsAny(name, "/\\ \n") || len(name) > 150 {
		t.Errorf("BuildPreUpgradeName() = %q, want a plain, short filename", name)
	}
	if _, ok := ParsePreUpgradeName(name); !ok {
		t.Errorf("ParsePreUpgradeName(%q) ok = false, want true", name)
	}
	if got := BuildPreUpgradeName(at(0), "", "dev"); !strings.Contains(got, "_from-unknown_to-dev.") {
		t.Errorf("empty from = %q, want it named unknown", got)
	}
}

func TestPreUpgradeAndDumpNamesNeverMatchEachOther(t *testing.T) {
	t.Parallel()
	snap := BuildPreUpgradeName(at(0), "a", "b")
	dump := BuildDumpName(at(0), tz.Jakarta, KindDaily, 1, "3f9a2c8e10b4deadbeef")
	if ValidDumpName(snap) {
		t.Errorf("ValidDumpName(%q) = true, want false", snap)
	}
	if _, ok := ParsePreUpgradeName(dump); ok {
		t.Errorf("ParsePreUpgradeName(%q) ok = true, want false", dump)
	}
	for _, foreign := range []string{
		"uruni-pre-upgrade-20261014-101500_from-a_to-b.zip",
		"uruni-pre-upgrade-20261014-101500_from-a_to-b.sqlite.bak",
		"x/../uruni-pre-upgrade-20261014-101500_from-a_to-b.sqlite",
		bootMarkerName,
		preUpgradeTempPrefix + "123",
	} {
		if _, ok := ParsePreUpgradeName(foreign); ok {
			t.Errorf("ParsePreUpgradeName(%q) ok = true, want false", foreign)
		}
	}
}

func TestWritePreUpgradeSnapshotIsAnOpenableCopyOfTheDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()

	name, err := WritePreUpgradeSnapshot(ctx, sqlDB, dir, "v1", "v2", at(1))
	if err != nil {
		t.Fatalf("WritePreUpgradeSnapshot() = %v", err)
	}
	snap := openSnapshot(t, dir, name)
	if err := db.QuickCheck(ctx, snap); err != nil {
		t.Errorf("QuickCheck(snapshot) = %v, want ok", err)
	}
	diff, err := db.CheckSchema(ctx, snap)
	if err != nil || !diff.Matches() {
		t.Errorf("CheckSchema(snapshot) = %v, %v, want the live schema", diff.Summary(5), err)
	}
	var versions int
	if err := snap.QueryRow(`SELECT COUNT(*) FROM goose_db_version`).Scan(&versions); err != nil || versions == 0 {
		t.Errorf("snapshot goose_db_version rows = %d, %v, want the applied migrations", versions, err)
	}
	if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("snapshot mode = %v, %v, want 0600 (it holds the password hash)", info.Mode(), err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			t.Errorf("temp file %s left behind", e.Name())
		}
	}
}

func TestEnsurePreUpgradeSnapshotWritesOnAVersionChangeAndLogsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	logger, logs := bufLogger()

	if err := writeBootMarker(dir, bootMarker{Booted: "v0.9.0-alpha.2"}); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v0.9.0-alpha.3", at(1), logger); err != nil {
		t.Fatalf("EnsurePreUpgradeSnapshot() = %v", err)
	}

	snaps, _ := ListPreUpgradeSnapshots(dir)
	if len(snaps) != 1 || snaps[0].From != "v0.9.0-alpha.2" || snaps[0].To != "v0.9.0-alpha.3" {
		t.Fatalf("snapshots = %+v, want one from alpha.2 to alpha.3", snaps)
	}
	for _, want := range []string{"pre-upgrade snapshot", snaps[0].Name, "from=v0.9.0-alpha.2", "to=v0.9.0-alpha.3"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log = %q, want it to contain %q", logs.String(), want)
		}
	}
	if got := strings.Count(logs.String(), "\n"); got != 1 {
		t.Errorf("log has %d lines, want one", got)
	}
	// The boot is not over: the marker still says what last booted.
	if m := readMarker(t, dir); m.Booted != "v0.9.0-alpha.2" || m.Snapshotted != "v0.9.0-alpha.3" {
		t.Errorf("marker after the snapshot = %+v", m)
	}
}

func TestEnsurePreUpgradeSnapshotWritesNothingOnAPlainRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	logger, logs := bufLogger()

	if err := writeBootMarker(dir, bootMarker{Booted: "v2"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(dir)
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(1), logger); err != nil {
		t.Fatalf("EnsurePreUpgradeSnapshot() = %v", err)
	}
	if err := MarkBooted(dir, "v2"); err != nil {
		t.Fatalf("MarkBooted() = %v", err)
	}
	after, _ := os.ReadDir(dir)
	if len(before) != len(after) || len(snapshotNames(t, dir)) != 0 {
		t.Errorf("dir changed on a plain restart: before %d entries, after %d", len(before), len(after))
	}
	if logs.Len() != 0 {
		t.Errorf("log on a plain restart = %q, want none", logs.String())
	}
}

func TestEnsurePreUpgradeSnapshotWithoutAMarker(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("a database that already has a schema is snapshotted from unknown", func(t *testing.T) {
		t.Parallel()
		sqlDB := migratedDB(t)
		dir := t.TempDir()
		if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v1", at(1), testDiscardLogger()); err != nil {
			t.Fatalf("EnsurePreUpgradeSnapshot() = %v", err)
		}
		snaps, _ := ListPreUpgradeSnapshots(dir)
		if len(snaps) != 1 || snaps[0].From != "unknown" || snaps[0].To != "v1" {
			t.Errorf("snapshots = %+v, want one from unknown to v1", snaps)
		}
	})

	t.Run("an empty database needs none, and the marker is written", func(t *testing.T) {
		t.Parallel()
		sqlDB, _ := openFileDB(t)
		dir := t.TempDir()
		logger, logs := bufLogger()
		if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v1", at(1), logger); err != nil {
			t.Fatalf("EnsurePreUpgradeSnapshot() = %v", err)
		}
		if names := snapshotNames(t, dir); len(names) != 0 || logs.Len() != 0 {
			t.Errorf("snapshots = %v, log = %q, want none of either", names, logs.String())
		}
		if m := readMarker(t, dir); m.Snapshotted != "v1" {
			t.Errorf("marker = %+v, want it to say v1 is handled", m)
		}
		if err := MarkBooted(dir, "v1"); err != nil {
			t.Fatal(err)
		}
		if m := readMarker(t, dir); m != (bootMarker{Booted: "v1"}) {
			t.Errorf("marker after boot = %+v, want booted v1 only", m)
		}
	})

	t.Run("a first boot that fails after migrating does not snapshot its own empty schema on retry", func(t *testing.T) {
		t.Parallel()
		sqlDB, _ := openFileDB(t)
		dir := t.TempDir()
		if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v1", at(1), testDiscardLogger()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Up(ctx, sqlDB, testDiscardLogger()); err != nil {
			t.Fatal(err)
		}
		// serve refused (say, the schema check) and Docker restarts it.
		if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v1", at(2), testDiscardLogger()); err != nil {
			t.Fatal(err)
		}
		if names := snapshotNames(t, dir); len(names) != 0 {
			t.Errorf("snapshots = %v, want none", names)
		}
	})
}

// The snapshot must show the state the old binary left, so it has to exist
// before the caller migrates. serve's own ordering is asserted in cmd/uruni.
func TestEnsurePreUpgradeSnapshotShowsThePreMigrationState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	live := legacyDB(t)
	dir := t.TempDir()

	if err := EnsurePreUpgradeSnapshot(ctx, live, dir, "v2", at(1), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Up(ctx, live, testDiscardLogger()); err != nil {
		t.Fatal(err)
	}

	snap := openSnapshot(t, dir, snapshotNames(t, dir)[0])
	if !hasTable(t, snap, "legacy") || hasTable(t, snap, "fund") {
		t.Error("snapshot should hold the legacy table and none of the migration's")
	}
	if !hasTable(t, live, "fund") {
		t.Error("live database should have been migrated afterwards")
	}
}

func TestARetriedFailedBootDoesNotWriteAnotherSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	if err := writeBootMarker(dir, bootMarker{Booted: "v1"}); err != nil {
		t.Fatal(err)
	}

	// Boot v2 fails after the snapshot (MarkBooted never runs); the restart
	// policy retries four times.
	for i := range 5 {
		if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(i), testDiscardLogger()); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	snaps, _ := ListPreUpgradeSnapshots(dir)
	if len(snaps) != 1 || snaps[0].From != "v1" || snaps[0].To != "v2" {
		t.Fatalf("snapshots after 5 attempts = %+v, want exactly the first", snaps)
	}
	if m := readMarker(t, dir); m.Booted != "v1" {
		t.Errorf("marker = %+v, want booted to stay v1 until a boot succeeds", m)
	}

	// A fix release tried after the failed one finds the copy already taken
	// and nothing booted since: no second copy, the marker follows.
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2-fix", at(10), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}
	if got := len(snapshotNames(t, dir)); got != 1 {
		t.Errorf("snapshots after trying v2-fix = %d, want still 1", got)
	}
}

// v1 booted, v2 fails, v2-fix fails, v2-fix2 fails: the one copy is the real
// v1 state, still labelled from v1, and none of the later attempts evict it.
func TestSeveralFailingReleasesInARowKeepTheOneRealCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	if err := MarkBooted(dir, "v1"); err != nil {
		t.Fatal(err)
	}

	for i, v := range []string{"v2", "v2", "v2-fix", "v2-fix", "v2-fix2", "v2-fix2"} {
		if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, v, at(i), testDiscardLogger()); err != nil {
			t.Fatalf("attempt %d (%s): %v", i, v, err)
		}
	}
	snaps, _ := ListPreUpgradeSnapshots(dir)
	if len(snaps) != 1 || snaps[0].From != "v1" || snaps[0].To != "v2" {
		t.Fatalf("snapshots = %+v, want exactly one, from v1 to v2", snaps)
	}
	if m := readMarker(t, dir); m != (bootMarker{Booted: "v1", Snapshotted: "v2-fix2"}) {
		t.Errorf("marker = %+v, want booted v1, snapshotted v2-fix2", m)
	}

	// v2-fix2 finally boots: the next upgrade is a new transition.
	if err := MarkBooted(dir, "v2-fix2"); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v3", at(10), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}
	if snaps, _ := ListPreUpgradeSnapshots(dir); len(snaps) != 2 || snaps[0].From != "v2-fix2" || snaps[0].To != "v3" {
		t.Errorf("snapshots after the next upgrade = %+v, want a second one from v2-fix2 to v3", snaps)
	}
}

func TestGoingBackAfterAFailedUpgradeSnapshotsAgainOnTheNextAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	if err := writeBootMarker(dir, bootMarker{Booted: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(1), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}

	// The operator gives up and runs v1 again, which boots fine and records entries.
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v1", at(2), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}
	if err := MarkBooted(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	if m := readMarker(t, dir); m != (bootMarker{Booted: "v1"}) {
		t.Fatalf("marker after the rollback = %+v, want the stale snapshotted cleared", m)
	}

	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(3), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}
	if got := len(snapshotNames(t, dir)); got != 2 {
		t.Errorf("snapshots = %d, want a fresh one for the second attempt at v2", got)
	}
}

func TestEnsurePreUpgradeSnapshotKeepsTheNewestRetained(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()

	for i := range 6 {
		v := fmt.Sprintf("v%d", i)
		if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, v, at(i), testDiscardLogger()); err != nil {
			t.Fatal(err)
		}
		if err := MarkBooted(dir, v); err != nil {
			t.Fatal(err)
		}
	}
	snaps, _ := ListPreUpgradeSnapshots(dir)
	if len(snaps) != PreUpgradeRetention {
		t.Fatalf("snapshots = %d, want %d", len(snaps), PreUpgradeRetention)
	}
	if snaps[0].To != "v5" || snaps[2].To != "v3" {
		t.Errorf("kept %v..%v, want v5 down to v3", snaps[0].To, snaps[2].To)
	}
}

func TestEnsurePreUpgradeSnapshotSweepsAnInterruptedCopy(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	stale := filepath.Join(dir, preUpgradeTempPrefix+"123456")
	if err := os.WriteFile(stale, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePreUpgradeSnapshot(context.Background(), sqlDB, dir, "v2", at(1), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale temp file still there: %v", err)
	}
	if got := len(snapshotNames(t, dir)); got != 1 {
		t.Errorf("snapshots = %d, want 1", got)
	}
}

func TestEnsurePreUpgradeSnapshotFailsClosedAndTriesAgainNextBoot(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	if err := writeBootMarker(dir, bootMarker{Booted: "v1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // a read-only test directory
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // lets t.TempDir clean up

	err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(1), testDiscardLogger())
	if !errors.Is(err, ErrPreUpgradeSnapshot) {
		t.Fatalf("EnsurePreUpgradeSnapshot() into a read-only dir = %v, want ErrPreUpgradeSnapshot", err)
	}
	if m := readMarker(t, dir); m.Snapshotted != "" {
		t.Errorf("marker = %+v, want a failed snapshot not remembered as taken", m)
	}

	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // the directory is ours again
		t.Fatal(err)
	}
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(2), testDiscardLogger()); err != nil {
		t.Fatalf("retry = %v, want the snapshot written now", err)
	}
	if got := len(snapshotNames(t, dir)); got != 1 {
		t.Errorf("snapshots = %d, want 1", got)
	}
}

func TestEnsurePreUpgradeSnapshotFailsWhenTheBackupDirIsGone(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	dir := filepath.Join(t.TempDir(), "missing")
	err := EnsurePreUpgradeSnapshot(context.Background(), sqlDB, dir, "v2", at(1), testDiscardLogger())
	if !errors.Is(err, ErrPreUpgradeSnapshot) {
		t.Errorf("EnsurePreUpgradeSnapshot() = %v, want ErrPreUpgradeSnapshot", err)
	}
}

// fillDir lays out everything a backup directory can hold.
func fillDir(t *testing.T) (dir string, snapshots, others []string) {
	t.Helper()
	dir = t.TempDir()
	for i := range 5 {
		snapshots = append(snapshots, BuildPreUpgradeName(at(i), "a", fmt.Sprintf("b%d", i)))
	}
	others = []string{
		BuildDumpName(at(0), tz.Jakarta, KindDaily, 1, "3f9a2c8e10b4aaaa"),
		BuildDumpName(at(1), tz.Jakarta, KindDaily, 1, "3f9a2c8e10b4bbbb"),
		BuildDumpName(at(2), tz.Jakarta, KindPreRestore, 1, "3f9a2c8e10b4cccc"),
		BuildDumpName(at(3), tz.Jakarta, KindPreRestore, 1, "3f9a2c8e10b4dddd"),
		bootMarkerName,
		tempPrefix + "restore-1",
		preUpgradeTempPrefix + "9",
		"README.txt",
		"uruni-pre-upgrade-20261014-101500_from-a_to-b.zip",
	}
	for _, n := range append(append([]string{}, snapshots...), others...) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, snapshots, others
}

func TestApplyPreUpgradeRetentionKeepsTheNewestAndTouchesNothingElse(t *testing.T) {
	t.Parallel()
	dir, snapshots, others := fillDir(t)

	if err := ApplyPreUpgradeRetention(dir, 3); err != nil {
		t.Fatalf("ApplyPreUpgradeRetention() = %v", err)
	}
	exists := func(n string) bool { _, err := os.Stat(filepath.Join(dir, n)); return err == nil }
	for i, n := range snapshots { // oldest first
		if want := i >= 2; exists(n) != want {
			t.Errorf("snapshot %s exists = %v, want %v", n, exists(n), want)
		}
	}
	for _, n := range others {
		if !exists(n) {
			t.Errorf("%s was deleted by pre-upgrade retention", n)
		}
	}
}

func TestDumpRetentionAndListingNeverSeeSnapshots(t *testing.T) {
	t.Parallel()
	dir, snapshots, _ := fillDir(t)

	if err := ApplyRetention(dir, 1, 1); err != nil {
		t.Fatalf("ApplyRetention() = %v", err)
	}
	for _, n := range snapshots {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("snapshot %s lost to dump retention: %v", n, err)
		}
	}
	for _, n := range []string{bootMarkerName, "README.txt", preUpgradeTempPrefix + "9"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s lost to dump retention: %v", n, err)
		}
	}
	dumps, err := ListDumps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dumps) != 2 { // one daily, one pre-restore survive
		t.Errorf("ListDumps() = %d dumps, want 2", len(dumps))
	}
}

// Retention runs only after the marker is down, so a marker that cannot be
// written (or a crash right after the copy) never costs an older copy.
func TestAMarkerFailureAfterTheSnapshotNeverEvictsAnOlderCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	for i := range PreUpgradeRetention {
		name := BuildPreUpgradeName(at(i), "a", fmt.Sprintf("b%d", i))
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	failing := func(string, bootMarker) error { return errors.New("disk full") }

	err := ensurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(20), testDiscardLogger(), failing)
	if err == nil {
		t.Fatal("ensurePreUpgradeSnapshot() with a failing marker write = nil, want the error")
	}
	if got := len(snapshotNames(t, dir)); got != PreUpgradeRetention+1 {
		t.Errorf("snapshots = %d, want the new one added and none pruned (%d)", got, PreUpgradeRetention+1)
	}

	// Next boot: nothing was recorded, so it tries again and now prunes.
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(21), testDiscardLogger()); err != nil {
		t.Fatalf("retry = %v", err)
	}
	snaps, _ := ListPreUpgradeSnapshots(dir)
	if len(snaps) != PreUpgradeRetention || snaps[0].To != "v2" {
		t.Errorf("snapshots after the retry = %+v, want the newest %d", snaps, PreUpgradeRetention)
	}
}

// A marker that cannot be updated after a good boot must not be left saying
// "a snapshot was taken, nothing booted since": entries are made after that
// boot, and the next upgrade would skip its copy. Gone, it snapshots.
func TestMarkBootedFailureRemovesAMarkerThatWouldMisleadTheNextUpgrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB := migratedDB(t)
	dir := t.TempDir()
	if err := MarkBooted(dir, "v1"); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v2", at(1), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}

	failing := func(string, bootMarker) error { return errors.New("disk full") }
	if err := markBooted(dir, "v2", failing); err == nil {
		t.Fatal("markBooted() with a failing write = nil, want the error")
	}
	if _, err := os.Stat(filepath.Join(dir, bootMarkerName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("marker after a failed MarkBooted: %v, want it removed", err)
	}

	if err := EnsurePreUpgradeSnapshot(ctx, sqlDB, dir, "v3", at(2), testDiscardLogger()); err != nil {
		t.Fatal(err)
	}
	snaps, _ := ListPreUpgradeSnapshots(dir)
	if len(snaps) != 2 || snaps[0].From != "unknown" || snaps[0].To != "v3" {
		t.Errorf("snapshots = %+v, want a second one from unknown to v3", snaps)
	}
}
