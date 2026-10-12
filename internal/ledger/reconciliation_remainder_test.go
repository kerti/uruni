package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// #498: an "adjusted" line is a plug and must square its difference exactly;
// an "entry_added" line may square only part of it, and whatever is left (the
// remainder, difference - signed(fix)) keeps the line open everywhere a
// line's status is read - the open-lines list, the Cek kas list row and the
// public report. These tests replace the one that used to pin a 20,000 plug
// against a 30,000 gap as accepted.

// remainderFixture is a fund holding 100,000 in cash, with the bank at 0.
func remainderFixture(t *testing.T) (*Ledger, fixture, store.Querier) {
	t.Helper()
	l := newTestLedger(t)
	f := newFixture(t, l)
	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 100_000, "2026-08-01")
	return l, f, store.New(l.db)
}

// takeCash counts the cash account (the bank always counts 0, matched) with
// one resolution and one fix.
func takeCash(l *Ledger, f fixture, actual money.Amount, resolution string, fix *Fix) (store.Reconciliation, error) {
	return l.TakeReconciliation(context.Background(), TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{AccountID: f.cashID, ActualAmount: actual, Resolution: resolution, Fix: fix}, f.bankZero()},
	})
}

func fixOf(f fixture, direction string, amount money.Amount) *Fix {
	return &Fix{PurposeID: f.mainID, Direction: direction, Amount: amount, OccurredOn: "2026-08-12"}
}

func cashLine(t *testing.T, q store.Querier, f fixture, rec store.Reconciliation) store.ListReconciliationLinesRow {
	t.Helper()
	lines, err := q.ListReconciliationLines(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	return lineFor(t, lines, f.cashID)
}

func TestTakeReconciliationAdjustedExactPlugIsAcceptedAndClosesTheLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		actual    money.Amount
		direction string
		amount    money.Amount
		diff      int64
	}{
		{"plug in for a surplus", 130_000, "in", 30_000, 30_000},
		{"plug out for a shortfall", 70_000, "out", 30_000, -30_000},
		{"plug out down to zero", 0, "out", 100_000, -100_000},
		{"one rupiah", 100_001, "in", 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, f, q := remainderFixture(t)
			ctx := context.Background()

			rec, err := takeCash(l, f, tc.actual, "adjusted", fixOf(f, tc.direction, tc.amount))
			if err != nil {
				t.Fatalf("TakeReconciliation() = %v, want no error", err)
			}
			line := cashLine(t, q, f, rec)
			if line.DifferenceAmount != tc.diff {
				t.Errorf("DifferenceAmount = %d, want %d", line.DifferenceAmount, tc.diff)
			}
			if line.RemainderAmount != 0 {
				t.Errorf("RemainderAmount = %d, want 0", line.RemainderAmount)
			}
			open, err := q.ListOpenReconciliationLinesByFund(ctx, f.fundID)
			if err != nil || len(open) != 0 {
				t.Errorf("open lines = %+v, %v, want none", open, err)
			}
			bal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
			if err != nil || bal != tc.actual {
				t.Errorf("AccountBalance() = %d, %v, want the counted %d", bal, err, tc.actual)
			}
		})
	}
}

func TestTakeReconciliationAdjustedPlugThatDoesNotEqualTheDifferenceIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		actual    money.Amount
		direction string
		amount    money.Amount
	}{
		{"undershoot (the old 20000 against 30000)", 130_000, "in", 20_000},
		{"overshoot", 130_000, "in", 40_000},
		{"off by one rupiah", 130_000, "in", 29_999},
		{"wrong direction on a surplus, which would double the gap", 130_000, "out", 30_000},
		{"wrong direction on a shortfall", 70_000, "in", 30_000},
		{"a plug when nothing differs", 100_000, "in", 5_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, f, q := remainderFixture(t)
			ctx := context.Background()

			_, err := takeCash(l, f, tc.actual, "adjusted", fixOf(f, tc.direction, tc.amount))
			if !errors.Is(err, ErrReconciliationPlugMismatch) {
				t.Fatalf("TakeReconciliation() = %v, want ErrReconciliationPlugMismatch", err)
			}

			// Nothing was written: no snapshot, and the ledger is as it was.
			recs, err := q.ListReconciliationsByFund(ctx, f.fundID)
			if err != nil || len(recs) != 0 {
				t.Errorf("reconciliations = %+v, %v, want none", recs, err)
			}
			bal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
			if err != nil || bal != 100_000 {
				t.Errorf("AccountBalance() = %d, %v, want 100000 untouched", bal, err)
			}
		})
	}
}

