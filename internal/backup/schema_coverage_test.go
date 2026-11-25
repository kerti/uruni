package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// excludedFromSchemaCoverage is every table or column the schema-coverage test
// does not compare, each with the reason it cannot be. column "" excludes the
// whole table. The list is the test's honesty: a new column is compared by
// default, and only lands here with a reason a reviewer can check.
var excludedFromSchemaCoverage = []struct {
	table, column, reason string
}{
	{"session", "", "restore clears every session by design (ADR-012): none survives to compare"},
	{"idempotency_key", "", "restore clears every idempotency key by design (ADR-043, ADR-012): none survives to compare"},
	{"user", "", "restore keeps the live login and never touches the file's (keepLiveUser, ADR-012): the destination's own row differs on purpose"},
}

// gooseTable is goose's bookkeeping, not data: db.Up writes it on both sides.
const gooseTable = "goose_db_version"

// TestRestoreRoundTripCoversEverySchemaColumn checks backup against the
// schema, not against itself (#901, ADR-012). The round-trip tests compare
// re-exported Documents, so a column backup does not know about is absent from
// both sides and the comparison passes: a column added to the schema without
// backup support restores as its default, which is silent data loss found
// only when a treasurer restores. This test compares the raw rows of the
// source and the restored database instead, with tables and columns read
// from sqlite_master and pragma_table_info, so a new column is compared the
// day it is added: a dropped column shows as a differing value, a dropped
// table as missing rows.
//
// Equal-and-empty proves nothing, so a coverage rule backs the comparison:
// every compared column must hold a value other than NULL, 0 or an empty
// string in at least one source row. A column the seed leaves empty fails the test by name: seed
// it here, or exclude it in excludedFromSchemaCoverage with a reason.
func TestRestoreRoundTripCoversEverySchemaColumn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srcDB := newTestDB(t)
	srcUploads := t.TempDir()
	buildFixture(t, srcDB, srcUploads)
	if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("replacing fixture receipt with a real image: %v", err)
	}

	seedColumnsTheFixtureLeavesEmpty(ctx, t, srcDB, srcUploads)

	zipBytes, _ := exportFixture(t, srcDB, srcUploads)
	parsed, err := parseUploadBytes(t, zipBytes)
	if err != nil {
		t.Fatalf("ParseUpload() = %v, want no error", err)
	}
	destDB := newTestDB(t)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

	// An exclusion for something the schema no longer has is a stale reason.
	for _, e := range excludedFromSchemaCoverage {
		var n int
		if e.column == "" {
			if err := srcDB.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, e.table).Scan(&n); err != nil {
				t.Fatalf("checking exclusion %s: %v", e.table, err)
			}
		} else if err := srcDB.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, e.table, e.column).Scan(&n); err != nil {
			t.Fatalf("checking exclusion %s.%s: %v", e.table, e.column, err)
		}
		if n == 0 {
			t.Errorf("excludedFromSchemaCoverage names %s.%s, which the schema does not have: drop the entry", e.table, e.column)
		}
	}

	tables := schemaTables(ctx, t, srcDB)
	var problems []string
	for _, tbl := range tables {
		src := readTable(ctx, t, srcDB, tbl)
		dst := readTable(ctx, t, destDB, tbl)
		problems = append(problems, diffTable(tbl, src, dst)...)
		problems = append(problems, uncoveredColumns(tbl, src)...)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// secondReceiptFilename is the receipt attached to a ledger row, beside
// buildFixture's, which hangs off a reimbursement.
const secondReceiptFilename = "0000000000000000000000000000fe.jpg"

// seedColumnsTheFixtureLeavesEmpty adds, through store.Queries like
// buildFixture, the rows that fill every column the fixture leaves NULL: an
// inactive account, a closed envelope, a waived reimbursement, a purpose
// correction, a receipt on a ledger row, and a second reconciliation with a
// note, a cutoff and an adjusted line carrying its adjusting entry (which has
// a note). They live here, not in buildFixture, so testdata/golden.json and
// the previous-release fixture do not move.
func seedColumnsTheFixtureLeavesEmpty(ctx context.Context, t *testing.T, sqlDB *sql.DB, uploadsDir string) {
	t.Helper()
	q := store.New(sqlDB)
	id := func(query string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := sqlDB.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("looking up a fixture row (%s): %v", query, err)
		}
		return n
	}
	fundID := id(`SELECT id FROM fund`)
	bankID := id(`SELECT id FROM account WHERE name = 'Bank'`)
	mainID := id(`SELECT id FROM purpose WHERE kind = 'main'`)
	passThroughID := id(`SELECT id FROM purpose WHERE kind = 'pass_through'`)
	envelopeID := id(`SELECT id FROM purpose WHERE kind = 'incidental'`)
	andiID := id(`SELECT id FROM member WHERE name = 'Andi'`)
	passThroughTxID := id(`SELECT id FROM "transaction" WHERE purpose_id = ? AND kind = 'normal'`, passThroughID)

	// A retired account (account.inactive_on).
	old, err := q.CreateAccount(ctx, store.CreateAccountParams{FundID: fundID, Kind: "bank", Name: "Rekening lama", CreatedAt: 1700000600})
	if err != nil {
		t.Fatalf("CreateAccount(old): %v", err)
	}
	retired := "2026-03-01"
	if _, err := q.UpdateAccount(ctx, store.UpdateAccountParams{ID: old.ID, SetInactiveOn: 1, InactiveOn: &retired}); err != nil {
		t.Fatalf("UpdateAccount(retire): %v", err)
	}

	// A closed envelope (incidental.closed_on, ADR-034).
	closed := "2026-03-02"
	if _, err := q.CloseIncidental(ctx, store.CloseIncidentalParams{ClosedOn: &closed, PurposeID: envelopeID}); err != nil {
		t.Fatalf("CloseIncidental: %v", err)
	}

	// A waived reimbursement (reimbursement.waived_on): off the ledger, never settled.
	waived := "2026-03-03"
	if _, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: fundID, MemberID: andiID, PurposeID: mainID, Amount: 15000, IncurredOn: "2026-03-01", WaivedOn: &waived, CreatedAt: 1700000610,
	}); err != nil {
		t.Fatalf("CreateReimbursement(waived): %v", err)
	}

	// A purpose correction (transfer.corrects_transaction_id, ADR-033): the
	// pass-through row's money really belonged to the envelope.
	fix, err := q.CreateTransfer(ctx, store.CreateTransferParams{
		FundID: fundID, Kind: "reclass_purpose", CorrectsTransactionID: &passThroughTxID, CreatedAt: 1700000620,
	})
	if err != nil {
		t.Fatalf("CreateTransfer(correction): %v", err)
	}
	for j, leg := range [...]struct {
		purposeID int64
		direction string
	}{{passThroughID, "out"}, {envelopeID, "in"}} {
		if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: fundID, AccountID: bankID, PurposeID: leg.purposeID, Direction: leg.direction, Amount: 75000,
			OccurredOn: "2026-03-04", Kind: "transfer", TransferID: &fix.ID, CreatedAt: 1700000621 + int64(j),
		}); err != nil {
			t.Fatalf("CreateTransaction(correction %s): %v", leg.direction, err)
		}
	}

	// A receipt on a ledger row (receipt.transaction_id), a real image: restore checks it.
	if err := os.WriteFile(filepath.Join(uploadsDir, secondReceiptFilename), realJPEGBytes(t), 0o600); err != nil {
		t.Fatalf("writing second receipt file: %v", err)
	}
	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: fundID, TransactionID: &passThroughTxID, Path: secondReceiptFilename, UploadedAt: 1700000630,
	}); err != nil {
		t.Fatalf("CreateReceipt(transaction): %v", err)
	}

	// An adjusting entry squaring the bank's 5000 difference (transaction.note),
	// then the count that records it: a note, a cutoff at that entry
	// (reconciliation.through_transaction_id) and an adjusted line pointing at it
	// (reconciliation_line.adjustment_transaction_id).
	note := "Selisih bank"
	adj, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fundID, AccountID: bankID, PurposeID: mainID, Direction: "in", Amount: 5000,
		OccurredOn: "2026-03-05", Kind: "adjustment", Note: &note, CreatedAt: 1700000640,
	})
	if err != nil {
		t.Fatalf("CreateTransaction(adjustment): %v", err)
	}
	recNote := "Hitung ulang akhir bulan"
	recon, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: fundID, PerformedAt: 1700000650, ThroughTransactionID: &adj.ID, Note: &recNote, CreatedAt: 1700000650,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation(second): %v", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: fundID, ReconciliationID: recon.ID, AccountID: bankID,
		RecordedAmount: 1175000, ActualAmount: 1180000, DifferenceAmount: 5000, Resolution: "adjusted", AdjustmentTransactionID: &adj.ID,
	}); err != nil {
		t.Fatalf("CreateReconciliationLine(adjusted): %v", err)
	}
}

