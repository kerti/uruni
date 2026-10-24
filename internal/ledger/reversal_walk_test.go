package ledger

import (
	"context"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ADR-042 puts a reversal on its original's own line of the walk, in the month it
// is dated: the reversal of income is a minus on Total masuk, the reversal of
// spending a minus on Total keluar. These tests hold the footing (ADR-038: start
// plus the lines is the end, to the rupiah) over a ledger holding every shape a
// reversal can take, for the whole fund and for each pos.

type reversalWalk struct {
	fundID int64
	f      fixture

	// Sunatan and Arisan are two open envelopes (pos).
	sunatanID, arisanID int64

	// The three originals a cek kas counted before they were reversed, in August,
	// at the injected clock.
	countedOut, countedIn, countedCorrected          store.Transaction
	reversalOfOut, reversalOfIn, reversalOfCorrected store.Transaction
}

func newReversalWalk(t *testing.T, l *Ledger) reversalWalk {
	t.Helper()
	ctx := context.Background()
	f := newFixture(t, l)
	w := reversalWalk{fundID: f.fundID, f: f}

	sunatan := openTestIncidental(t, l, f.fundID, "Sunatan", "2026-07-01")
	arisan := openTestIncidental(t, l, f.fundID, "Arisan", "2026-07-01")
	w.sunatanID, w.arisanID = sunatan.PurposeID, arisan.PurposeID

	// July. Four rows reversed before any cek kas, so each reversal sits on its
	// original's day in its original's month and nets there: a reversed out, a
	// reversed in, a dues payment and a named contribution.
	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 500_000, "2026-07-01")
	reverseAt(t, l, f.fundID, postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 40_000, "2026-07-05", nil).ID, onTheDay("2026-07-05"))
	reverseAt(t, l, f.fundID, postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 30_000, "2026-07-07", nil).ID, onTheDay("2026-07-07"))
	paid, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: f.memberID, OccurredOn: "2026-07-10",
		Periods: []PeriodAmount{{DuesPeriod: "2026-07", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	reverseAt(t, l, f.fundID, paid[0].ID, onTheDay("2026-07-10"))
	gift, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: w.sunatanID, Direction: "in",
		Amount: 15_000, OccurredOn: "2026-07-12", MemberID: &f.memberID,
	})
	if err != nil {
		t.Fatalf("PostTransaction(named contribution) = %v, want no error", err)
	}
	reverseAt(t, l, f.fundID, gift.ID, onTheDay("2026-07-12"))
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 10_000, "2026-07-20", nil)

	// Three more rows, left standing until a cek kas has counted them.
	w.countedOut = postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 12_000, "2026-07-25", nil)
	w.countedIn = postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 9_000, "2026-07-26", nil)
	w.countedCorrected = postEntry(t, l, f.fundID, f.cashID, w.sunatanID, "in", 20_000, "2026-07-27", nil)
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: w.countedCorrected.ID, PurposeID: w.arisanID}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}
	countEverything(t, l, f.fundID, f.cashID, f.bankID)

	// August: the three are reversed today, 10 August, a later month than they
	// were posted in.
	august := onTheDay("2026-08-10")
	w.reversalOfOut = reverseAt(t, l, f.fundID, w.countedOut.ID, august)
	w.reversalOfIn = reverseAt(t, l, f.fundID, w.countedIn.ID, august)
	w.reversalOfCorrected = reverseAt(t, l, f.fundID, w.countedCorrected.ID, august)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 5_000, "2026-08-15", nil)
	return w
}

