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

var (
	sqlcParam = regexp.MustCompile(`\?(\d+)`)
	sqlcSlice = regexp.MustCompile(`/\*SLICE:\w+\*/\?`)
)

// queryPlan returns EXPLAIN QUERY PLAN's detail lines for query, binding
// every parameter to NULL (the plan does not depend on the values): sqlc's
// numbered ?N, or a query's bare ? placeholders, one each. A sqlc.slice
// becomes NULL, which binds nothing and plans as one value.
func queryPlan(t *testing.T, sqlDB *sql.DB, query string) []string {
	t.Helper()
	query = sqlcSlice.ReplaceAllString(query, "NULL")
	n := 0
	for _, m := range sqlcParam.FindAllStringSubmatch(query, -1) {
		var i int
		for _, c := range m[1] {
			i = i*10 + int(c-'0')
		}
		n = max(n, i)
	}
	if n == 0 {
		n = strings.Count(query, "?")
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
	// The placed date (ADR-051, #845) reads the claim's incurred_on for the
	// filter in `matched` and for the order and keyset outside it: both joins to
	// reimbursement must be a primary-key search, one per payout.
	joined := strings.Join(plan, "\n")
	if n := strings.Count(joined, "SEARCH rb USING INTEGER PRIMARY KEY (rowid=?)"); n != 2 {
		t.Errorf("GET /api/transactions' query searches the claim %d times by key, want 2; plan:\n%s", n, joined)
	}
}

func TestListReportTransactionsReadsTheTwoPlacedHalvesBySearch(t *testing.T) {
	t.Parallel()
	// The report places a settled talangan's payout by its claim's incurred_on
	// (ADR-051), so the rows are two searches, one per half, never one OR over
	// two tables (which would visit every row of the fund). has_receipt's
	// EXISTS runs once per row, so its two halves are searches too (#848).
	plan := queryPlan(t, planDB(t), listReportTransactions)
	joined := strings.Join(plan, "\n")
	for _, want := range []string{
		"SEARCH x USING INDEX transaction_by_date (fund_id=? AND occurred_on>?)",
		"SEARCH c USING COVERING INDEX reimbursement_by_date (fund_id=? AND incurred_on>?)",
		"SEARCH x USING COVERING INDEX reimbursement_settled_once (reimbursement_id=?)",
		"SEARCH rc USING INDEX receipt_by_transaction (transaction_id=?)",
		"SEARCH rc USING INDEX receipt_by_reimbursement (reimbursement_id=?)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the report's rows have no %q; plan:\n%s", want, joined)
		}
	}
	for _, line := range plan {
		if strings.HasPrefix(line, "SCAN ") {
			t.Errorf("the report's rows scan: %q; plan:\n%s", line, joined)
		}
	}

	plan = queryPlan(t, planDB(t), reportPayoutsPaidEarlier)
	if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "SEARCH t USING INDEX transaction_by_date") {
		t.Errorf("the paid-earlier sum is not a date search; plan:\n%s", joined)
	}
}

func TestReceiptLookupsSearchByParent(t *testing.T) {
	t.Parallel()
	// Every read of a row's photos finds them by parent, never by a scan of
	// every photo the fund has (#848): the viewer's, and the lists' batched
	// receipt_ids, one query per page.
	sqlDB := planDB(t)
	for name, c := range map[string]struct{ query, want string }{
		"ListReceiptsByTransaction":        {listReceiptsByTransaction, "SEARCH receipt USING INDEX receipt_by_transaction (transaction_id=?)"},
		"ListReceiptsByReimbursement":      {listReceiptsByReimbursement, "SEARCH receipt USING INDEX receipt_by_reimbursement (reimbursement_id=?)"},
		"ListReceiptIDsByTransactionIDs":   {listReceiptIDsByTransactionIDs, "SEARCH receipt USING INDEX receipt_by_transaction (transaction_id=?)"},
		"ListReceiptIDsByReimbursementIDs": {listReceiptIDsByReimbursementIDs, "SEARCH receipt USING INDEX receipt_by_reimbursement (reimbursement_id=?)"},
	} {
		plan := queryPlan(t, sqlDB, c.query)
		if joined := strings.Join(plan, "\n"); !strings.Contains(joined, c.want) {
			t.Errorf("%s has no %q; plan:\n%s", name, c.want, joined)
		}
	}
}

func TestDuesGroupRowsSearchesNotScans(t *testing.T) {
	t.Parallel()
	// A page's dues groups are its payments' rows and their reversals: two
	// searches. One join ON own OR reversal scanned the whole ledger (#861).
	plan := queryPlan(t, planDB(t), listDuesGroupRows)
	joined := strings.Join(plan, "\n")
	for _, want := range []string{
		"SEARCH x USING INDEX transaction_by_dues_payment (dues_payment_id=?)",
		"SEARCH r USING COVERING INDEX dues_payment_reversed_once (reverses_transaction_id=?)",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ListDuesGroupRows has no %q; plan:\n%s", want, joined)
		}
	}
	for _, line := range plan {
		if strings.HasPrefix(line, "SCAN ") {
			t.Errorf("ListDuesGroupRows scans: %q; plan:\n%s", line, joined)
		}
	}
}

func TestCekKasProbesSearchPerRow(t *testing.T) {
	t.Parallel()
	// Every ledger read asks two things of the cek kas per row it returns: is
	// the row counted, and did it square a counted line. Each must be one
	// search, not a walk of every snapshot or line of the fund (#861).
	sqlDB := planDB(t)
	for name, query := range map[string]string{
		"ListTransactionsPage":           listTransactionsPage,
		"ListDuesGroupRows":              listDuesGroupRows,
		"ListDuesPaymentReversalTargets": listDuesPaymentReversalTargets,
		"GetReversalTarget":              getReversalTarget,
	} {
		joined := strings.Join(queryPlan(t, sqlDB, query), "\n")
		for _, want := range []string{
			"SEARCH rl USING INDEX reconciliation_line_by_adjustment (adjustment_transaction_id=?)",
			"SEARCH rc USING COVERING INDEX reconciliation_by_cutoff (fund_id=? AND through_transaction_id>?)",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("%s has no %q; plan:\n%s", name, want, joined)
			}
		}
		if strings.Contains(joined, "SCAN rl") || strings.Contains(joined, "SCAN rc") {
			t.Errorf("%s scans the cek kas; plan:\n%s", name, joined)
		}
	}
}

func TestIncidentalRecipientsSearchByKey(t *testing.T) {
	t.Parallel()
	// The primary key leads with fund_id, so the lookup names it (#861).
	plan := queryPlan(t, planDB(t), listIncidentalRecipients)
	if joined := strings.Join(plan, "\n"); !strings.Contains(joined, "SEARCH ir USING COVERING INDEX sqlite_autoindex_incidental_recipient_1 (fund_id=? AND purpose_id=?)") {
		t.Errorf("ListIncidentalRecipients is no key search; plan:\n%s", joined)
	}
}