func TestTakeReconciliationAdjustedPlugRefusalLeavesOtherLinesUnposted(t *testing.T) {
	t.Parallel()
	l, f, q := remainderFixture(t)
	ctx := context.Background()
	postOpeningBalance(t, l, f.fundID, f.bankID, f.mainID, 50_000, "2026-08-01")

	// The bank line is a valid plug, the cash line is not: the snapshot is
	// one call, so the bank's fix must not survive the cash line's refusal.
	_, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: f.bankID, ActualAmount: 40_000, Resolution: "adjusted", Fix: fixOf(f, "out", 10_000)},
			{AccountID: f.cashID, ActualAmount: 130_000, Resolution: "adjusted", Fix: fixOf(f, "in", 20_000)},
		},
	})
	if !errors.Is(err, ErrReconciliationPlugMismatch) {
		t.Fatalf("TakeReconciliation() = %v, want ErrReconciliationPlugMismatch", err)
	}
	bal, err := l.AccountBalance(ctx, f.fundID, f.bankID)
	if err != nil || bal != 50_000 {
		t.Errorf("bank AccountBalance() = %d, %v, want 50000 - the valid plug rolled back too", bal, err)
	}
	if recs, err := q.ListReconciliationsByFund(ctx, f.fundID); err != nil || len(recs) != 0 {
		t.Errorf("reconciliations = %+v, %v, want none", recs, err)
	}
}

func TestTakeReconciliationEntryAddedExactEntryClosesTheLine(t *testing.T) {
	t.Parallel()
	l, f, q := remainderFixture(t)
	ctx := context.Background()

	rec, err := takeCash(l, f, 130_000, "entry_added", fixOf(f, "in", 30_000))
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}
	line := cashLine(t, q, f, rec)
	if line.DifferenceAmount != 30_000 || line.RemainderAmount != 0 {
		t.Errorf("line difference/remainder = %d/%d, want 30000/0", line.DifferenceAmount, line.RemainderAmount)
	}
	if open, err := q.ListOpenReconciliationLinesByFund(ctx, f.fundID); err != nil || len(open) != 0 {
		t.Errorf("open lines = %+v, %v, want none", open, err)
	}
	r := monthlyReport(t, l, ReportParams{FundID: f.fundID})
	if r.Reconciliation == nil || !r.Reconciliation.Matched || r.Reconciliation.Difference != 0 {
		t.Errorf("Reconciliation = %+v, want matched", r.Reconciliation)
	}
}

func TestTakeReconciliationEntryAddedPartialEntryKeepsTheRemainderOpen(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		actual        money.Amount
		direction     string
		amount        money.Amount
		wantDiff      int64
		wantRemainder int64
	}{
		{"surplus, one of the missing entries found", 130_000, "in", 20_000, 30_000, 10_000},
		{"shortfall, one of the missing entries found", 70_000, "out", 10_000, -30_000, -20_000},
		{"overshoot flips the remainder's sign", 130_000, "in", 50_000, 30_000, -20_000},
		{"wrong direction only grows the remainder", 130_000, "out", 10_000, 30_000, 40_000},
		{"one rupiah short of the gap", 130_000, "in", 29_999, 30_000, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, f, q := remainderFixture(t)
			ctx := context.Background()

			rec, err := takeCash(l, f, tc.actual, "entry_added", fixOf(f, tc.direction, tc.amount))
			if err != nil {
				t.Fatalf("TakeReconciliation() = %v, want no error - an entry may close only part of the gap", err)
			}

			// The frozen difference is the gap that was found, untouched.
			line := cashLine(t, q, f, rec)
			if line.DifferenceAmount != tc.wantDiff {
				t.Errorf("DifferenceAmount = %d, want %d", line.DifferenceAmount, tc.wantDiff)
			}
			if line.RemainderAmount != tc.wantRemainder {
				t.Errorf("RemainderAmount = %d, want %d", line.RemainderAmount, tc.wantRemainder)
			}

			// The open-lines list (Beranda) carries it as open.
			open, err := q.ListOpenReconciliationLinesByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListOpenReconciliationLinesByFund() = %v, want no error", err)
			}
			if len(open) != 1 || open[0].AccountID != f.cashID || open[0].RemainderAmount != tc.wantRemainder {
				t.Fatalf("open lines = %+v, want the cash line with remainder %d", open, tc.wantRemainder)
			}

			// Cek kas's list row reads selisih for the remainder.
			page, err := q.ListReconciliationsPage(ctx, store.ListReconciliationsPageParams{FundID: f.fundID, PageLimit: 10})
			if err != nil || len(page) != 1 {
				t.Fatalf("ListReconciliationsPage() = %+v, %v, want one row", page, err)
			}
			wantAbs := tc.wantRemainder
			if wantAbs < 0 {
				wantAbs = -wantAbs
			}
			if page[0].OpenDifferenceAmount != wantAbs {
				t.Errorf("OpenDifferenceAmount = %d, want %d", page[0].OpenDifferenceAmount, wantAbs)
			}

			// The public report reads selisih for the remainder, not the
			// difference that was found.
			r := monthlyReport(t, l, ReportParams{FundID: f.fundID})
			if r.Reconciliation == nil || r.Reconciliation.Matched || r.Reconciliation.Difference.Int64() != wantAbs {
				t.Errorf("Reconciliation = %+v, want selisih %d", r.Reconciliation, wantAbs)
			}
		})
	}
}