func TestReversalWalkFootsEveryMonthForTheFund(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newReversalWalk(t, l)

	// Hand-worked.
	//   Jul  500000 openings + (10000 + 9000 + 20000) in - 12000 out = 527000.
	//        The four pairs reversed on their own day net to nothing on either
	//        line, so neither is negative.
	//   Aug  527000 + (-9000 - 20000 + 5000) in - (-12000) out       = 515000.
	//        Reversing spending lowers Total keluar, below zero here; reversing
	//        income lowers Total masuk.
	months := []struct {
		month string
		want  ReportWalk
	}{
		{"2026-06", ReportWalk{StartOn: "2026-05-31", EndOn: "2026-06-30"}},
		{"2026-07", ReportWalk{StartOn: "2026-06-30", EndOn: "2026-07-31", Openings: 500_000, In: 39_000, Out: 12_000, End: 527_000}},
		{"2026-08", ReportWalk{StartOn: "2026-07-31", EndOn: "2026-08-31", Start: 527_000, In: -24_000, Out: -12_000, End: 515_000}},
	}
	for _, m := range months {
		r := monthlyReport(t, l, ReportParams{FundID: w.fundID, Month: m.month})
		want := m.want
		want.Full = true
		if r.Walk != want {
			t.Errorf("%s: Walk = %+v, want %+v", m.month, r.Walk, want)
		}
		if got := walkFoots(t, r.Walk); got != r.Walk.End {
			t.Errorf("%s: Start + Openings + In - Out + Adjustments = %d, End = %d", m.month, got, r.Walk.End)
		}
		if end := ledgerSumThrough(t, l, w.fundID, r.Walk.EndOn); end != r.Walk.End {
			t.Errorf("%s: End = %d, but the ledger through %s sums to %d", m.month, r.Walk.End, r.Walk.EndOn, end)
		}
		if start := ledgerSumThrough(t, l, w.fundID, r.Walk.StartOn); start != r.Walk.Start {
			t.Errorf("%s: Start = %d, but the ledger through %s sums to %d", m.month, r.Walk.Start, r.Walk.StartOn, start)
		}
	}

	// The month of an uncounted reversal never goes negative: July holds a reversed
	// out, a reversed in, a dues payment and a contribution, each on its own day.
	july := monthlyReport(t, l, ReportParams{FundID: w.fundID, Month: "2026-07"})
	if july.Walk.In < 0 || july.Walk.Out < 0 {
		t.Errorf("July In/Out = %d/%d, want neither negative: an uncounted reversal nets in its original's month", july.Walk.In, july.Walk.Out)
	}
	// Beranda reads the same walk (Arus kas bulan ini), and the negative Keluar
	// reaches it as it is.
	in, out, err := l.at(onTheDay("2026-08-20")).MonthInOut(context.Background(), w.fundID)
	if err != nil {
		t.Fatalf("MonthInOut() = %v, want no error", err)
	}
	if in != -24_000 || out != -12_000 {
		t.Errorf("MonthInOut(August) = %d/%d, want -24000/-12000", in, out)
	}
}

// The same months for each pos: Kas Utama, the two envelopes and Titipan. A pos's
// Start and End are its bounded balances; the corrected original is walked on both
// pos, its reversal on the pos it was corrected to, in another month.
func TestReversalWalkFootsEveryMonthForEveryPurpose(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newReversalWalk(t, l)

	// Hand-worked.
	//   Kas Utama  Jul 500000 + (10000 + 9000) in - 12000 out = 507000
	//              Aug 507000 + (-9000 + 5000) in + 12000     = 515000
	//   Sunatan    Jul the contribution (+20000) corrected away (-20000 Dipindah): 0;
	//              the named gift reversed on its own day nets to nothing
	//   Arisan     Jul +20000 Dipindah in: 20000
	//              Aug the reversal lands here, -20000 in: 0
	type W = ReportWalk
	cases := []struct {
		name    string
		purpose int64
		months  map[string]W
	}{
		{"Kas Utama", w.f.mainID, map[string]W{
			"2026-07": {Openings: 500_000, In: 19_000, Out: 12_000, End: 507_000},
			"2026-08": {Start: 507_000, In: -4_000, Out: -12_000, End: 515_000},
		}},
		{"Sunatan", w.sunatanID, map[string]W{
			"2026-07": {In: 20_000, Moved: -20_000},
			"2026-08": {},
		}},
		{"Arisan", w.arisanID, map[string]W{
			"2026-07": {Moved: 20_000, End: 20_000},
			"2026-08": {Start: 20_000, In: -20_000, End: 0},
		}},
		{"Titipan", w.f.passID, map[string]W{"2026-07": {}, "2026-08": {}}},
	}
	var fundStart, fundEnd = map[string]money.Amount{}, map[string]money.Amount{}
	for _, tc := range cases {
		for _, month := range []string{"2026-07", "2026-08"} {
			id := tc.purpose
			r := monthlyReport(t, l, ReportParams{FundID: w.fundID, Month: month, PurposeID: &id})
			want := tc.months[month]
			want.Full = true
			want.StartOn, want.EndOn = r.Walk.StartOn, r.Walk.EndOn
			if r.Walk != want {
				t.Errorf("%s %s: Walk = %+v, want %+v", tc.name, month, r.Walk, want)
			}
			if got := walkFootsMoved(t, r.Walk); got != r.Walk.End {
				t.Errorf("%s %s: Start + Openings + In - Out + Adjustments + Moved = %d, End = %d", tc.name, month, got, r.Walk.End)
			}
			if start := purposeLedgerSum(t, l, w.fundID, id, r.Walk.StartOn); start != r.Walk.Start {
				t.Errorf("%s %s: Start = %d, but the ledger through %s sums to %d for this pos", tc.name, month, r.Walk.Start, r.Walk.StartOn, start)
			}
			if end := purposeLedgerSum(t, l, w.fundID, id, r.Walk.EndOn); end != r.Walk.End {
				t.Errorf("%s %s: End = %d, but the ledger through %s sums to %d for this pos", tc.name, month, r.Walk.End, r.Walk.EndOn, end)
			}
			if moved, _ := purposeMovedInMonth(t, l, w.fundID, id, month); moved != r.Walk.Moved {
				t.Errorf("%s %s: Moved = %d, but the raw legs sum to %d", tc.name, month, r.Walk.Moved, moved)
			}
			fundStart[month] += r.Walk.Start
			fundEnd[month] += r.Walk.End
		}
	}
	// The pos together are the fund.
	for _, month := range []string{"2026-07", "2026-08"} {
		fund := monthlyReport(t, l, ReportParams{FundID: w.fundID, Month: month})
		if fundStart[month] != fund.Walk.Start || fundEnd[month] != fund.Walk.End {
			t.Errorf("%s: the pos sum to %d -> %d, the fund walks %d -> %d", month, fundStart[month], fundEnd[month], fund.Walk.Start, fund.Walk.End)
		}
	}
}

