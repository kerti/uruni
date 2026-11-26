package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// #903: a dump write owns its prune, and a prune that fails is a logged
// warning, never the write's failure. These tests make one deletion fail by
// swapping removeOldDump, so none of them is parallel: parallel tests pause
// until every serial one has finished, so the swap never overlaps another
// test's retention pass.

// stubDumpName is a valid dump name for an old file seeded into a backup
// directory: day is a September 2026 day (older than every "now" these tests
// use), n is the hash's only distinguishing digit.
func stubDumpName(kind Kind, day int, formatVersion int64, n int) string {
	return fmt.Sprintf("uruni-202609%02d-120000-%s-fv%d-%012d.zip", day, kind, formatVersion, n)
}

// seedStubDumps writes count stub dumps of kind on September days 1..count and
// returns their names, oldest first.
func seedStubDumps(t *testing.T, dir string, kind Kind, count int, formatVersion int64) []string {
	t.Helper()
	names := make([]string, 0, count)
	for day := 1; day <= count; day++ {
		name := stubDumpName(kind, day, formatVersion, day)
		if _, ok := ParseDumpName(name); !ok {
			t.Fatalf("stub name %q is not a valid dump name", name)
		}
		writeDumpFile(t, dir, name)
		names = append(names, name)
	}
	return names
}

// failRemovingOnly makes ApplyRetention's deletion of the file called name fail
// with a permission error for the rest of the test; every other deletion is the
// real one.
func failRemovingOnly(t *testing.T, name string) {
	t.Helper()
	prev := removeOldDump
	removeOldDump = func(path string) error {
		if filepath.Base(path) == name {
			return &os.PathError{Op: "remove", Path: path, Err: os.ErrPermission}
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { removeOldDump = prev })
}

// capturedLogger is a logger whose records land in the returned buffer.
func capturedLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func assertPruneWarning(t *testing.T, logs, backupDir string) {
	t.Helper()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "backup: pruning old dumps failed") {
		t.Errorf("log = %q, want a Warn record %q", logs, "backup: pruning old dumps failed")
	}
	if !strings.Contains(logs, "permission") {
		t.Errorf("log = %q, want the deletion error in the record", logs)
	}
	if !strings.Contains(logs, backupDir) {
		t.Errorf("log = %q, want backup_dir %q in the record", logs, backupDir)
	}
}

func assertExists(t *testing.T, dir, name string, want bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, name))
	if want && err != nil {
		t.Errorf("%s: stat = %v, want it on disk", name, err)
	}
	if !want && !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s: stat = %v, want it deleted", name, err)
	}
}

func TestWritePreRestoreDumpSucceedsWhenAnOldDumpCannotBePruned(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	old := seedStubDumps(t, backupDir, KindPreRestore, PreRestoreRetention, FormatVersion)
	failRemovingOnly(t, old[0])
	logger, logs := capturedLogger()

	name, err := WritePreRestoreDump(context.Background(), sqlDB, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 12, 0, 0), logger)
	if err != nil {
		t.Fatalf("WritePreRestoreDump() = %v, want no error: the dump was written", err)
	}
	assertExists(t, backupDir, name, true)
	assertExists(t, backupDir, old[0], true) // the undeletable one is still there
	assertExists(t, backupDir, old[1], true)
	assertPruneWarning(t, logs.String(), backupDir)
}

func TestRestoreSucceedsWhenAnOldDumpCannotBePruned(t *testing.T) {
	destDB := newTestDB(t)
	seedOneUser(t, destDB)
	backupDir := t.TempDir()
	old := seedStubDumps(t, backupDir, KindPreRestore, PreRestoreRetention, FormatVersion)
	failRemovingOnly(t, old[0])
	logger, logs := capturedLogger()

	err := Restore(context.Background(), RestoreParams{
		DB: destDB, UploadsDir: t.TempDir(), BackupDir: backupDir,
		Parsed: ParsedUpload{Document: minimalDocWithOneFund(t)}, Now: time.Unix(1800000000, 0), Logger: logger,
	})
	if err != nil {
		t.Fatalf("Restore() = %v, want no error: the safety-net dump exists, only an old one could not be deleted", err)
	}

	funds, err := store.New(destDB).ListFunds(context.Background())
	if err != nil {
		t.Fatalf("ListFunds() = %v", err)
	}
	if len(funds) != 1 || funds[0].Name != "Dana" {
		t.Errorf("ListFunds() = %v, want the restored fund", funds)
	}
	assertExists(t, backupDir, old[0], true)
	assertPruneWarning(t, logs.String(), backupDir)
	dumps, err := ListDumps(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dumps) != PreRestoreRetention+1 {
		t.Errorf("len(dumps) = %d, want %d: the new safety net plus the %d old ones the failed prune left", len(dumps), PreRestoreRetention+1, PreRestoreRetention)
	}
}