// tableShape is one table as the schema declares it: the compared columns in
// declaration order, and which of them make up the primary key.
type tableShape struct {
	name string
	cols []string
	pk   []string
}

// tableRows is a table's rows rendered to strings, keyed by primary key.
// Restore preserves every id verbatim (ADR-012 obligation 2; restore.sql names
// id in every insert), so a row in the source and its restored twin share a
// primary key, and rows are matched by it, not by position or content.
type tableRows struct {
	shape tableShape
	rows  map[string][]string
}

func excluded(table, column string) bool {
	for _, e := range excludedFromSchemaCoverage {
		if e.table == table && (e.column == "" || e.column == column) {
			return true
		}
	}
	return false
}

func schemaTables(ctx context.Context, t *testing.T, sqlDB *sql.DB) []tableShape {
	t.Helper()
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> ? ORDER BY name`, gooseTable)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scanning table name: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("closing table list: %v", err)
	}

	var shapes []tableShape
	for _, n := range names {
		if excluded(n, "") {
			continue
		}
		shapes = append(shapes, tableInfo(ctx, t, sqlDB, n))
	}
	return shapes
}

func tableInfo(ctx context.Context, t *testing.T, sqlDB *sql.DB, table string) tableShape {
	t.Helper()
	rows, err := sqlDB.QueryContext(ctx, `SELECT name, pk FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		t.Fatalf("reading columns of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	shape := tableShape{name: table}
	pkAt := map[int]string{}
	for rows.Next() {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			t.Fatalf("scanning columns of %s: %v", table, err)
		}
		if excluded(table, name) {
			continue
		}
		shape.cols = append(shape.cols, name)
		if pk > 0 {
			pkAt[pk] = name
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading columns of %s: %v", table, err)
	}
	for i := 1; i <= len(pkAt); i++ {
		shape.pk = append(shape.pk, pkAt[i])
	}
	if len(shape.pk) == 0 {
		t.Fatalf("table %s has no primary key: rows cannot be matched across the restore", table)
	}
	return shape
}

func readTable(ctx context.Context, t *testing.T, sqlDB *sql.DB, shape tableShape) tableRows {
	t.Helper()
	quoted := make([]string, len(shape.cols))
	for i, c := range shape.cols {
		quoted[i] = `"` + c + `"`
	}
	//nolint:gosec // table and column names come from sqlite_master, never from input
	rows, err := sqlDB.QueryContext(ctx, `SELECT `+strings.Join(quoted, ", ")+` FROM "`+shape.name+`"`)
	if err != nil {
		t.Fatalf("reading %s: %v", shape.name, err)
	}
	defer func() { _ = rows.Close() }()
	out := tableRows{shape: shape, rows: map[string][]string{}}
	for rows.Next() {
		vals := make([]any, len(shape.cols))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scanning %s: %v", shape.name, err)
		}
		rendered := make([]string, len(vals))
		for i, v := range vals {
			rendered[i] = renderValue(v)
		}
		out.rows[rowKey(shape, rendered)] = rendered
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading %s: %v", shape.name, err)
	}
	return out
}

