package ledger

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// The report's date range (ADR-049): whole days, inclusive, in the fund's zone,
// assembled by the same code as the month, so the trust claims of ADR-037 and
// ADR-038 hold for it too: the walk foots to the rupiah, both ends are ledger
// sums, and a range that reaches today reads like the running month.

func rangeReport(t *testing.T, l *Ledger, fundID int64, from, to string) Report {
	t.Helper()
	return monthlyReport(t, l, ReportParams{FundID: fundID, From: from, To: to})
}

// dayBefore is the ISO day before day.
func dayBefore(t *testing.T, day string) string {
	t.Helper()
	d, err := time.Parse(time.DateOnly, day)
	if err != nil {
		t.Fatalf("parsing %q: %v", day, err)
	}
	return d.AddDate(0, 0, -1).Format(time.DateOnly)
}

// A range's rows are those dated in [From, To], both ends included and the day
// beyond either left out; its walk starts on the balance at the end of the day
// before From and ends on the balance at the end of To, each a ledger sum by a
// route of its own, and foots exactly.
func TestRangeIncludesBothEndDaysAndExcludesTheDaysBeyond(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newMonthEndScenario(t, l)

	// The scenario's rows: 31 Aug in 100000; 1 Sep in 50000; 2 Sep a gift of
	// 5000 and a settlement of 7000; 15 Aug a gift of 20000 (before every range
	// here); the roll on 5 Sep is a move, outside the totals.
	tests := []struct {
		name     string
		from, to string
		in, out  money.Amount
		entries  int
	}{
		{"crossing the month boundary, both ends exact", "2026-08-31", "2026-09-01", 150_000, 0, 2},
		{"ends on the first of the month", "2026-09-01", "2026-09-01", 50_000, 0, 1},
		{"starts a day early, ends on the last of the month", "2026-08-30", "2026-08-31", 100_000, 0, 1},
		{"a single day", "2026-08-31", "2026-08-31", 100_000, 0, 1},
		{"a day with an in and an out", "2026-09-02", "2026-09-02", 5_000, 7_000, 2},
		{"two days, the day before and after both out", "2026-09-01", "2026-09-02", 55_000, 7_000, 3},
		{"a quiet stretch", "2026-08-16", "2026-08-30", 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := rangeReport(t, l, s.f.fundID, tt.from, tt.to)

			if !r.IsRange || r.From != tt.from || r.To != tt.to || r.AsOf != tt.to || r.Running {
				t.Errorf("IsRange, From, To, AsOf, Running = %v, %q, %q, %q, %v; want true, %q, %q, %q, false",
					r.IsRange, r.From, r.To, r.AsOf, r.Running, tt.from, tt.to, tt.to)
			}
			if want := tt.to[:7]; r.Month != want {
				t.Errorf("Month = %q, want the month that holds To, %q", r.Month, want)
			}
			assertInOut(t, r.Walk, tt.in, tt.out)
			if got := len(entries(r.Rows)); got != tt.entries {
				t.Errorf("entries listed = %d, want %d", got, tt.entries)
			}
			for _, row := range r.Rows {
				if row.Date < tt.from || row.Date > tt.to {
					t.Errorf("row dated %s is outside [%s, %s]", row.Date, tt.from, tt.to)
				}
			}

			w := r.Walk
			if !w.Full || w.StartOn != dayBefore(t, tt.from) || w.EndOn != tt.to {
				t.Errorf("Full, StartOn, EndOn = %v, %q, %q; want true, %q, %q", w.Full, w.StartOn, w.EndOn, dayBefore(t, tt.from), tt.to)
			}
			if want := ledgerSumThrough(t, l, s.f.fundID, w.StartOn); w.Start != want {
				t.Errorf("Start = %d, but the ledger through %s sums to %d", w.Start, w.StartOn, want)
			}
			if want := ledgerSumThrough(t, l, s.f.fundID, w.EndOn); w.End != want {
				t.Errorf("End = %d, but the ledger through %s sums to %d", w.End, w.EndOn, want)
			}
			if got := walkFoots(t, w); got != w.End {
				t.Errorf("Start + Openings + In - Out + Adjustments = %d, End = %d", got, w.End)
			}
			// The header reads as of To, the same balance the walk ends on.
			if r.Balance != w.End {
				t.Errorf("Balance = %d, want the walk's End %d", r.Balance, w.End)
			}
		})
	}
}

