package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// countingAssembler wraps assembleZip so a test can assert how many zips a call
// built: the seam #501 added, not timing.
func countingAssembler() (zipAssembler, *atomic.Int64) {
	var n atomic.Int64
	return func(w io.Writer, docBytes []byte, receipts []store.Receipt, uploadsDir string) ([]string, error) {
		n.Add(1)
		return assembleZip(w, docBytes, receipts, uploadsDir)
	}, &n
}

// TestWriteDailyIfNeededUnchangedFundBuildsNoZip: the hourly tick on a day
// without a dump but without a change decides from the hash alone.
func TestWriteDailyIfNeededUnchangedFundBuildsNoZip(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	read := readDocumentFrom(sqlDB)
	assemble, built := countingAssembler()

	if _, written, err := writeDailyIfNeeded(ctx, read, assemble, nil, uploadsDir, backupDir, tz.Jakarta, jakartaTime(2026, 9, 29, 3, 0, 0)); err != nil || !written {
		t.Fatalf("writeDailyIfNeeded(yesterday) = (written=%v, err=%v), want (true, nil)", written, err)
	}
	if got := built.Load(); got != 1 {
		t.Fatalf("zips built after the first dump = %d, want 1", got)
	}

	// Every hourly tick of the next day: no dump of its own yet, fund unchanged.
	for hour := 0; hour < 24; hour++ {
		_, written, err := writeDailyIfNeeded(ctx, read, assemble, nil, uploadsDir, backupDir, tz.Jakarta, jakartaTime(2026, 9, 30, hour, 0, 0))
		if err != nil || written {
			t.Fatalf("writeDailyIfNeeded(hour %d) = (written=%v, err=%v), want (false, nil)", hour, written, err)
		}
	}
	if got := built.Load(); got != 1 {
		t.Errorf("zips built after 24 unchanged ticks = %d, want still 1 (an unchanged fund builds nothing)", got)
	}
}

// TestWriteDailyIfNeededBuildsExactlyOneZipWhenTheFundChanged: the early
// return must not swallow a real change.
func TestWriteDailyIfNeededBuildsExactlyOneZipWhenTheFundChanged(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	read := readDocumentFrom(sqlDB)
	assemble, built := countingAssembler()

	if _, written, err := writeDailyIfNeeded(ctx, read, assemble, nil, uploadsDir, backupDir, tz.Jakarta, jakartaTime(2026, 9, 29, 3, 0, 0)); err != nil || !written {
		t.Fatalf("first dump = (written=%v, err=%v), want (true, nil)", written, err)
	}
	// A receipt photo vanishing from disk (the row stays) is a real change.
	if err := os.Remove(filepath.Join(uploadsDir, receiptFilename)); err != nil {
		t.Fatalf("removing the receipt file: %v", err)
	}
	name, written, err := writeDailyIfNeeded(ctx, read, assemble, nil, uploadsDir, backupDir, tz.Jakarta, jakartaTime(2026, 9, 30, 3, 0, 0))
	if err != nil || !written {
		t.Fatalf("second dump = (%q, written=%v, err=%v), want (_, true, nil)", name, written, err)
	}
	if got := built.Load(); got != 2 {
		t.Errorf("zips built = %d, want 2 (one per written dump)", got)
	}
}

// TestRefusedDocumentBuildsNoZip: CheckTotals still runs before any zip.
func TestRefusedDocumentBuildsNoZip(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	assemble, built := countingAssembler()

	_, _, err := writeDailyIfNeeded(context.Background(), tamperedReader(t, sqlDB), assemble, nil, uploadsDir, backupDir, tz.Jakarta, jakartaTime(2026, 9, 30, 3, 0, 0))
	if err == nil {
		t.Fatal("writeDailyIfNeeded() = nil, want ErrTotalsMismatch")
	}
	if got := built.Load(); got != 0 {
		t.Errorf("zips built for a refused document = %d, want 0", got)
	}
	assertDirEmpty(t, backupDir)
}

func planHash(t *testing.T, read documentReader, uploadsDir string) string {
	t.Helper()
	p, err := planDump(context.Background(), read, uploadsDir)
	if err != nil {
		t.Fatalf("planDump() = %v, want no error", err)
	}
	return p.hash
}

