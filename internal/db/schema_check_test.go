package db

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
)

// migratedDB is a file-backed database with every embedded migration applied:
// an up-to-date instance.
func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := openTestDB(t)
	if _, err := Up(context.Background(), sqlDB, discardLogger()); err != nil {
		t.Fatalf("Up() = %v, want no error", err)
	}
	return sqlDB
}

func mustExec(t *testing.T, sqlDB *sql.DB, stmt string) {
	t.Helper()
	if _, err := sqlDB.Exec(stmt); err != nil {
		t.Fatalf("exec %q = %v, want no error", stmt, err)
	}
}

func checkSchema(t *testing.T, sqlDB *sql.DB) SchemaDiff {
	t.Helper()
	diff, err := CheckSchema(context.Background(), sqlDB)
	if err != nil {
		t.Fatalf("CheckSchema() = %v, want no error", err)
	}
	return diff
}

func TestCheckSchemaPassesAFreshlyMigratedDatabase(t *testing.T) {
	t.Parallel()
	if diff := checkSchema(t, migratedDB(t)); !diff.Matches() {
		t.Errorf("CheckSchema() on a fresh database = %+v, want a match", diff)
	}
}

func TestCheckSchemaPassesAnUpToDateDatabaseMigratedTwiceAndReopened(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := t.TempDir() + "/uruni.db"

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	if _, err := Up(ctx, first, discardLogger()); err != nil {
		t.Fatalf("first Up() = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	// What a restart does: reopen, migrate (a no-op), check.
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen Open() = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if n, err := Up(ctx, second, discardLogger()); err != nil || n != 0 {
		t.Fatalf("second Up() = %d, %v, want 0, nil", n, err)
	}
	if diff := checkSchema(t, second); !diff.Matches() {
		t.Errorf("CheckSchema() on a reopened database = %+v, want a match", diff)
	}
}

func TestCheckSchemaRefusesADatabaseDifferingByOneColumn(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	// The stale-database shape ADR-025 warns about: the old build's fund table
	// lacked (or had an extra) column, and goose still reports everything applied.
	mustExec(t, sqlDB, `ALTER TABLE fund ADD COLUMN stray TEXT`)

	diff := checkSchema(t, sqlDB)
	if diff.Matches() {
		t.Fatal("CheckSchema() with an extra column = match, want a mismatch")
	}
	if len(diff.Changed) != 1 || diff.Changed[0] != "table fund" {
		t.Errorf("Changed = %v, want [table fund]", diff.Changed)
	}
	if len(diff.Missing) != 0 || len(diff.Unexpected) != 0 {
		t.Errorf("Missing = %v, Unexpected = %v, want both empty", diff.Missing, diff.Unexpected)
	}
	if got := diff.Summary(5); got != "table fund changed" {
		t.Errorf("Summary() = %q, want %q", got, "table fund changed")
	}
}

func TestCheckSchemaRefusesADroppedColumn(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	mustExec(t, sqlDB, `ALTER TABLE account DROP COLUMN inactive_on`)

	diff := checkSchema(t, sqlDB)
	if len(diff.Changed) != 1 || diff.Changed[0] != "table account" {
		t.Errorf("Changed = %v, want [table account]", diff.Changed)
	}
}

func TestCheckSchemaDetectsAMissingObject(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	mustExec(t, sqlDB, `DROP INDEX session_by_expiry`)
	mustExec(t, sqlDB, `DROP TRIGGER transaction_named_row_shape`)

	diff := checkSchema(t, sqlDB)
	want := []string{"index session_by_expiry", "trigger transaction_named_row_shape"}
	if strings.Join(diff.Missing, "|") != strings.Join(want, "|") {
		t.Errorf("Missing = %v, want %v", diff.Missing, want)
	}
	if len(diff.Changed) != 0 || len(diff.Unexpected) != 0 {
		t.Errorf("Changed = %v, Unexpected = %v, want both empty", diff.Changed, diff.Unexpected)
	}
}

// idempotency_key (ADR-043) is part of the schema the boot guard holds: a file
// from before the table, which goose still reports as fully applied, is refused
// rather than served by a binary whose keyed writes would fail on every request.
func TestCheckSchemaCoversTheIdempotencyKeyTable(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	mustExec(t, sqlDB, `DROP TABLE idempotency_key`)

	diff := checkSchema(t, sqlDB)
	want := []string{"index idempotency_key_by_expiry", "table idempotency_key"}
	got := append([]string(nil), diff.Missing...)
	slices.Sort(got)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Missing = %v, want %v", got, want)
	}
	if len(diff.Changed) != 0 || len(diff.Unexpected) != 0 {
		t.Errorf("Changed = %v, Unexpected = %v, want both empty", diff.Changed, diff.Unexpected)
	}
}

// ledger_entry (#627) is part of the schema the boot guard holds: a database
// missing the view, or carrying a different one, is refused rather than served
// with balances that read from nothing.
func TestCheckSchemaCoversTheLedgerEntryView(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	mustExec(t, sqlDB, `DROP VIEW ledger_entry`)
	if diff := checkSchema(t, sqlDB); strings.Join(diff.Missing, "|") != "view ledger_entry" {
		t.Errorf("Missing = %v, want [view ledger_entry]", diff.Missing)
	}

	mustExec(t, sqlDB, `CREATE VIEW ledger_entry AS SELECT id FROM "transaction"`)
	if diff := checkSchema(t, sqlDB); strings.Join(diff.Changed, "|") != "view ledger_entry" {
		t.Errorf("Changed = %v, want [view ledger_entry]", diff.Changed)
	}
}

// The view as #627 shipped it, without effective_purpose_id (#628): the database
// an older build left behind. goose still reports every migration applied, so the
// boot guard is the only thing that notices the view is the old one.
func TestCheckSchemaRefusesALedgerEntryWithoutTheEffectivePurpose(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	mustExec(t, sqlDB, `DROP VIEW ledger_entry`)
	mustExec(t, sqlDB, `CREATE VIEW ledger_entry AS
SELECT t.id, t.fund_id, t.account_id, t.purpose_id, t.direction, t.amount, t.occurred_on, t.kind,
       t.member_id, t.dues_period, t.reimbursement_id, t.transfer_id, t.reverses_transaction_id,
       t.note, t.created_at,
       CAST(CASE WHEN t.direction = 'in' THEN t.amount ELSE -t.amount END AS INTEGER) AS signed_amount,
       (SELECT r.id FROM "transaction" r WHERE r.reverses_transaction_id = t.id) AS reversed_by_transaction_id,
       (SELECT r.occurred_on FROM "transaction" r WHERE r.reverses_transaction_id = t.id) AS reversed_on,
       CAST(EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id) AS INTEGER) AS reversed
FROM "transaction" t`)

	diff := checkSchema(t, sqlDB)
	if strings.Join(diff.Changed, "|") != "view ledger_entry" {
		t.Errorf("Changed = %v, want [view ledger_entry]", diff.Changed)
	}
	if len(diff.Missing) != 0 || len(diff.Unexpected) != 0 {
		t.Errorf("Missing = %v, Unexpected = %v, want both empty", diff.Missing, diff.Unexpected)
	}
}

func TestCheckSchemaDetectsAnUnexpectedObject(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	mustExec(t, sqlDB, `CREATE TABLE leftover (id INTEGER PRIMARY KEY) STRICT`)
	mustExec(t, sqlDB, `CREATE VIEW leftover_view AS SELECT id FROM leftover`)

	diff := checkSchema(t, sqlDB)
	want := []string{"table leftover", "view leftover_view"}
	if strings.Join(diff.Unexpected, "|") != strings.Join(want, "|") {
		t.Errorf("Unexpected = %v, want %v", diff.Unexpected, want)
	}
}

func TestCheckSchemaIgnoresSQLiteInternalsAndGoose(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	// sqlite_sequence appears with the first AUTOINCREMENT table (goose's own);
	// ANALYZE adds sqlite_stat1. Neither is the schema's business.
	mustExec(t, sqlDB, `ANALYZE`)

	if diff := checkSchema(t, sqlDB); !diff.Matches() {
		t.Errorf("CheckSchema() after ANALYZE = %+v, want a match", diff)
	}
}

func TestCheckSchemaIgnoresACommentOnlyEdit(t *testing.T) {
	t.Parallel()
	got, err := expectedSchema(context.Background())
	if err != nil {
		t.Fatalf("expectedSchema() = %v", err)
	}
	// A database whose file's comments and layout, and only those, were later
	// edited: same structure, different bytes.
	edited := make([]schemaObject, len(got))
	copy(edited, got)
	for i, o := range edited {
		edited[i].SQL = strings.Replace(o.SQL, " ", "  -- a new comment\n\t/* and a block */  ", 1)
	}
	// normalizeDDL runs on read, so feed the raw text through it as readSchema does.
	for i := range edited {
		edited[i].SQL = normalizeDDL(edited[i].SQL)
	}
	if diff := compareSchemas(got, edited); !diff.Matches() {
		t.Errorf("compareSchemas() on a comment-only edit = %+v, want a match", diff)
	}
}

func TestSummaryNamesAtMostLimitObjects(t *testing.T) {
	t.Parallel()
	d := SchemaDiff{
		Changed:    []string{"table a", "table b"},
		Missing:    []string{"index c", "index d"},
		Unexpected: []string{"view e", "view f"},
	}
	got := d.Summary(3)
	want := "table a changed, table b changed, index c missing, and 3 more"
	if got != want {
		t.Errorf("Summary(3) = %q, want %q", got, want)
	}
	if got := (SchemaDiff{}).Summary(3); got != "" {
		t.Errorf("Summary() of a match = %q, want empty", got)
	}
}

func TestNormalizeDDL(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"line comment", "CREATE TABLE t ( -- note\n  a INTEGER\n)", "CREATE TABLE t ( a INTEGER )"},
		{"block comment", "CREATE /* x */ TABLE t(a)", "CREATE TABLE t(a)"},
		{"whitespace runs", "CREATE   TABLE\n\tt(a)", "CREATE TABLE t(a)"},
		{"dashes in a string literal survive", "CHECK (a <> '--  x')", "CHECK (a <> '--  x')"},
		{"escaped quote inside a literal", "DEFAULT 'it''s -- ok'", "DEFAULT 'it''s -- ok'"},
		{"quoted identifier survives", `CREATE TABLE "a  b" (x)`, `CREATE TABLE "a  b" (x)`},
		{"a different column still differs", "CREATE TABLE t (a, b)", "CREATE TABLE t (a, b)"},
	}
	for _, tc := range cases {
		if got := normalizeDDL(tc.in); got != tc.want {
			t.Errorf("%s: normalizeDDL(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
	if normalizeDDL("CREATE TABLE t (a)") == normalizeDDL("CREATE TABLE t (a, b)") {
		t.Error("normalizeDDL erased a real column difference")
	}
}

func TestCheckSchemaIgnoresACommentEditedInTheStoredDDL(t *testing.T) {
	t.Parallel()
	sqlDB := migratedDB(t)
	// SQLite keeps comments inside a CREATE verbatim; rewriting the stored text
	// stands in for an instance built from an older revision whose only change
	// since was a comment.
	mustExec(t, sqlDB, `PRAGMA writable_schema = ON`)
	mustExec(t, sqlDB, `UPDATE sqlite_schema SET sql = replace(sql, 'CREATE TABLE fund (', 'CREATE TABLE fund ( -- an older comment' || char(10)) WHERE name = 'fund'`)
	mustExec(t, sqlDB, `PRAGMA writable_schema = OFF`)

	var stored string
	if err := sqlDB.QueryRow(`SELECT sql FROM sqlite_schema WHERE name = 'fund'`).Scan(&stored); err != nil {
		t.Fatalf("reading fund DDL = %v", err)
	}
	if !strings.Contains(stored, "an older comment") {
		t.Fatalf("test setup: stored DDL %q lacks the injected comment", stored)
	}
	if diff := checkSchema(t, sqlDB); !diff.Matches() {
		t.Errorf("CheckSchema() after a comment-only difference = %+v, want a match", diff)
	}
}
