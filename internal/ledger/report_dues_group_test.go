package ledger

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ADR-048 in the public report: a dues payment is ONE listed row, and so is a
// reversal group, while every figure the report adds up stays what it was when a
// payment was a row a month. The list folds; the walk, the balance and the dues
// section never do.

func TestDuesPaymentRefusal(t *testing.T) {
	t.Parallel()
	standing := ReversalFacts{Kind: TransactionKindDues, OccurredOn: "2026-08-12"}
	reversed := ReversalFacts{Kind: TransactionKindDues, OccurredOn: "2026-08-12", Reversed: true}
	counted := ReversalFacts{Kind: TransactionKindDues, OccurredOn: "2026-08-12", Counted: true}
	closed := ReversalFacts{Kind: TransactionKindDues, OccurredOn: "2026-08-12", EffectiveClosed: true}
	const today = "2026-09-01"

	for _, tc := range []struct {
		name   string
		months []ReversalFacts
		today  string
		want   error
	}{
		{"every month standing", []ReversalFacts{standing, standing, standing}, today, nil},
		{"one month standing is enough (data from before ADR-048)", []ReversalFacts{reversed, standing, reversed}, today, nil},
		{"every month reversed", []ReversalFacts{reversed, reversed}, today, ErrAlreadyReversed},
		{"no months at all", nil, today, ErrAlreadyReversed},
		{"a counted month dated today or earlier is fine", []ReversalFacts{counted, standing}, today, nil},
		{"a counted month dated after today refuses the whole act", []ReversalFacts{standing, counted}, "2026-08-01", ErrReversalBeforeOriginal},
		{"a month under a closed envelope refuses the whole act", []ReversalFacts{standing, closed}, today, ErrIncidentalClosed},
		{"a reversed month is skipped, not refused, even if it would be", []ReversalFacts{{Kind: TransactionKindDues, Reversed: true, EffectiveClosed: true}, standing}, today, nil},
	} {
		if got := DuesPaymentRefusal(tc.months, tc.today); !errors.Is(got, tc.want) {
			t.Errorf("%s: DuesPaymentRefusal() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestReportListsADuesPaymentAsOneRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)

	// Aug 12: a three-month payment (given out of order), a second payment by the
	// same member on the same day for one month, and a reversal of the first.
	first := payDues(t, l, f, "2026-08-12", 25_000, "2026-08", "2026-06", "2026-07")
	payDues(t, l, f, "2026-08-12", 30_000, "2026-09")
	reversed, err := l.at(onTheDay("2026-08-12")).ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, DuesPaymentID: *first[0].DuesPaymentID})
	if err != nil {
		t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
	}
	if len(reversed) != 3 {
		t.Fatalf("ReverseDuesPayment() posted %d rows, want 3", len(reversed))
	}

	report := monthlyReportAt(t, l, reportNow, ReportParams{FundID: f.fundID, Month: "2026-08"})
	got := entries(report.Rows)
	if len(got) != 3 {
		t.Fatalf("report holds %d rows, want 3: the payment, the other payment, the reversal group (rows: %+v)", len(got), got)
	}

	var payment, other, reversal *ReportEntry
	for i := range got {
		e := &got[i]
		switch {
		case e.IsReversal:
			reversal = e
		case len(e.DuesPeriods) == 3:
			payment = e
		default:
			other = e
		}
	}
	if payment == nil || other == nil || reversal == nil {
		t.Fatalf("rows are not one payment, one other payment and one reversal group: %+v", got)
	}

	if payment.Amount != 75_000 || !payment.Reversed || payment.PartlyReversed {
		t.Errorf("payment = amount %d reversed %v partly %v, want 75000 true false", payment.Amount, payment.Reversed, payment.PartlyReversed)
	}
	if want := []string{"2026-06", "2026-07", "2026-08"}; !equalStrings(payment.DuesPeriods, want) || payment.DuesPeriod == nil || *payment.DuesPeriod != "2026-06" {
		t.Errorf("payment periods = %v (period %v), want %v and the earliest month", payment.DuesPeriods, payment.DuesPeriod, want)
	}
	if reversal.Amount != 75_000 || !equalStrings(reversal.DuesPeriods, payment.DuesPeriods) || reversal.Reversed {
		t.Errorf("reversal group = amount %d periods %v reversed %v, want 75000 %v false", reversal.Amount, reversal.DuesPeriods, reversal.Reversed, payment.DuesPeriods)
	}
	if other.Amount != 30_000 || len(other.DuesPeriods) != 1 || other.Reversed {
		t.Errorf("other payment = amount %d periods %v reversed %v, want 30000, one month, standing", other.Amount, other.DuesPeriods, other.Reversed)
	}

	// The walk counts every row: the same figures as when each month was a row.
	// In = 3*25.000 + 30.000 - 3*25.000 (the reversal takes its amount off In).
	assertInOut(t, report.Walk, 30_000, 0)
	if report.Walk.Start != 0 || report.Walk.End != 30_000 {
		t.Errorf("Walk Start/End = %d/%d, want 0/30000", report.Walk.Start, report.Walk.End)
	}
	if balance, err := l.FundBalance(ctx, f.fundID); err != nil || balance != 30_000 || report.Balance != balance {
		t.Errorf("FundBalance() = %d, %v and report balance %d, want both 30000", balance, err, report.Balance)
	}
	// The month summary Riwayat prints is the same derivation.
	summary, err := l.at(reportNow).MonthWalk(ctx, ReportParams{FundID: f.fundID, Month: "2026-08"})
	if err != nil {
		t.Fatalf("MonthWalk() = %v, want no error", err)
	}
	if summary != report.Walk {
		t.Errorf("MonthWalk() = %+v, want the report's walk %+v", summary, report.Walk)
	}
}

// A payment reversed in part (only data from before ADR-048 holds one) reads
// honestly: not reversed, partly reversed, and its reversal group is the single
// month taken back.
func TestReportListsAPartlyReversedPaymentHonestly(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	rows := payDues(t, l, f, "2026-08-12", 25_000, "2026-06", "2026-07", "2026-08")
	var july store.Transaction
	for _, r := range rows {
		if r.DuesPeriod != nil && *r.DuesPeriod == "2026-07" {
			july = r
		}
	}
	postReversalOn(t, l, f.fundID, july.ID, "2026-08-12")

	report := monthlyReportAt(t, l, reportNow, ReportParams{FundID: f.fundID, Month: "2026-08"})
	got := entries(report.Rows)
	if len(got) != 2 {
		t.Fatalf("report holds %d rows, want 2: the payment and the one reversal (rows: %+v)", len(got), got)
	}
	for _, e := range got {
		if e.IsReversal {
			if e.Amount != 25_000 || !equalStrings(e.DuesPeriods, []string{"2026-07"}) {
				t.Errorf("reversal = amount %d periods %v, want 25000 and July", e.Amount, e.DuesPeriods)
			}
			continue
		}
		if e.Amount != 75_000 || e.Reversed || !e.PartlyReversed {
			t.Errorf("payment = amount %d reversed %v partly %v, want 75000 false true", e.Amount, e.Reversed, e.PartlyReversed)
		}
	}
	assertInOut(t, report.Walk, 50_000, 0)
}

// The fold keys on the payment, never on likeness: two same-day, same-member,
// same-note payments are two rows, and a filter keeps a group whole or drops it.
func TestReportKeepsTwoLookAlikePaymentsApartAndFiltersGroupsWhole(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	payDues(t, l, f, "2026-08-12", 25_000, "2026-07", "2026-08")
	payDues(t, l, f, "2026-08-12", 25_000, "2026-09", "2026-10")
	postEntry(t, l, f.fundID, f.cashID, f.mainID, DirectionOut, 10_000, "2026-08-12", nil)

	all := monthlyReportAt(t, l, reportNow, ReportParams{FundID: f.fundID, Month: "2026-08"})
	var duesRows []ReportEntry
	for _, e := range entries(all.Rows) {
		if e.Kind == TransactionKindDues {
			duesRows = append(duesRows, e)
		}
	}
	if len(duesRows) != 2 {
		t.Fatalf("report holds %d payment rows, want 2 (rows: %+v)", len(duesRows), duesRows)
	}
	for _, e := range duesRows {
		if e.Amount != 50_000 || len(e.DuesPeriods) != 2 {
			t.Errorf("payment = amount %d periods %v, want 50000 and two months", e.Amount, e.DuesPeriods)
		}
	}
	assertInOut(t, all.Walk, 100_000, 10_000)

	in := monthlyReportAt(t, l, reportNow, ReportParams{FundID: f.fundID, Month: "2026-08", Direction: ReportDirectionIn})
	if got := entries(in.Rows); len(got) != 2 || got[0].Amount != 50_000 || got[1].Amount != 50_000 {
		t.Errorf("direction=in rows = %+v, want both payments, whole", got)
	}
	mine := monthlyReportAt(t, l, reportNow, ReportParams{FundID: f.fundID, Month: "2026-08", MemberID: &f.memberID})
	if got := entries(mine.Rows); len(got) != 2 {
		t.Errorf("member filter rows = %+v, want both payments", got)
	}
	assertInOut(t, mine.Walk, 100_000, 0)
	out := monthlyReportAt(t, l, reportNow, ReportParams{FundID: f.fundID, Month: "2026-08", Direction: ReportDirectionOut})
	if got := entries(out.Rows); len(got) != 1 || got[0].Kind == TransactionKindDues {
		t.Errorf("direction=out rows = %+v, want only the expense", got)
	}
}

// The fold must not move one rupiah: a payment's rows summed by the walk equal
// the rows summed by the ledger, to the integer, whatever the list shows.
func TestReportWalkIgnoresTheFold(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)

	payDues(t, l, f, "2026-08-03", 33_333, "2026-05", "2026-06", "2026-07")
	payDues(t, l, f, "2026-08-04", 1, "2026-08")
	report := monthlyReportAt(t, l, reportNow, ReportParams{FundID: f.fundID, Month: "2026-08"})

	var listed money.Amount
	for _, e := range entries(report.Rows) {
		listed += e.Amount
	}
	if want := money.Amount(3*33_333 + 1); report.Walk.In != want || listed != want || report.Balance != want {
		t.Errorf("In %d, listed %d, balance %d, want all %d", report.Walk.In, listed, report.Balance, want)
	}
}

func equalStrings(a, b []string) bool { return slices.Equal(a, b) }
