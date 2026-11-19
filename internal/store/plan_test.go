package store

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/db"
)

// These tests hold two query plans to searches, because each read went
// quadratic in the ledger without them (#838): at 5,233 rows GET
// /api/transactions took 2.7 s, 14 ms after. Production never runs ANALYZE,
// so the plans are taken on a freshly migrated database with no statistics -
// the case that matters, and the one where the second regression hid.

// planDB is a migrated, empty database: the planner's choice with no
// statistics, as on every real instance.
func planDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "uruni.db"))
	if err != nil {
		t.Fatalf("db.Open() = %v, want no error", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Up(context.Background(), sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v, want no error", err)
	}
	return sqlDB
}

var sqlcParam = regexp.MustCompile(`\?(\d+)`)

// queryPlan returns EXPLAIN QUERY PLAN's detail lines for query, binding
// every ?N parameter to NULL (the plan does not depend on the values).
func queryPlan(t *testing.T, sqlDB *sql.DB, query string) []string {
	t.Helper()
	n := 0
	for _, m := range sqlcParam.FindAllStringSubmatch(query, -1) {
		var i int
		for _, c := range m[1] {
			i = i*10 + int(c-'0')
		}
		n = max(n, i)
	}
	args := make([]any, n)
	rows, err := sqlDB.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN = %v, want no error", err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scanning the plan = %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the plan = %v", err)
	}
	return plan
}

func TestLedgerEntrySearchesNotScans(t *testing.T) {
	t.Parallel()
	// effective_purpose_id runs once per ledger row: the transfer correcting
	// the row, then its legs. Each must be an index search.
	plan := queryPlan(t, planDB(t), "SELECT effective_purpose_id FROM ledger_entry")
	joined := strings.Join(plan, "\n")
	for _, want := range []string{"SEARCH c USING COVERING INDEX transfer_by_corrects", "SEARCH leg USING INDEX transaction_by_transfer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ledger_entry plan has no %q; plan:\n%s", want, joined)
		}
	}
	for _, line := range plan {
		if strings.HasPrefix(line, "SCAN c") || strings.HasPrefix(line, "SCAN leg") {
			t.Errorf("ledger_entry scans per row: %q; plan:\n%s", line, joined)
		}
	}
}

func TestListTransactionsPageJoinsNothingByScan(t *testing.T) {
	t.Parallel()
	// A LEFT JOIN answered by SCAN visits every row of the joined table for
	// every candidate row. It was `keyed` (the group-key CTE), joined back
	// on id with no index to search.
	plan := queryPlan(t, planDB(t), listTransactionsPage)
	for _, line := range plan {
		if strings.HasPrefix(line, "SCAN ") && strings.HasSuffix(line, "LEFT-JOIN") {
			t.Errorf("GET /api/transactions' query joins by scan: %q; plan:\n%s", line, strings.Join(plan, "\n"))
		}
	}
}