// A range holding exactly one calendar month is that month's report, figure for
// figure: one assembly serves both.
func TestRangeOfAWholeMonthReadsLikeTheMonth(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	for _, tc := range []struct{ month, from, to string }{
		{"2026-08", "2026-08-01", "2026-08-31"},
		{"2026-09", "2026-09-01", "2026-09-30"},
	} {
		t.Run(tc.month, func(t *testing.T) {
			month := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: tc.month})
			rng := rangeReport(t, l, s.fundID, tc.from, tc.to)

			if rng.Walk != month.Walk {
				t.Errorf("Walk = %+v, want the month's %+v", rng.Walk, month.Walk)
			}
			if !reflect.DeepEqual(rng.Rows, month.Rows) {
				t.Errorf("Rows differ from the month's:\n%+v\nvs\n%+v", rng.Rows, month.Rows)
			}
			if rng.Balance != month.Balance || rng.OwedToMembers != month.OwedToMembers || rng.AsOf != month.AsOf || rng.Running != month.Running {
				t.Errorf("header = %d, %d, %q, %v; want the month's %d, %d, %q, %v",
					rng.Balance, rng.OwedToMembers, rng.AsOf, rng.Running, month.Balance, month.OwedToMembers, month.AsOf, month.Running)
			}
			if !reflect.DeepEqual(rng.Reconciliation, month.Reconciliation) || !reflect.DeepEqual(rng.PurposeBalances, month.PurposeBalances) ||
				!reflect.DeepEqual(rng.Envelopes, month.Envelopes) || !reflect.DeepEqual(rng.Dues, month.Dues) || !reflect.DeepEqual(rng.DuesGrid, month.DuesGrid) {
				t.Error("reconciliation, Saldo per pos, envelopes or dues differ from the month's")
			}
		})
	}
}

// Hand-worked on the walk scenario, so the test is not the code agreeing with
// itself (the month balances are in TestMonthlyReportWalkFootsEveryMonth...).
func TestRangeWalkAcrossAMonthBoundaryIsHandWorked(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	// 5-12 Sep: the reversal of a gift (-15000 on Total masuk), 60000 in, 25000 of
	// dues. Before it, August ended on 1_002_000 and 1 and 2 September netted
	// +7000 -3000 of adjustments.
	r := rangeReport(t, l, s.fundID, "2026-09-05", "2026-09-12")
	want := ReportWalk{
		Full: true, StartOn: "2026-09-04", EndOn: "2026-09-12",
		Start: 1_006_000, In: 70_000, End: 1_076_000,
	}
	if r.Walk != want {
		t.Errorf("Walk = %+v, want %+v", r.Walk, want)
	}

	// 28 Aug - 2 Sep: the rows of two months. August's 28th holds only the
	// envelope's roll (a move, nothing in or out of the fund), September's 1st and
	// 2nd the two adjustments: +7000 and -3000.
	r = rangeReport(t, l, s.fundID, "2026-08-28", "2026-09-02")
	want = ReportWalk{
		Full: true, StartOn: "2026-08-27", EndOn: "2026-09-02",
		Start: 1_002_000, Adjustments: 4_000, End: 1_006_000,
	}
	if r.Walk != want {
		t.Errorf("Walk = %+v, want %+v", r.Walk, want)
	}
}

// Under a purpose filter the range is that pos's walk: Start + Openings + In -
// Out + Adjustments + Moved = End, the same equation as the month's, with the
// ends the pos's own balances.
func TestRangeWalkUnderAPurposeFilterFootsWithMoved(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	// Sunatan: 80000 given on 6 Aug; on 10 Aug 20000 spent (and corrected away and
	// back: legs that net to zero); 10000 moved in on 18 Aug; the rest, 70000,
	// rolled into Kas Utama on 28 Aug.
	r := monthlyReport(t, l, ReportParams{FundID: s.fundID, PurposeID: &s.sunatanID, From: "2026-08-10", To: "2026-08-30"})
	w := r.Walk
	want := ReportWalk{
		Full: true, StartOn: "2026-08-09", EndOn: "2026-08-30",
		Start: 80_000, Out: 20_000, Moved: -60_000, End: 0,
	}
	if w != want {
		t.Errorf("Sunatan Walk = %+v, want %+v", w, want)
	}
	if got := walkFoots(t, w) + w.Moved; got != w.End {
		t.Errorf("Start + In - Out + Adjustments + Moved = %d, End = %d", got, w.End)
	}

	// Kas Utama over the same days, crossing into September: the same equation.
	r = monthlyReport(t, l, ReportParams{FundID: s.fundID, PurposeID: &s.mainID, From: "2026-08-10", To: "2026-09-10"})
	w = r.Walk
	if got := walkFoots(t, w) + w.Moved; got != w.End {
		t.Errorf("Kas Utama: Start + Openings + In - Out + Adjustments + Moved = %d, End = %d (Walk %+v)", got, w.End, w)
	}
	startBal, endBal := purposeBalanceThrough(t, l, s.fundID, s.mainID, w.StartOn), purposeBalanceThrough(t, l, s.fundID, s.mainID, w.EndOn)
	if w.Start != startBal || w.End != endBal {
		t.Errorf("Kas Utama Start, End = %d, %d; the ledger sums to %d, %d", w.Start, w.End, startBal, endBal)
	}
}