// A reversal filters as its own line, so the list, the filter and the totals agree
// (ADR-038): the reversal of an out is under Uang keluar and lowers Total keluar,
// the reversal of an in is under Uang masuk and lowers Total masuk. The rows keep
// the stored direction, the balance's view.
func TestReversalFiltersAsItsOwnLine(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	w := newReversalWalk(t, l)

	reversalsIn := func(r Report) (n int, sawOutReversal bool) {
		for _, e := range entries(r.Rows) {
			if e.IsReversal {
				n++
				if e.Direction == "in" {
					sawOutReversal = true
				}
			}
		}
		return n, sawOutReversal
	}

	out := monthlyReport(t, l, ReportParams{FundID: w.fundID, Month: "2026-08", Direction: "out"})
	if n, sawIn := reversalsIn(out); n != 1 || !sawIn {
		t.Errorf("under Uang keluar: %d reversals (the reversal of the out: %v), want exactly that one", n, sawIn)
	}
	if out.Walk.Out != -12_000 {
		t.Errorf("Total keluar under the filter = %d, want -12000", out.Walk.Out)
	}
	for _, e := range entries(out.Rows) {
		if e.IsReversal && e.Line != ReportLineOut {
			t.Errorf("the reversal of an out lands on %q, want %q", e.Line, ReportLineOut)
		}
	}

	in := monthlyReport(t, l, ReportParams{FundID: w.fundID, Month: "2026-08", Direction: "in"})
	if n, sawIn := reversalsIn(in); n != 2 || sawIn {
		t.Errorf("under Uang masuk: %d reversals (one of them an out's: %v), want the two reversals of ins", n, sawIn)
	}
	if in.Walk.In != -24_000 {
		t.Errorf("Total masuk under the filter = %d, want -24000", in.Walk.In)
	}
}

func TestClassifyReportRowPutsAReversalOnItsOriginalsLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		direction string
		reverses  bool
		want      ReportLine
	}{
		{"a reversal going out reverses income", "out", true, ReportLineIn},
		{"a reversal going in reverses spending", "in", true, ReportLineOut},
		{"an adjustment going in is a Penyesuaian", "in", false, ReportLineAdjustment},
		{"an adjustment going out is a Penyesuaian", "out", false, ReportLineAdjustment},
	} {
		got, err := classifyReportRow("adjustment", tc.direction, tc.reverses)
		if err != nil || got != tc.want {
			t.Errorf("%s: classifyReportRow() = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestReportWalkAddReadsAReversalOfSpendingAsAMinusOnOut(t *testing.T) {
	t.Parallel()
	var w ReportWalk
	for _, e := range []ReportEntry{
		{Line: ReportLineOut, Direction: "out", Amount: 40_000},
		{Line: ReportLineOut, Direction: "in", Amount: 40_000, IsReversal: true},
		{Line: ReportLineOut, Direction: "out", Amount: 7_000},
		{Line: ReportLineIn, Direction: "in", Amount: 5_000},
		{Line: ReportLineIn, Direction: "out", Amount: 8_000, IsReversal: true},
	} {
		if err := w.add(e); err != nil {
			t.Fatalf("add(%+v) = %v, want no error", e, err)
		}
	}
	if w.Out != 7_000 || w.In != -3_000 {
		t.Errorf("Out/In = %d/%d, want 7000/-3000", w.Out, w.In)
	}
}