// renderValue keeps the storage class visible (NULL, integer, text, blob), so
// a restore that turns NULL into 0 or an empty string differs, not merely prints alike.
func renderValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return fmt.Sprintf("int:%d", x)
	case string:
		return fmt.Sprintf("text:%q", x)
	case []byte:
		return fmt.Sprintf("blob:%x", x)
	default:
		return fmt.Sprintf("%T:%v", x, x)
	}
}

func rowKey(shape tableShape, rendered []string) string {
	parts := make([]string, len(shape.pk))
	for i, pk := range shape.pk {
		for j, c := range shape.cols {
			if c == pk {
				parts[i] = pk + "=" + rendered[j]
			}
		}
	}
	return strings.Join(parts, ",")
}

func diffTable(shape tableShape, src, dst tableRows) []string {
	var out []string
	keys := make([]string, 0, len(src.rows))
	for k := range src.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		want := src.rows[k]
		got, ok := dst.rows[k]
		if !ok {
			out = append(out, fmt.Sprintf("%s row %s: in the source, missing after restore", shape.name, k))
			continue
		}
		for i, c := range shape.cols {
			if want[i] != got[i] {
				out = append(out, fmt.Sprintf("%s row %s, column %s: source %s, restored %s", shape.name, k, c, want[i], got[i]))
			}
		}
	}
	for k := range dst.rows {
		if _, ok := src.rows[k]; !ok {
			out = append(out, fmt.Sprintf("%s row %s: restored but not in the source", shape.name, k))
		}
	}
	sort.Strings(out)
	return out
}

func uncoveredColumns(shape tableShape, src tableRows) []string {
	var out []string
	for i, c := range shape.cols {
		covered := false
		for _, row := range src.rows {
			if v := row[i]; v != "NULL" && v != "int:0" && v != `text:""` {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, fmt.Sprintf("%s.%s is NULL, 0 or empty in every source row, so the comparison proves nothing: seed it in this test, or add it to excludedFromSchemaCoverage with a reason", shape.name, c))
		}
	}
	return out
}