// A partial entry stays open only until the location is counted again: the
// next count of the same location supersedes it, exactly as it does a
// left_open line.
func TestTakeReconciliationPartialEntryIsSupersededByTheNextCount(t *testing.T) {
	t.Parallel()
	l, f, q := remainderFixture(t)
	ctx := context.Background()

	if _, err := takeCash(l, f, 130_000, "entry_added", fixOf(f, "in", 20_000)); err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}
	// The ledger now holds 120,000; the second count finds 130,000 and plugs
	// the rest exactly.
	if _, err := takeCash(l, f, 130_000, "adjusted", fixOf(f, "in", 10_000)); err != nil {
		t.Fatalf("TakeReconciliation(second) = %v, want no error", err)
	}
	if open, err := q.ListOpenReconciliationLinesByFund(ctx, f.fundID); err != nil || len(open) != 0 {
		t.Errorf("open lines = %+v, %v, want none after the next count", open, err)
	}
	r := monthlyReport(t, l, ReportParams{FundID: f.fundID})
	if r.Reconciliation == nil || !r.Reconciliation.Matched {
		t.Errorf("Reconciliation = %+v, want matched", r.Reconciliation)
	}
}

// Lines written before #498 carry no link for entry_added (their remainder
// was never recorded) and may carry a short plug for adjusted. The first
// reads closed, as it always did; the second now reads open for what its
// plug missed, because the ledger can prove it.
func TestReconciliationRemainderOfLinesWrittenBeforeTheRule(t *testing.T) {
	t.Parallel()
	l, f, q := remainderFixture(t)
	ctx := context.Background()

	fix := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 20_000, "2026-08-12", nil)
	rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{FundID: f.fundID, PerformedAt: 1_000, CreatedAt: 1_000})
	if err != nil {
		t.Fatalf("CreateReconciliation() = %v, want no error", err)
	}
	mk := func(accountID, recorded, actual int64, resolution string, link *int64) {
		t.Helper()
		if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
			FundID: f.fundID, ReconciliationID: rec.ID, AccountID: accountID,
			RecordedAmount: recorded, ActualAmount: actual, DifferenceAmount: actual - recorded,
			Resolution: resolution, AdjustmentTransactionID: link,
		}); err != nil {
			t.Fatalf("CreateReconciliationLine() = %v, want no error", err)
		}
	}
	mk(f.cashID, 100_000, 130_000, "adjusted", &fix.ID) // a 20,000 plug against a 30,000 gap
	mk(f.bankID, 0, 7_000, "entry_added", nil)          // no link: remainder unknowable

	lines, err := q.ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	if got := lineFor(t, lines, f.cashID).RemainderAmount; got != 10_000 {
		t.Errorf("short legacy plug RemainderAmount = %d, want 10000", got)
	}
	if got := lineFor(t, lines, f.bankID).RemainderAmount; got != 0 {
		t.Errorf("unlinked legacy entry_added RemainderAmount = %d, want 0", got)
	}
}
