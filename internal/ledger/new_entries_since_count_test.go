package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// NewEntries is the yes/no behind "ada catatan baru sejak itu" (#734): does the
// fund hold any row after the latest count's cutoff. The cutoff is the one place
// id order may be relied on (see the reconciliation table).

// matchedCount takes a count at the fund's current balances, nothing to fix.
func matchedCount(t *testing.T, l *Ledger, f fixture, cash money.Amount) store.Reconciliation {
	t.Helper()
	rec, err := l.TakeReconciliation(context.Background(), TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{AccountID: f.cashID, ActualAmount: cash, Resolution: "matched"}, f.bankZero()},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}
	return rec
}

func newEntriesNow(t *testing.T, l *Ledger, f fixture) bool {
	t.Helper()
	r := monthlyReportAt(t, l, time.Now(), ReportParams{FundID: f.fundID})
	if r.Reconciliation == nil {
		t.Fatal("Reconciliation is nil, want a count to have been taken")
	}
	return r.Reconciliation.NewEntries
}

func today() string { return time.Now().In(wib).Format(time.DateOnly) }

func TestReportNewEntriesFalseWithNothingAfterTheCutoffTrueWithARowAfter(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 100_000, "2026-08-01")
	matchedCount(t, l, f, 100_000)
	if newEntriesNow(t, l, f) {
		t.Fatal("NewEntries = true straight after the count, want false: nothing sits above the cutoff")
	}

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 5_000, today(), nil)
	if !newEntriesNow(t, l, f) {
		t.Error("NewEntries = false after a row above the cutoff, want true")
	}
}

// A count over an empty ledger stores a NULL cutoff: any row at all is newer.
func TestReportNewEntriesWithANullCutoffCountsEveryRow(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	rec := matchedCount(t, l, f, 0)
	if rec.ThroughTransactionID != nil {
		t.Fatalf("ThroughTransactionID = %v, want nil for an empty ledger", rec.ThroughTransactionID)
	}
	if newEntriesNow(t, l, f) {
		t.Fatal("NewEntries = true over an empty ledger, want false")
	}

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 5_000, today(), nil)
	if !newEntriesNow(t, l, f) {
		t.Error("NewEntries = false after the first row, want true")
	}
}

// The count's own fix is posted after the cutoff (ADR-024) but is part of the
// count, not news since it: an adjusted or entry_added fix must not light the
// flag the moment the treasurer finishes counting.
func TestReportNewEntriesIgnoresTheCountsOwnFix(t *testing.T) {
	t.Parallel()
	for _, resolution := range []Resolution{"adjusted", "entry_added"} {
		t.Run(string(resolution), func(t *testing.T) {
			t.Parallel()
			l := newTestLedger(t)
			f := newFixture(t, l)
			postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 100_000, "2026-08-01")

			if _, err := l.TakeReconciliation(context.Background(), TakeReconciliationParams{
				FundID: f.fundID,
				Counts: []AccountCount{{
					AccountID: f.cashID, ActualAmount: 97_000, Resolution: resolution,
					Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 3_000, OccurredOn: today()},
				}, f.bankZero()},
			}); err != nil {
				t.Fatalf("TakeReconciliation() = %v, want no error", err)
			}
			if newEntriesNow(t, l, f) {
				t.Fatal("NewEntries = true straight after a count that posted its own fix, want false")
			}

			postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 1_000, today(), nil)
			if !newEntriesNow(t, l, f) {
				t.Error("NewEntries = false after a later row, want true")
			}
		})
	}
}

// Every row counts, a reversal included.
func TestReportNewEntriesCountsAReversal(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	slip := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 10_000, today(), nil)
	matchedCount(t, l, f, 10_000)
	if newEntriesNow(t, l, f) {
		t.Fatal("NewEntries = true straight after the count, want false")
	}

	if _, err := l.ReverseTransaction(context.Background(), ReverseTransactionParams{FundID: f.fundID, TransactionID: slip.ID}); err != nil {
		t.Fatalf("ReverseTransaction() = %v, want no error", err)
	}
	if !newEntriesNow(t, l, f) {
		t.Error("NewEntries = false after a reversal, want true")
	}
}

// Another fund's rows are not this fund's news.
func TestReportNewEntriesIsScopedToTheFund(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	other := newFixtureWithSlug(t, l, "Other Fund", "zyxwvutsrqponmlkjihgfe")

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 10_000, today(), nil)
	matchedCount(t, l, f, 10_000)

	postEntry(t, l, other.fundID, other.cashID, other.mainID, "in", 7_000, today(), nil)
	if newEntriesNow(t, l, f) {
		t.Error("NewEntries = true after only another fund recorded a row, want false")
	}
}

// A past month's report reads the ledger as of that month's end: a row dated
// after the bound is not in its figures, so it is not news there either, while a
// backdated row entered later, dated inside the month, is.
func TestReportNewEntriesPastMonthIsBoundedByTheMonth(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	q := store.New(l.db)
	f := newFixture(t, l)

	first := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-09-03", nil)
	performedAt := jakartaAt(2026, time.September, 20, 12, 0, 0).Unix()
	if _, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: performedAt, ThroughTransactionID: &first.ID, CreatedAt: performedAt,
	}); err != nil {
		t.Fatalf("CreateReconciliation() = %v, want no error", err)
	}
	september := func() bool {
		t.Helper()
		r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})
		if r.Reconciliation == nil {
			t.Fatal("Reconciliation is nil for September, want the count")
		}
		return r.Reconciliation.NewEntries
	}

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 5_000, "2026-10-01", nil)
	if september() {
		t.Error("September NewEntries = true for a row dated in October, want false")
	}

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 2_000, "2026-09-25", nil)
	if !september() {
		t.Error("September NewEntries = false for a later-entered row dated in September, want true")
	}
}
