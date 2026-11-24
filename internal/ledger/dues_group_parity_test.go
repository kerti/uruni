package ledger

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// The dues group key (ADR-048) is written twice: in SQL, where Riwayat's list
// (ListTransactionsPage) folds a payment's rows and a reversal group into one
// row each, and in Go, where the public report folds them (duesGroupKey). Each
// side has its own tests, so a rule changed on one side alone would leave both
// suites green. This pins the two to the same answer over every way the key can
// split rows: payments told apart only by their payment, a payment reversed on
// two different days (two groups), and a payment reversed whole (one group).
//
// The groups are compared by what each list shows: the day, whether the row is
// a reversal, and the group's amount. The amounts are chosen so that any other
// grouping changes the set.
func TestRiwayatAndTheReportFoldDuesRowsIntoTheSameGroups(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)

	// One payment, two of its months reversed on two different days: three rows.
	first := payDues(t, l, f, "2026-08-05", 10_000, "2026-04", "2026-05", "2026-06")
	postReversalOn(t, l, f.fundID, monthRow(t, first, "2026-05").ID, "2026-08-12")
	postReversalOn(t, l, f.fundID, monthRow(t, first, "2026-06").ID, "2026-08-20")
	// Two look-alike payments on the day of the first reversal: two rows, not one.
	payDues(t, l, f, "2026-08-12", 10_000, "2026-07", "2026-08")
	payDues(t, l, f, "2026-08-12", 10_000, "2026-09", "2026-10")
	// A payment reversed whole on one later day: two rows, the payment and its group.
	last := payDues(t, l, f, "2026-08-15", 7_000, "2026-11", "2026-12")
	for _, row := range last {
		postReversalOn(t, l, f.fundID, row.ID, "2026-08-25")
	}
	// A row that belongs to no group, so neither list may fold it into one.
	postEntry(t, l, f.fundID, f.cashID, f.mainID, DirectionOut, 4_000, "2026-08-12", nil)

	want := []string{
		"2026-08-05 payment 30000",
		"2026-08-12 payment 20000",
		"2026-08-12 payment 20000",
		"2026-08-12 reversal 10000",
		"2026-08-15 payment 14000",
		"2026-08-20 reversal 10000",
		"2026-08-25 reversal 14000",
	}

	report := monthlyReportAt(t, l, reportNow, ReportParams{FundID: f.fundID, Month: "2026-08"})
	var fromReport []string
	for _, r := range report.Rows {
		e := r.Entry
		if e == nil {
			continue
		}
		if reversal := e.IsReversal && len(e.DuesPeriods) > 0; e.Kind != TransactionKindDues && !reversal {
			continue
		}
		fromReport = append(fromReport, duesGroupLine(r.Date, e.IsReversal, int64(e.Amount)))
	}
	slices.Sort(fromReport)

	page, err := store.New(l.db).ListTransactionsPage(ctx, store.ListTransactionsPageParams{
		CashView: 1, PageLimit: 100, FundID: f.fundID, Month: "2026-08",
	})
	if err != nil {
		t.Fatalf("ListTransactionsPage() = %v, want no error", err)
	}
	var fromRiwayat []string
	for _, row := range page {
		reversal := row.ReversesDuesPaymentID != nil
		if row.Kind != string(TransactionKindDues) && !reversal {
			continue
		}
		fromRiwayat = append(fromRiwayat, duesGroupLine(row.OccurredOn, reversal, row.GroupAmount))
	}
	slices.Sort(fromRiwayat)

	if !slices.Equal(fromReport, want) {
		t.Errorf("report dues rows = %q, want %q", fromReport, want)
	}
	if !slices.Equal(fromRiwayat, want) {
		t.Errorf("Riwayat dues rows = %q, want %q", fromRiwayat, want)
	}
}

// duesGroupLine is one listed dues row as both lists can state it.
func duesGroupLine(day string, reversal bool, amount int64) string {
	kind := "payment"
	if reversal {
		kind = "reversal"
	}
	return fmt.Sprintf("%s %s %d", day, kind, amount)
}

// monthRow is the row of a posted payment that pays period.
func monthRow(t *testing.T, rows []store.Transaction, period string) store.Transaction {
	t.Helper()
	for _, r := range rows {
		if r.DuesPeriod != nil && *r.DuesPeriod == period {
			return r
		}
	}
	t.Fatalf("no row pays %s", period)
	return store.Transaction{}
}
