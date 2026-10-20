package backup

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// newFileTestDBs opens one migrated, file-backed database through two
// independent pools (reader and writer), so a test can land a committed write
// from "another connection" at an exact point inside a read. The shared
// :memory: helper cannot: its single connection has no second handle. This is
// #496's cross-connection shape; in production the interleaving needs a second
// process, as ADR-004's one connection keeps in-process writers out of an open
// transaction.
func newFileTestDBs(t *testing.T) (reader, writer *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "uruni.db")
	open := func() *sql.DB {
		sqlDB, err := db.Open(context.Background(), path)
		if err != nil {
			t.Fatalf("db.Open(%q) = %v, want no error", path, err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		return sqlDB
	}
	reader = open()
	if _, err := db.Up(context.Background(), reader, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v, want no error", err)
	}
	return reader, open()
}

// injectingQuerier runs inject once, right after the export has read the
// fund's transaction rows and before anything else: where, before #496, a
// write would land between the rows and the totals.
type injectingQuerier struct {
	store.Querier
	once   sync.Once
	inject func()
}

func (q *injectingQuerier) ListTransactionsByFund(ctx context.Context, fundID int64) ([]store.Transaction, error) {
	rows, err := q.Querier.ListTransactionsByFund(ctx, fundID)
	q.once.Do(q.inject)
	return rows, err
}

// postDuesLikeIncome commits one more income row through the writer's
// connection: a valid posting, what a second process (or, before the
// transaction, the HTTP handler) would land mid-export.
func postDuesLikeIncome(t *testing.T, writer *sql.DB, doc Document) {
	t.Helper()
	var main Purpose
	for _, p := range doc.Purposes {
		if p.Kind == "main" {
			main = p
		}
	}
	if _, err := store.New(writer).CreateTransaction(context.Background(), store.CreateTransactionParams{
		FundID: main.FundID, AccountID: doc.Accounts[0].ID, PurposeID: main.ID, Direction: "in", Amount: 12345,
		OccurredOn: "2026-03-01", Kind: "normal", CreatedAt: 1700009999,
	}); err != nil {
		t.Fatalf("injected CreateTransaction() = %v, want no error", err)
	}
}

// TestWriteMidExportCannotTearTheDocument is #496's core claim, made
// deterministic: the write is committed synchronously from the hook, not
// raced. In BuildDocument's one read transaction the export keeps reading its
// snapshot, so rows and totals describe one instant and CheckTotals passes;
// the write is simply not in this backup.
func TestWriteMidExportCannotTearTheDocument(t *testing.T) {
	t.Parallel()
	reader, writer := newFileTestDBs(t)
	buildFixture(t, reader, t.TempDir())
	ctx := context.Background()

	before, _, err := BuildDocument(ctx, reader)
	if err != nil {
		t.Fatalf("BuildDocument(before) = %v, want no error", err)
	}

	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx() = %v, want no error", err)
	}
	defer func() { _ = tx.Rollback() }()
	hooked := &injectingQuerier{Querier: store.New(tx), inject: func() { postDuesLikeIncome(t, writer, before) }}

	doc, _, err := readDocument(ctx, hooked)
	if err != nil {
		t.Fatalf("readDocument() = %v, want no error", err)
	}
	if got, want := len(doc.Transactions), len(before.Transactions); got != want {
		t.Fatalf("the document holds %d transactions, want %d: a write made mid-read leaked into the snapshot", got, want)
	}
	if err := CheckTotals(doc); err != nil {
		t.Errorf("CheckTotals() = %v, want nil: the rows and the totals must be one snapshot", err)
	}
	if doc.Totals.Funds[0].Balance != before.Totals.Funds[0].Balance {
		t.Errorf("fund total = %d, want the pre-write %d", doc.Totals.Funds[0].Balance, before.Totals.Funds[0].Balance)
	}

	// The write did land, and the next export carries it - consistently.
	_ = tx.Rollback()
	after, _, err := BuildDocument(ctx, reader)
	if err != nil {
		t.Fatalf("BuildDocument(after) = %v, want no error", err)
	}
	if got, want := len(after.Transactions), len(before.Transactions)+1; got != want {
		t.Errorf("the next export holds %d transactions, want %d", got, want)
	}
	if err := CheckTotals(after); err != nil {
		t.Errorf("CheckTotals(after) = %v, want nil", err)
	}
}