// TestChangeHashFollowsTheReceiptList: adding a receipt, removing it, and a
// photo going missing from disk each move the hash; restoring them restores it.
func TestChangeHashFollowsTheReceiptList(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	ctx := context.Background()
	q := store.New(sqlDB)
	read := readDocumentFrom(sqlDB)

	base := planHash(t, read, uploadsDir)

	funds, err := q.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("ListFunds() = (%v, %v), want exactly 1 fund", funds, err)
	}
	claims, err := q.ListReimbursementsByFund(ctx, funds[0].ID)
	if err != nil || len(claims) == 0 {
		t.Fatalf("ListReimbursementsByFund() = (%v, %v), want at least 1", claims, err)
	}

	// Added: a second receipt row and its file.
	const extra = "0000000000000000000000000000aa.jpg"
	if err := os.WriteFile(filepath.Join(uploadsDir, extra), []byte("more"), 0o600); err != nil {
		t.Fatalf("writing the extra receipt file: %v", err)
	}
	added, err := q.CreateReceipt(ctx, store.CreateReceiptParams{FundID: funds[0].ID, ReimbursementID: &claims[0].ID, Path: extra, UploadedAt: 1700000900})
	if err != nil {
		t.Fatalf("CreateReceipt: %v", err)
	}
	withExtra := planHash(t, read, uploadsDir)
	if withExtra == base {
		t.Error("hash did not change when a receipt was added")
	}

	// Removed: the row goes again.
	if err := q.DeleteReceipt(ctx, added.ID); err != nil {
		t.Fatalf("DeleteReceipt: %v", err)
	}
	if got := planHash(t, read, uploadsDir); got != base {
		t.Error("hash after removing the added receipt differs from the original, want identical")
	}

	// A photo missing from disk while its row stays: same document, so only
	// the missing list can move the hash.
	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{FundID: funds[0].ID, ReimbursementID: &claims[0].ID, Path: extra, UploadedAt: 1700000901}); err != nil {
		t.Fatalf("CreateReceipt (again): %v", err)
	}
	present := planHash(t, read, uploadsDir)
	if err := os.Remove(filepath.Join(uploadsDir, extra)); err != nil {
		t.Fatalf("removing the extra file: %v", err)
	}
	if got := planHash(t, read, uploadsDir); got == present {
		t.Error("hash did not change when a receipt file went missing from disk")
	}
}

// TestChangeHashIgnoresWhatVariesPerTick: the document has no wall-clock field
// and receipt mtimes are not read, so repeated plans of an untouched fund,
// however much later, hash identically. If this fails, the scheduler's dedupe
// has silently stopped working.
func TestChangeHashIgnoresWhatVariesPerTick(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	read := readDocumentFrom(sqlDB)

	first := planHash(t, read, uploadsDir)

	later := time.Now().Add(48 * time.Hour)
	if err := os.Chtimes(filepath.Join(uploadsDir, receiptFilename), later, later); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	if got := planHash(t, read, uploadsDir); got != first {
		t.Errorf("hash changed with nothing but a receipt file's mtime: %s vs %s", first, got)
	}
	if got := planHash(t, read, uploadsDir); got != first {
		t.Errorf("hash changed between two plans of an untouched fund: %s vs %s", first, got)
	}
}

// TestPlanHashMatchesTheHashOfTheZipPath: the hash a plan decides on is the
// hash the written dump is named with.
func TestPlanHashMatchesTheHashOfTheZipPath(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	read := readDocumentFrom(sqlDB)

	planned := planHash(t, read, uploadsDir)
	m, err := planDump(context.Background(), read, uploadsDir)
	if err != nil {
		t.Fatalf("planDump() = %v", err)
	}
	if m.hash != planned {
		t.Errorf("second plan's hash %s != first plan's %s", m.hash, planned)
	}
	zipBytes, docBytes, missing, err := BuildZip(m.doc, m.receipts, uploadsDir)
	if err != nil {
		t.Fatalf("BuildZip() = %v", err)
	}
	if got := ChangeHash(docBytes, missing); got != planned {
		t.Errorf("ChangeHash over BuildZip's output = %s, want the plan's %s (hash definition drifted)", got, planned)
	}
	if len(zipBytes) == 0 {
		t.Error("BuildZip returned an empty zip")
	}
}
