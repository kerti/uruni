package backup

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/kerti/uruni/internal/db"
)

// TestDailyDumpOfADamagedDatabaseIsRefused (#505): a dump that would be
// written runs the integrity check first, and a failing check writes nothing,
// so a damaged file never takes a retention slot from a good backup.
func TestDailyDumpOfADamagedDatabaseIsRefused(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	assemble, built := countingAssembler()
	damaged := func(context.Context) error {
		return fmt.Errorf("%w: page 7 is never used", db.ErrIntegrity)
	}

	_, written, err := writeDailyIfNeeded(context.Background(), readDocumentFrom(sqlDB), assemble, damaged, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 3, 0, 0))
	if !errors.Is(err, db.ErrIntegrity) || written {
		t.Fatalf("writeDailyIfNeeded with a failing check = (written=%v, err=%v), want (false, ErrIntegrity)", written, err)
	}
	if got := built.Load(); got != 0 {
		t.Errorf("zips built = %d, want 0", got)
	}
	dumps, err := ListDumps(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dumps) != 0 {
		t.Errorf("dumps in the backup dir = %v, want none", dumps)
	}
}

// TestDailyDumpSkipsTheIntegrityCheckWhenNothingChanged: the check is paid
// for only by a dump that will be written, so the hourly no-op tick stays a
// hash comparison.
func TestDailyDumpSkipsTheIntegrityCheckWhenNothingChanged(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	read := readDocumentFrom(sqlDB)
	checks := 0
	check := func(ctx context.Context) error {
		checks++
		return db.QuickCheck(ctx, sqlDB)
	}

	if _, written, err := writeDailyIfNeeded(ctx, read, assembleZip, check, uploadsDir, backupDir, jakartaTime(2026, 9, 29, 3, 0, 0)); err != nil || !written {
		t.Fatalf("first dump = (written=%v, err=%v), want (true, nil)", written, err)
	}
	for hour := 0; hour < 24; hour++ {
		if _, written, err := writeDailyIfNeeded(ctx, read, assembleZip, check, uploadsDir, backupDir, jakartaTime(2026, 9, 30, hour, 0, 0)); err != nil || written {
			t.Fatalf("writeDailyIfNeeded(hour %d) = (written=%v, err=%v), want (false, nil)", hour, written, err)
		}
	}
	if checks != 1 {
		t.Errorf("integrity checks run = %d, want 1 (only the dump that was written)", checks)
	}
}