// TestReadingWithoutATransactionTearsTheDocument is the control for the test
// above: the same injected write and hook, read statement by statement as the
// export did before #496. The document comes out torn (rows from before the
// write, totals from after) and CheckTotals names it. Without this, the test
// above could pass for the wrong reason (a hook that never fires, a write that
// never lands).
func TestReadingWithoutATransactionTearsTheDocument(t *testing.T) {
	t.Parallel()
	reader, writer := newFileTestDBs(t)
	buildFixture(t, reader, t.TempDir())
	ctx := context.Background()

	before, _, err := BuildDocument(ctx, reader)
	if err != nil {
		t.Fatalf("BuildDocument(before) = %v, want no error", err)
	}

	hooked := &injectingQuerier{Querier: store.New(reader), inject: func() { postDuesLikeIncome(t, writer, before) }}
	torn, _, err := readDocument(ctx, hooked)
	if err != nil {
		t.Fatalf("readDocument() = %v, want no error", err)
	}
	if err := CheckTotals(torn); !errors.Is(err, ErrTotalsMismatch) {
		t.Fatalf("CheckTotals(torn) = %v, want ErrTotalsMismatch: the hook must be able to tear a document that is not read in a transaction", err)
	}
}

// tamperedReader hands the dump path a document whose totals disagree with its
// rows by one rupiah, the smallest torn document.
func tamperedReader(t *testing.T, sqlDB *sql.DB) documentReader {
	t.Helper()
	return func(ctx context.Context) (Document, []store.Receipt, error) {
		doc, receipts, err := BuildDocument(ctx, sqlDB)
		if err != nil {
			t.Fatalf("BuildDocument() = %v, want no error", err)
		}
		doc.Totals.Funds[0].Balance++
		return doc, receipts, nil
	}
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) = %v, want no error", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("%s holds %d entries, want none (no dump, no temp file): %v", dir, len(entries), entries)
	}
}

// TestDumpRefusesADocumentWhoseTotalsDisagreeWithItsRows: every producer
// shares buildDumpMaterials, so each is refused with ErrTotalsMismatch and the
// backup directory stays empty: nothing renamed into place, no temp file left.
// (Each caller logs the error at error level: main.go for the boot dump, the
// scheduler tick for the daily one, the HTTP handler for the download and the
// pre-restore safety net.)
func TestDumpRefusesADocumentWhoseTotalsDisagreeWithItsRows(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	ctx := context.Background()
	read := tamperedReader(t, sqlDB)

	t.Run("WriteDump", func(t *testing.T) {
		t.Parallel()
		backupDir := t.TempDir()
		name, err := writeDump(ctx, read, assembleZip, uploadsDir, backupDir, tz.Jakarta, KindDaily, jakartaTime(2026, 9, 30, 10, 0, 0))
		if !errors.Is(err, ErrTotalsMismatch) {
			t.Fatalf("writeDump() = (%q, %v), want ErrTotalsMismatch", name, err)
		}
		assertDirEmpty(t, backupDir)
	})
	t.Run("WriteDailyIfNeeded", func(t *testing.T) {
		t.Parallel()
		backupDir := t.TempDir()
		name, written, err := writeDailyIfNeeded(ctx, read, assembleZip, nil, uploadsDir, backupDir, tz.Jakarta, jakartaTime(2026, 9, 30, 10, 0, 0))
		if !errors.Is(err, ErrTotalsMismatch) || written {
			t.Fatalf("writeDailyIfNeeded() = (%q, %v, %v), want (_, false, ErrTotalsMismatch)", name, written, err)
		}
		assertDirEmpty(t, backupDir)
	})
	t.Run("Export", func(t *testing.T) {
		t.Parallel()
		if _, err := buildDumpMaterials(ctx, read, assembleZip, uploadsDir); !errors.Is(err, ErrTotalsMismatch) {
			t.Fatalf("buildDumpMaterials() = %v, want ErrTotalsMismatch (the download shares this path)", err)
		}
	})
}

// TestDumpOfAConsistentDocumentIsStillWritten: the check refuses only a bad
// document, never a good one.
func TestDumpOfAConsistentDocumentIsStillWritten(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()

	name, err := WriteDump(context.Background(), sqlDB, uploadsDir, backupDir, KindDaily, jakartaTime(2026, 9, 30, 10, 0, 0))
	if err != nil {
		t.Fatalf("WriteDump() = %v, want no error", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, name)); err != nil {
		t.Errorf("the dump %q is not on disk: %v", name, err)
	}
}