// purposeBalanceThrough is a pos's balance on or before through, by raw SQL.
func purposeBalanceThrough(t *testing.T, l *Ledger, fundID, purposeID int64, through string) money.Amount {
	t.Helper()
	var sum int64
	err := l.db.QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0)
		FROM "transaction"
		WHERE fund_id = ? AND purpose_id = ? AND occurred_on <= ?`, fundID, purposeID, through).Scan(&sum)
	if err != nil {
		t.Fatalf("summing pos %d through %s = %v, want no error", purposeID, through, err)
	}
	return money.FromDB(sum)
}

// A range ending today is the running period: it reads unbounded, the same
// figures as Beranda and as the running month, rows dated after today included.
func TestRunningRangeReadsLikeTheRunningMonth(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	month := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: "2026-10"})
	for _, to := range []string{"2026-10-02", "2026-12-31"} { // today, and a day the ledger will not clamp
		t.Run("to "+to, func(t *testing.T) {
			r := rangeReport(t, l, s.fundID, "2026-10-01", to)
			if !r.Running || r.AsOf != "2026-10-02" || r.To != "2026-10-02" || r.Month != "2026-10" {
				t.Errorf("Running, AsOf, To, Month = %v, %q, %q, %q; want true, 2026-10-02, 2026-10-02, 2026-10", r.Running, r.AsOf, r.To, r.Month)
			}
			if r.Walk != month.Walk || !reflect.DeepEqual(r.Rows, month.Rows) {
				t.Errorf("walk or rows differ from the running month's:\n%+v\nvs\n%+v", r.Walk, month.Walk)
			}
			if r.Balance != month.Balance || r.OwedToMembers != month.OwedToMembers ||
				!reflect.DeepEqual(r.PurposeBalances, month.PurposeBalances) || !reflect.DeepEqual(r.Reconciliation, month.Reconciliation) {
				t.Error("header figures differ from the running month's")
			}
		})
	}

	// A longer running range starts earlier and ends on the same, unbounded
	// balance: every row, the three dated after today among them.
	r := rangeReport(t, l, s.fundID, "2026-09-25", "2026-10-02")
	all := ledgerSumThrough(t, l, s.fundID, "")
	if r.Walk.End != all || r.Balance != all {
		t.Errorf("End, Balance = %d, %d; want the whole ledger, %d", r.Walk.End, r.Balance, all)
	}
	if fb, err := l.at(reportNow).FundBalance(context.Background(), s.fundID); err != nil || fb != r.Balance {
		t.Errorf("Balance = %d, FundBalance = %d (%v): the running range must equal Beranda", r.Balance, fb, err)
	}
	if want := ledgerSumThrough(t, l, s.fundID, "2026-09-24"); r.Walk.Start != want {
		t.Errorf("Start = %d, want the balance through 24 Sep, %d", r.Walk.Start, want)
	}
	if got := walkFoots(t, r.Walk); got != r.Walk.End {
		t.Errorf("walk does not foot: %d vs End %d", got, r.Walk.End)
	}

	// The day before today ends a past period: bounded, so the rows dated after
	// it (and today's) are in neither the walk nor the header.
	past := rangeReport(t, l, s.fundID, "2026-09-25", "2026-10-01")
	if past.Running || past.AsOf != "2026-10-01" {
		t.Errorf("Running, AsOf = %v, %q; want false, 2026-10-01", past.Running, past.AsOf)
	}
	if want := ledgerSumThrough(t, l, s.fundID, "2026-10-01"); past.Balance != want || past.Walk.End != want || want != 1_073_500 {
		t.Errorf("Balance, End = %d, %d; want %d (1073500)", past.Balance, past.Walk.End, want)
	}
}

// The day edges are the fund's: a Jakarta day ends at 17:00 UTC.
func TestRangeEdgesAreTheFundsDays(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-08-31", nil)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 5_000, "2026-09-01", nil)

	count := func(at time.Time, difference int64, resolution string) {
		t.Helper()
		rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{FundID: f.fundID, PerformedAt: at.Unix(), CreatedAt: at.Unix()})
		if err != nil {
			t.Fatalf("CreateReconciliation() = %v, want no error", err)
		}
		if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
			FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.cashID,
			ActualAmount: difference, DifferenceAmount: difference, Resolution: resolution,
		}); err != nil {
			t.Fatalf("CreateReconciliationLine() = %v, want no error", err)
		}
	}
	// The last second of 31 August in Jakarta, and the first of 1 September.
	count(jakartaAt(2026, time.August, 31, 23, 59, 59), 3_000, "left_open")
	count(jakartaAt(2026, time.September, 1, 0, 0, 0), 0, "matched")

	end31 := rangeReport(t, l, f.fundID, "2026-08-25", "2026-08-31")
	if rec := end31.Reconciliation; rec == nil || rec.Date != "2026-08-31" || rec.Matched || rec.Difference != 3_000 {
		t.Errorf("a range ending 31 Aug: reconciliation = %+v, want the 23:59:59 count, 3000 open", rec)
	}
	end01 := rangeReport(t, l, f.fundID, "2026-08-25", "2026-09-01")
	if rec := end01.Reconciliation; rec == nil || rec.Date != "2026-09-01" || !rec.Matched {
		t.Errorf("a range ending 1 Sep: reconciliation = %+v, want the 00:00:00 count, matched", rec)
	}

	// "Today" flips at the same second: one second before, 31 August is today and
	// the range runs unbounded (the 1 September row, dated ahead, is in the
	// balance); a second later it is a past day and the row is out.
	for _, tc := range []struct {
		name        string
		now         time.Time
		wantRunning bool
		wantBalance money.Amount
	}{
		{"one second before September in Jakarta", jakartaAt(2026, time.August, 31, 23, 59, 59), true, 105_000},
		{"the first second of September in Jakarta", jakartaAt(2026, time.September, 1, 0, 0, 0), false, 100_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := monthlyReportAt(t, l, tc.now, ReportParams{FundID: f.fundID, From: "2026-08-31", To: "2026-08-31"})
			if r.Running != tc.wantRunning || r.AsOf != "2026-08-31" || r.Balance != tc.wantBalance {
				t.Errorf("Running, AsOf, Balance = %v, %q, %d; want %v, 2026-08-31, %d", r.Running, r.AsOf, r.Balance, tc.wantRunning, tc.wantBalance)
			}
		})
	}
}

// An envelope is on the report when it was open at some point during the
// period: opened by its last day, not closed before its first.
func TestRangeEnvelopesAreThoseOpenDuringIt(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newMonthEndScenario(t, l) // Halal bihalal: opened 10 Aug, closed 5 Sep

	tests := []struct {
		name         string
		from, to     string
		shown        bool
		wantClosedOn string // "" for still open at the period's end
		wantBalance  money.Amount
	}{
		{"ends the day before it opened", "2026-08-01", "2026-08-09", false, "", 0},
		{"ends the day it opened", "2026-08-01", "2026-08-10", true, "", 0},
		{"inside its life, still open at the end", "2026-08-15", "2026-08-20", true, "", 20_000},
		{"spans its closing", "2026-08-31", "2026-09-10", true, "2026-09-05", 0},
		{"starts the day it closed", "2026-09-05", "2026-09-20", true, "2026-09-05", 0},
		{"starts the day after it closed", "2026-09-06", "2026-09-20", false, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := rangeReport(t, l, s.f.fundID, tt.from, tt.to)
			var found *ReportEnvelope
			for i := range r.Envelopes {
				if r.Envelopes[i].Name == "Halal bihalal" {
					found = &r.Envelopes[i]
				}
			}
			if (found != nil) != tt.shown {
				t.Fatalf("envelope shown = %v, want %v (%+v)", found != nil, tt.shown, r.Envelopes)
			}
			if found == nil {
				return
			}
			closed := ""
			if found.ClosedOn != nil {
				closed = *found.ClosedOn
			}
			if closed != tt.wantClosedOn || found.Balance != tt.wantBalance {
				t.Errorf("ClosedOn, Balance = %q, %d; want %q, %d", closed, found.Balance, tt.wantClosedOn, tt.wantBalance)
			}
		})
	}
}

// The dues list and the twelve-month grid are monthly: a range reads the month
// that holds To, whatever month it started in.
func TestRangeDuesReadTheMonthThatHoldsTo(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newMonthScenario(t, l)

	status := func(r Report) map[string]DuesStatus {
		out := map[string]DuesStatus{}
		for _, d := range r.Dues {
			out[d.MemberName] = d.Status
		}
		return out
	}

	// From August, To in September: September's statuses.
	sep := rangeReport(t, l, s.f.fundID, "2026-08-15", "2026-09-10")
	if sep.Month != "2026-09" {
		t.Errorf("Month = %q, want 2026-09", sep.Month)
	}
	got := status(sep)
	if got["Member One"] != DuesStatusPaid || got["Member Two"] != DuesStatusPartial || got["Member Three"] != DuesStatusUnpaid {
		t.Errorf("dues = %v, want September's: paid, partial, unpaid", got)
	}
	if want := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09"}).DuesGrid; !reflect.DeepEqual(sep.DuesGrid, want) {
		t.Errorf("DuesGrid differs from September's:\n%+v\nvs\n%+v", sep.DuesGrid, want)
	}

	// The same From ending in August: August's, in which nobody has paid.
	aug := rangeReport(t, l, s.f.fundID, "2026-08-15", "2026-08-31")
	if aug.Month != "2026-08" {
		t.Errorf("Month = %q, want 2026-08", aug.Month)
	}
	for name, st := range status(aug) {
		if st != DuesStatusUnpaid {
			t.Errorf("August dues: %s = %s, want unpaid", name, st)
		}
	}
	if len(aug.Dues) != 3 {
		t.Errorf("August dues rows = %d, want 3", len(aug.Dues))
	}

	// The dues filter narrows the list as it does for the month, never the rows.
	filtered := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, From: "2026-08-15", To: "2026-09-10", DuesStatus: ReportDuesPaid})
	if len(filtered.Dues) != 1 || filtered.Dues[0].MemberName != "Member One" {
		t.Errorf("dues under the paid filter = %+v, want Member One alone", filtered.Dues)
	}
	if len(filtered.Rows) != len(sep.Rows) {
		t.Errorf("a dues filter changed the rows: %d vs %d", len(filtered.Rows), len(sep.Rows))
	}
}

// Month and a range are exclusive: with both days set, Month is ignored (even a
// malformed one); anything short of two valid days in order is the ledger's
// ErrInvalidArgument, the edge deciding what to fall back to.
func TestRangeParamsAreValidatedByTheLedger(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 1_000, "2026-09-01", nil)

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "not-a-month", From: "2026-09-01", To: "2026-09-02"})
	if !r.IsRange || r.Month != "2026-09" {
		t.Errorf("IsRange, Month = %v, %q; want a range, 2026-09 (Month ignored)", r.IsRange, r.Month)
	}
	if m := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"}); m.IsRange || m.From != "2026-09-01" || m.To != "2026-09-30" {
		t.Errorf("a month: IsRange, From, To = %v, %q, %q; want false, 2026-09-01, 2026-09-30", m.IsRange, m.From, m.To)
	}
	if run := monthlyReport(t, l, ReportParams{FundID: f.fundID}); run.From != "2026-10-01" || run.To != "2026-10-02" {
		t.Errorf("the running month: From, To = %q, %q; want 2026-10-01, 2026-10-02", run.From, run.To)
	}

	for _, tt := range []struct {
		name     string
		from, to string
	}{
		{"from alone", "2026-09-01", ""},
		{"to alone", "", "2026-09-01"},
		{"from malformed", "2026-9-1", "2026-09-02"},
		{"to not a day", "2026-09-01", "2026-02-30"},
		{"words", "yesterday", "today"},
		{"a month, not a day", "2026-09", "2026-10"},
		{"reversed", "2026-09-02", "2026-09-01"},
		{"starting after today", "2026-10-03", "2026-10-04"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := ReportParams{FundID: f.fundID, From: tt.from, To: tt.to}
			if _, err := l.at(reportNow).MonthlyReport(context.Background(), p); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("MonthlyReport(%+v) error = %v, want ErrInvalidArgument", p, err)
			}
		})
	}
}

// MonthWalk still reads one month, and a range's From and To are no part of it.
func TestMonthWalkIgnoresARange(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	want := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: "2026-09"}).Walk
	got, err := l.at(reportNow).MonthWalk(context.Background(), ReportParams{FundID: s.fundID, Month: "2026-09", From: "2026-09-05", To: "2026-09-12"})
	if err != nil {
		t.Fatalf("MonthWalk() = %v, want no error", err)
	}
	if got != want {
		t.Errorf("MonthWalk = %+v, want the month's %+v", got, want)
	}
}

// ReportDays bounds a range: the first transaction's day and today, both in the
// fund's zone; an empty fund, or one whose first row is dated ahead, starts today.
func TestReportDays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	l := newTestLedger(t)
	empty := newFixture(t, l)
	first, today, err := l.at(reportNow).ReportDays(ctx, empty.fundID)
	if err != nil || first != "2026-10-02" || today != "2026-10-02" {
		t.Errorf("empty fund: ReportDays = %q, %q, %v; want today twice", first, today, err)
	}

	postEntry(t, l, empty.fundID, empty.cashID, empty.mainID, "in", 1_000, "2026-06-20", nil)
	postEntry(t, l, empty.fundID, empty.cashID, empty.mainID, "in", 1_000, "2026-09-01", nil)
	if first, today, err = l.at(reportNow).ReportDays(ctx, empty.fundID); err != nil || first != "2026-06-20" || today != "2026-10-02" {
		t.Errorf("ReportDays = %q, %q, %v; want 2026-06-20, 2026-10-02", first, today, err)
	}

	// Today turns over at 17:00 UTC.
	if _, today, _ = l.at(jakartaAt(2026, time.August, 31, 23, 59, 59)).ReportDays(ctx, empty.fundID); today != "2026-08-31" {
		t.Errorf("one second before September in Jakarta: today = %q, want 2026-08-31", today)
	}
	if _, today, _ = l.at(jakartaAt(2026, time.September, 1, 0, 0, 0)).ReportDays(ctx, empty.fundID); today != "2026-09-01" {
		t.Errorf("first second of September in Jakarta: today = %q, want 2026-09-01", today)
	}

	l2 := newTestLedger(t)
	ahead := newFixture(t, l2)
	postEntry(t, l2, ahead.fundID, ahead.cashID, ahead.mainID, "in", 1_000, "2027-03-01", nil)
	if first, today, err = l2.at(reportNow).ReportDays(ctx, ahead.fundID); err != nil || first != "2026-10-02" || today != "2026-10-02" {
		t.Errorf("first row dated ahead: ReportDays = %q, %q, %v; want today twice", first, today, err)
	}

	if _, _, err := l.at(reportNow).ReportDays(ctx, 424_242); err == nil {
		t.Error("ReportDays(unknown fund) = nil error, want one")
	}
}

// A member filter slices the range as it slices the month: no ends (a balance
// has no meaning for one member, ADR-038), only Total masuk and Total keluar,
// over the same rows. A range covering the whole month reads exactly as the
// month does under the same filter.
func TestRangeUnderAMemberFilterHasNoEnds(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)
	members, err := l.q.ListMembersByFund(context.Background(), s.fundID)
	if err != nil {
		t.Fatalf("ListMembersByFund() = %v", err)
	}
	if len(members) == 0 {
		t.Fatal("the walk scenario has no member to filter by")
	}
	for _, m := range members {
		month := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: "2026-09", MemberID: &m.ID})
		whole := monthlyReport(t, l, ReportParams{FundID: s.fundID, From: "2026-09-01", To: "2026-09-30", MemberID: &m.ID})
		if whole.Walk.Full || whole.Walk.StartOn != "" || whole.Walk.Start != 0 || whole.Walk.End != 0 {
			t.Errorf("%s: range walk = %+v, want no ends under a member filter", m.Name, whole.Walk)
		}
		if whole.Walk != month.Walk || len(whole.Rows) != len(month.Rows) {
			t.Errorf("%s: whole-month range = %+v (%d rows), want the month's %+v (%d rows)", m.Name, whole.Walk, len(whole.Rows), month.Walk, len(month.Rows))
		}
		part := monthlyReport(t, l, ReportParams{FundID: s.fundID, From: "2026-09-05", To: "2026-09-12", MemberID: &m.ID})
		if part.Walk.Full {
			t.Errorf("%s: part-month range walk is Full under a member filter", m.Name)
		}
	}
}
