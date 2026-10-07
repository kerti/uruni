package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// TestBuildDocumentTotalsMatchLedgerBalances is CLAUDE.md rule 1 and 2 and
// ADR-012's "a restore proves its numbers" applied to the export side: the
// totals block must equal a fresh, independent read of the same ledger
// calls the home screen uses - never a figure this test (or, more to the
// point, BuildDocument itself) sums by hand.
func TestBuildDocumentTotalsMatchLedgerBalances(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	buildFixture(t, sqlDB, t.TempDir())
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	doc, _, err := BuildDocument(ctx, q, l)
	if err != nil {
		t.Fatalf("BuildDocument() = %v, want no error", err)
	}

	if len(doc.Totals.Funds) != 1 {
		t.Fatalf("len(Totals.Funds) = %d, want 1", len(doc.Totals.Funds))
	}
	fundID := doc.Totals.Funds[0].FundID

	wantFund, err := l.FundBalance(ctx, fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if doc.Totals.Funds[0].Balance != wantFund.Int64() {
		t.Errorf("Totals.Funds[0].Balance = %d, want %d", doc.Totals.Funds[0].Balance, wantFund.Int64())
	}

	if len(doc.Totals.Accounts) != len(doc.Accounts) {
		t.Fatalf("len(Totals.Accounts) = %d, want %d (one per account)", len(doc.Totals.Accounts), len(doc.Accounts))
	}
	for _, at := range doc.Totals.Accounts {
		want, err := l.AccountBalance(ctx, fundID, at.AccountID)
		if err != nil {
			t.Fatalf("AccountBalance(%d) = %v, want no error", at.AccountID, err)
		}
		if at.Balance != want.Int64() {
			t.Errorf("account %d balance = %d, want %d", at.AccountID, at.Balance, want.Int64())
		}
	}

	if len(doc.Totals.Purposes) != len(doc.Purposes) {
		t.Fatalf("len(Totals.Purposes) = %d, want %d (one per purpose)", len(doc.Totals.Purposes), len(doc.Purposes))
	}
	for _, pt := range doc.Totals.Purposes {
		want, err := l.PurposeBalance(ctx, fundID, pt.PurposeID)
		if err != nil {
			t.Fatalf("PurposeBalance(%d) = %v, want no error", pt.PurposeID, err)
		}
		if pt.Balance != want.Int64() {
			t.Errorf("purpose %d balance = %d, want %d", pt.PurposeID, pt.Balance, want.Int64())
		}
	}
}

// TestBuildDocumentExcludesSession is ADR-012's "the backup holds every
// table except session" - a live session's own token must never reach the
// export, since a restored session would let a stale cookie for a login
// that no longer exists (or, restored the other way, a leaked token) keep
// working after a restore. Document has no Sessions field at all - this
// test is the belt to that suspenders: seed a real session row and confirm
// its token appears nowhere in the marshaled document.
func TestBuildDocumentExcludesSession(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	buildFixture(t, sqlDB, t.TempDir())
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	const sentinelToken = "session-token-must-never-be-exported"
	if _, err := q.CreateSession(ctx, store.CreateSessionParams{
		Token: sentinelToken, Data: []byte("{}"), ExpiresAt: 4102444800,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	doc, _, err := BuildDocument(ctx, q, l)
	if err != nil {
		t.Fatalf("BuildDocument() = %v, want no error", err)
	}

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("json.Marshal(doc) = %v, want no error", err)
	}
	if strings.Contains(string(out), sentinelToken) {
		t.Error("exported document contains the session token; session must never be exported (ADR-012)")
	}
	if strings.Contains(strings.ToLower(string(out)), "session") {
		t.Error("exported document mentions \"session\" anywhere; ADR-012 excludes the table entirely")
	}
}

// TestExportIncludesReceiptsUnderStoredName is ADR-012's "receipts/ folder
// holding every receipt image under its stored, server-generated name" -
// the zip's entry name must be exactly the receipt row's own path, and its
// bytes must be exactly what sits on the uploads volume, unmodified.
func TestExportIncludesReceiptsUnderStoredName(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	zipBytes, _, err := Export(ctx, q, l, uploadsDir)
	if err != nil {
		t.Fatalf("Export() = %v, want no error", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader() = %v, want no error", err)
	}

	if _, err := zr.Open(jsonFilename); err != nil {
		t.Errorf("zip has no %s: %v", jsonFilename, err)
	}

	wantEntry := receiptsDir + receiptFilename
	f, err := zr.Open(wantEntry)
	if err != nil {
		t.Fatalf("zip has no %s: %v", wantEntry, err)
	}
	defer func() { _ = f.Close() }()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("reading %s from zip: %v", wantEntry, err)
	}
	if !bytes.Equal(got, receiptFileBytes) {
		t.Errorf("zip entry %s = %q, want %q", wantEntry, got, receiptFileBytes)
	}
}

// TestExportSkipsAMissingReceiptImageInsteadOfFailing: a receipt whose image
// file is gone from the uploads volume must never block a backup
// (maintainer, 2026-09-30). The zip still builds, uruni.json still carries
// the receipt row, the image is simply absent, and its stored name comes
// back in missing for the route to log.
func TestExportSkipsAMissingReceiptImageInsteadOfFailing(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	if err := os.Remove(filepath.Join(uploadsDir, receiptFilename)); err != nil {
		t.Fatalf("removing the fixture's receipt image: %v", err)
	}
	ctx := context.Background()

	zipBytes, missing, err := Export(ctx, store.New(sqlDB), ledger.New(sqlDB), uploadsDir)
	if err != nil {
		t.Fatalf("Export() = %v, want no error - a missing image must not block the backup", err)
	}
	if len(missing) != 1 || missing[0] != receiptFilename {
		t.Errorf("missing = %v, want [%s]", missing, receiptFilename)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader() = %v, want no error", err)
	}
	if _, err := zr.Open(receiptsDir + receiptFilename); err == nil {
		t.Errorf("zip has %s%s, want it skipped", receiptsDir, receiptFilename)
	}
	f, err := zr.Open(jsonFilename)
	if err != nil {
		t.Fatalf("opening %s: %v", jsonFilename, err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("reading %s: %v", jsonFilename, err)
	}
	if !bytes.Contains(raw, []byte(receiptFilename)) {
		t.Errorf("uruni.json no longer names %s, want its receipt row kept", receiptFilename)
	}
}

func TestZipFilenameIsDated(t *testing.T) {
	t.Parallel()
	got := ZipFilename(time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC))
	if got != "uruni-2026-09-30.zip" {
		t.Errorf("ZipFilename() = %q, want uruni-2026-09-30.zip", got)
	}
}

// TestExportProducesValidJSONMatchingFormatVersion is a cheap smoke test
// ahead of the golden fixture's own, much stricter comparison: the JSON
// entry decodes, and its format_version is exactly FormatVersion.
func TestExportProducesValidJSONMatchingFormatVersion(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	zipBytes, _, err := Export(ctx, q, l, uploadsDir)
	if err != nil {
		t.Fatalf("Export() = %v, want no error", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader() = %v, want no error", err)
	}
	f, err := zr.Open(jsonFilename)
	if err != nil {
		t.Fatalf("opening %s: %v", jsonFilename, err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("reading %s: %v", jsonFilename, err)
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshaling %s: %v", jsonFilename, err)
	}
	if doc.FormatVersion != FormatVersion {
		t.Errorf("format_version = %d, want %d", doc.FormatVersion, FormatVersion)
	}
}