func TestWriteDailyIfNeededSucceedsWhenAnOldDumpCannotBePruned(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	old := seedStubDumps(t, backupDir, KindDaily, DailyRetention, FormatVersion)
	failRemovingOnly(t, old[0])
	logger, logs := capturedLogger()

	name, written, err := WriteDailyIfNeeded(context.Background(), sqlDB, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 12, 0, 0), logger)
	if err != nil || !written {
		t.Fatalf("WriteDailyIfNeeded() = (%q, %v, %v), want (name, true, nil)", name, written, err)
	}
	assertExists(t, backupDir, name, true)
	assertExists(t, backupDir, old[0], true)
	assertPruneWarning(t, logs.String(), backupDir)
}

func TestEnsureBootDumpSucceedsWhenAnOldDumpCannotBePruned(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	// A newer format on disk makes NeedsFormatVersionDump true.
	old := seedStubDumps(t, backupDir, KindDaily, DailyRetention, FormatVersion+1)
	failRemovingOnly(t, old[0])
	logger, logs := capturedLogger()

	if err := EnsureBootDump(context.Background(), sqlDB, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 12, 0, 0), logger); err != nil {
		t.Fatalf("EnsureBootDump() = %v, want no error: the boot dump was written", err)
	}
	dumps, err := ListDumps(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dumps) != DailyRetention+1 || dumps[0].FormatVersion != FormatVersion {
		t.Errorf("dumps = %+v, want the new boot dump first and the %d old ones kept", dumps, DailyRetention)
	}
	assertExists(t, backupDir, old[0], true)
	assertPruneWarning(t, logs.String(), backupDir)
	if strings.Contains(logs.String(), "boot backup dump failed") {
		t.Errorf("log = %q, want no failure for a dump that was written", logs.String())
	}
}

// With deletion working, each write path still enforces its quota: the
// scheduler and boot no longer call ApplyRetention themselves, so the writes
// have to.

func TestWriteDailyIfNeededPrunesAfterItsWrite(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	old := seedStubDumps(t, backupDir, KindDaily, DailyRetention, FormatVersion)
	preRestore := seedStubDumps(t, backupDir, KindPreRestore, PreRestoreRetention, FormatVersion)
	logger, logs := capturedLogger()

	name, written, err := WriteDailyIfNeeded(context.Background(), sqlDB, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 12, 0, 0), logger)
	if err != nil || !written {
		t.Fatalf("WriteDailyIfNeeded() = (%q, %v, %v), want (name, true, nil)", name, written, err)
	}
	assertExists(t, backupDir, name, true)
	assertExists(t, backupDir, old[0], false) // the eighth daily is over quota
	assertExists(t, backupDir, old[1], true)
	for _, n := range preRestore {
		assertExists(t, backupDir, n, true) // the other kind's quota is its own
	}
	if logs.Len() != 0 {
		t.Errorf("log = %q, want silence when the prune works", logs.String())
	}
}

func TestEnsureBootDumpPrunesAfterItsWrite(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	old := seedStubDumps(t, backupDir, KindDaily, DailyRetention, FormatVersion+1)
	logger, _ := capturedLogger()

	if err := EnsureBootDump(context.Background(), sqlDB, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 12, 0, 0), logger); err != nil {
		t.Fatalf("EnsureBootDump() = %v, want no error", err)
	}
	assertExists(t, backupDir, old[0], false)
	dumps, err := ListDumps(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dumps) != DailyRetention {
		t.Errorf("len(dumps) = %d, want %d", len(dumps), DailyRetention)
	}
}

// A day that writes nothing prunes nothing: the pass belongs to the write.
func TestWriteDailyIfNeededThatWritesNothingDoesNotPrune(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	logger, _ := capturedLogger()
	now := jakartaTime(2026, 9, 30, 12, 0, 0)
	if _, written, err := WriteDailyIfNeeded(context.Background(), sqlDB, uploadsDir, backupDir, now, logger); err != nil || !written {
		t.Fatalf("first WriteDailyIfNeeded() = (%v, %v), want (true, nil)", written, err)
	}
	// Over quota by one, but today already has its dump, so nothing is written.
	seedStubDumps(t, backupDir, KindPreRestore, PreRestoreRetention+1, FormatVersion)
	if _, written, err := WriteDailyIfNeeded(context.Background(), sqlDB, uploadsDir, backupDir, now.Add(time.Hour), logger); err != nil || written {
		t.Fatalf("second WriteDailyIfNeeded() = (%v, %v), want (false, nil)", written, err)
	}
	assertExists(t, backupDir, stubDumpName(KindPreRestore, 1, FormatVersion, 1), true)
}

// The raw write does not prune: it is WriteDump's contract.
func TestWriteDumpDoesNotPrune(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	old := seedStubDumps(t, backupDir, KindDaily, DailyRetention, FormatVersion)
	if _, err := WriteDump(context.Background(), sqlDB, uploadsDir, backupDir, KindDaily, jakartaTime(2026, 9, 30, 12, 0, 0)); err != nil {
		t.Fatalf("WriteDump() = %v", err)
	}
	assertExists(t, backupDir, old[0], true)
}
