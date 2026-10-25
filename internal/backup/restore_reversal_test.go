package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// ADR-042 adds no column, so the backup format stays; these tests hold what
// restore must do about it. The triggers stay ON during a restore (ADR-012
// obligation 1): they refuse a hand-edited or corrupted file, and must not
// refuse a reversal already in a live file that the ledger would not post today
// (one dated before its original).

type reversalFixture struct {
	fundID, cashID, mainID, passThroughID int64
}

// addReversalShapes adds, to buildFixture's ledger, the two shapes the golden
// leaves out: a purpose-corrected row reversed (the reversal sits in another pos
// than the row), and a reversal dated before its original.
func addReversalShapes(t *testing.T, sqlDB *sql.DB) reversalFixture {
	t.Helper()
	ctx := context.Background()
	q := store.New(sqlDB)
	funds, err := q.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("ListFunds() = (%v, %v), want exactly one fixture fund", funds, err)
	}
	f := reversalFixture{fundID: funds[0].ID}
	accounts, err := q.ListAccountsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListAccountsByFund() = %v", err)
	}
	for _, a := range accounts {
		if a.Kind == "cash" {
			f.cashID = a.ID
		}
	}
	purposes, err := q.ListPurposesByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListPurposesByFund() = %v", err)
	}
	for _, p := range purposes {
		switch p.Kind {
		case "main":
			f.mainID = p.ID
		case "pass_through":
			f.passThroughID = p.ID
		}
	}

	post := func(p store.CreateTransactionParams) store.Transaction {
		t.Helper()
		p.FundID, p.AccountID, p.CreatedAt = f.fundID, f.cashID, 1700000500+p.Amount
		row, err := q.CreateTransaction(ctx, p)
		if err != nil {
			t.Fatalf("CreateTransaction(%+v) = %v", p, err)
		}
		return row
	}

	// Tagged to Kas Utama, corrected to the pass-through, then reversed: the
	// correction's legs carry the move, the reversal copies the effective pos.
	misTagged := post(store.CreateTransactionParams{PurposeID: f.mainID, Direction: "out", Amount: 6000, OccurredOn: "2026-02-06", Kind: "normal"})
	correction, err := q.CreateTransfer(ctx, store.CreateTransferParams{
		FundID: f.fundID, Kind: "reclass_purpose", CorrectsTransactionID: &misTagged.ID, CreatedAt: 1700000600,
	})
	if err != nil {
		t.Fatalf("CreateTransfer(correction) = %v", err)
	}
	for _, leg := range []struct {
		purposeID int64
		direction string
	}{{f.passThroughID, "out"}, {f.mainID, "in"}} {
		post(store.CreateTransactionParams{
			PurposeID: leg.purposeID, Direction: leg.direction, Amount: 6000, OccurredOn: "2026-02-06",
			Kind: "transfer", TransferID: &correction.ID,
		})
	}
	post(store.CreateTransactionParams{
		PurposeID: f.passThroughID, Direction: "in", Amount: 6000, OccurredOn: "2026-03-01",
		Kind: "adjustment", ReversesTransactionID: &misTagged.ID,
	})

	// A reversal dated BEFORE its original, as a hand-made or legacy file may hold
	// one (the ledger never checked): the trigger must not refuse it on restore.
	early := post(store.CreateTransactionParams{PurposeID: f.mainID, Direction: "out", Amount: 3000, OccurredOn: "2026-04-10", Kind: "normal"})
	post(store.CreateTransactionParams{
		PurposeID: f.mainID, Direction: "in", Amount: 3000, OccurredOn: "2026-04-01",
		Kind: "adjustment", ReversesTransactionID: &early.ID,
	})
	return f
}

// useARealReceiptImage swaps buildFixture's stub receipt for a decodable image on
// disk, which ParseUpload's ADR-011 check needs.
func useARealReceiptImage(t *testing.T, uploadsDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(uploadsDir, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}
}

func TestRestoreRoundTripCarriesReversalsAcrossPosAndOutOfOrder(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	useARealReceiptImage(t, srcUploads)
	addReversalShapes(t, srcDB)

	zipBytes, wantDoc := exportFixture(t, srcDB, srcUploads)
	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error", err)
	}
	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	assertBalancesMatch(t, destDB, wantDoc)
	gotDoc, _, err := BuildDocument(context.Background(), destDB)
	if err != nil {
		t.Fatalf("BuildDocument(dest) = %v, want no error", err)
	}
	assertDocumentsEqual(t, gotDoc, wantDoc)

	var reversals int
	for _, tx := range gotDoc.Transactions {
		if tx.ReversesTransactionID != nil {
			reversals++
		}
	}
	// The dues reversal, the golden's ordinary one, and the two added here.
	if reversals != 4 {
		t.Errorf("restored file holds %d reversals, want 4", reversals)
	}
}

// A same-direction "reversal" in a file is refused by the trigger, the whole
// restore rolls back, and nothing of the file is left behind.
func TestRestoreRefusesAReversalTheTriggerRejects(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	useARealReceiptImage(t, srcUploads)
	doc, receipts, err := BuildDocument(context.Background(), srcDB)
	if err != nil {
		t.Fatalf("BuildDocument() = %v, want no error", err)
	}

	// The golden's ordinary reversal is the last row. Make it go the same way as
	// its original, and re-state the totals so the file is otherwise consistent.
	last := &doc.Transactions[len(doc.Transactions)-1]
	if last.ReversesTransactionID == nil || last.Kind != "adjustment" {
		t.Fatalf("the fixture's last row is %+v, want the ordinary reversal", *last)
	}
	last.Direction = "in"
	totals, err := totalsFromRows(doc)
	if err != nil {
		t.Fatalf("totalsFromRows() = %v", err)
	}
	doc.Totals = totals

	zipBytes, _, _, err := BuildZip(doc, receipts, srcUploads)
	if err != nil {
		t.Fatalf("BuildZip() = %v, want no error", err)
	}
	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error (the file is only wrong for the trigger)", err)
	}

	destDB := newTestDB(t)
	seedOneUser(t, destDB)
	err = Restore(context.Background(), RestoreParams{DB: destDB, UploadsDir: t.TempDir(), BackupDir: t.TempDir(), Parsed: parsed, Now: time.Unix(1800000000, 0)})
	if err == nil {
		t.Fatal("Restore() = nil, want the trigger to refuse the same-direction reversal")
	}
	if !strings.Contains(err.Error(), "must reverse a dues payment or a normal transaction") {
		t.Errorf("Restore() = %v, want it to name the reversal rule", err)
	}
	var n int
	if err := destDB.QueryRowContext(context.Background(), `SELECT count(*) FROM "transaction"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d transactions left after a refused restore, want 0: the restore rolls back whole", n)
	}
}
