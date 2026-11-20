package ledger

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ADR-051, "balances follow the cash; flows follow the spending": a settled
// talangan's payout is one ledger row dated the day it was paid, and every
// balance keeps that date. Every flow (the walk's Total keluar, the report's
// rows, Riwayat's month walk, Beranda's Arus kas) places it on the day its
// member fronted it, and a signed bridge line (ReportWalk.PaidLater and PaidEarlier, Talangan dibayar kemudian and Pelunasan talangan sebelumnya on
// the page) keeps each walk footing. A location filter is a cash view: no
// bridge, the payout on the day the cash left.
//
// One scenario holds every shape; the tests below read it through every view.
//
//	Sep  15  Cash       in 300.000  Kas Utama
//	Sep  16  Cash       in  10.000  Pass-through
//	Sep  20  Bank       in  50.000  Kas Utama
//	Okt  20  claim D, 99.000 Kas Utama, never settled
//	Okt  21  claim E,  7.000 Kas Utama, waived 2 Nov
//	Okt  27  claim A, 450.000 Kas Utama by Jane, fronted; paid 12 Nov from Cash
//	Okt  28  claim B,  10.000 Pass-through by Jane, fronted; paid 14 Nov from Bank
//	Nov   3  Cash       in 600.000  Kas Utama (the month's dues, as an in)
//	Nov   5  claim C,  20.000 Kas Utama by Budi, fronted; paid 8 Nov from Cash
//
// The clock stands on 5 December, so September to November are past months and
// December, with nothing in it, the running one.
var frontedNow = jakartaAt(2026, time.December, 5, 12, 0, 0)

type frontedScenario struct {
	f      fixture
	budiID int64
}

func newFrontedScenario(t *testing.T, l *Ledger) frontedScenario {
	t.Helper()
	ctx := context.Background()
	q := store.New(l.db)
	f := newFixture(t, l)
	budi, err := q.CreateMember(ctx, store.CreateMemberParams{JoinedOn: "2000-01-01", FundID: f.fundID, Name: "Budi", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateMember(Budi) = %v, want no error", err)
	}
	s := frontedScenario{f: f, budiID: budi.ID}

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 300_000, "2026-09-15", nil)
	postEntry(t, l, f.fundID, f.cashID, f.passID, "in", 10_000, "2026-09-16", nil)
	postEntry(t, l, f.fundID, f.bankID, f.mainID, "in", 50_000, "2026-09-20", nil)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 600_000, "2026-11-03", nil)

	claim := func(member, purpose int64, amount int64, incurredOn string) store.Reimbursement {
		t.Helper()
		c, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
			FundID: f.fundID, MemberID: member, PurposeID: purpose, Amount: amount, IncurredOn: incurredOn, CreatedAt: 1,
		})
		if err != nil {
			t.Fatalf("CreateReimbursement(%d on %s) = %v, want no error", amount, incurredOn, err)
		}
		return c
	}
	settle := func(c store.Reimbursement, accountID int64, on string) {
		t.Helper()
		if _, err := l.at(onTheDay(on)).SettleReimbursement(ctx, SettleReimbursementParams{
			FundID: f.fundID, ReimbursementID: c.ID, AccountID: accountID, OccurredOn: on,
		}); err != nil {
			t.Fatalf("SettleReimbursement(%d on %s) = %v, want no error", c.Amount, on, err)
		}
	}
	claim(f.memberID, f.mainID, 99_000, "2026-10-20") // D: unsettled
	waived := claim(f.memberID, f.mainID, 7_000, "2026-10-21")
	waivedOn := "2026-11-02"
	if _, err := l.at(onTheDay(waivedOn)).UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: waived.ID, WaivedOn: &waivedOn, SetWaivedOn: true,
	}); err != nil {
		t.Fatalf("waiving claim E = %v, want no error", err)
	}
	settle(claim(f.memberID, f.mainID, 450_000, "2026-10-27"), f.cashID, "2026-11-12") // A
	settle(claim(f.memberID, f.passID, 10_000, "2026-10-28"), f.bankID, "2026-11-14")  // B
	settle(claim(budi.ID, f.mainID, 20_000, "2026-11-05"), f.cashID, "2026-11-08")     // C
	return s
}

// ledgerSumForPurposeThrough is the pos's balance through a day by raw SQL: the
// stored tag, summed by the payout's own date.
func ledgerSumForPurposeThrough(t *testing.T, l *Ledger, fundID, purposeID int64, through string) money.Amount {
	t.Helper()
	var sum int64
	err := l.db.QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0)
		FROM "transaction"
		WHERE fund_id = ? AND purpose_id = ? AND occurred_on <= ?`, fundID, purposeID, through).Scan(&sum)
	if err != nil {
		t.Fatalf("summing purpose %d through %s = %v, want no error", purposeID, through, err)
	}
	return money.FromDB(sum)
}

// assertWalk compares the walk's figures, then holds it to the two claims every
// walk makes: it foots to the rupiah, and both its ends are ledger sums by a
// route of their own (raw SQL by the row's own date: balances never move).
func assertWalk(t *testing.T, name string, got, want ReportWalk, endSum, startSum money.Amount) {
	t.Helper()
	want.Full = true
	if got != want {
		t.Errorf("%s: walk = %+v, want %+v", name, got, want)
	}
	if foots := walkFootsMoved(t, got); foots != got.End {
		t.Errorf("%s: Start + Openings + In - Out + Adjustments + Moved + Fronted = %d, End = %d", name, foots, got.End)
	}
	if got.End != endSum {
		t.Errorf("%s: End = %d, but the ledger by payment date sums to %d", name, got.End, endSum)
	}
	if got.Start != startSum {
		t.Errorf("%s: Start = %d, but the ledger by payment date sums to %d", name, got.Start, startSum)
	}
}

// The issue's scenario, whole fund: Rp450.000 fronted 27 Okt and paid from Kas
// tunai 12 Nov counts in Okt's Total keluar, and the Talangan line keeps both
// months footing.
func TestFrontedPayoutCountsInTheMonthItWasFrontedAndTheWalkFoots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f

	// 450.000 (A) + 10.000 (B) in October, and nothing for D (99.000, still owed)
	// or E (7.000, waived on 2 Nov): an unsettled claim is in no walk. November
	// pays A, B and C, and counts only its own claim (C, 20.000): 20.000 -
	// 480.000 = -460.000.
	oct := ReportWalk{StartOn: "2026-09-30", EndOn: "2026-10-31", Start: 360_000, Out: 460_000, PaidLater: 460_000, End: 360_000}
	nov := ReportWalk{StartOn: "2026-10-31", EndOn: "2026-11-30", Start: 360_000, In: 600_000, Out: 20_000, PaidEarlier: 460_000, End: 480_000}
	dec := ReportWalk{StartOn: "2026-11-30", EndOn: "2026-12-05", Start: 480_000, End: 480_000}

	for _, tc := range []struct {
		month string
		want  ReportWalk
	}{{"2026-10", oct}, {"2026-11", nov}, {"2026-12", dec}} {
		r := monthlyReportAt(t, l, frontedNow, ReportParams{FundID: f.fundID, Month: tc.month})
		assertWalk(t, "MonthlyReport "+tc.month, r.Walk, tc.want,
			ledgerSumThrough(t, l, f.fundID, tc.want.EndOn), ledgerSumThrough(t, l, f.fundID, tc.want.StartOn))

		// Riwayat's month walk is the report's, line for line.
		w, err := l.at(frontedNow).MonthWalk(ctx, ReportParams{FundID: f.fundID, Month: tc.month})
		if err != nil {
			t.Fatalf("MonthWalk(%s) = %v, want no error", tc.month, err)
		}
		if w != r.Walk {
			t.Errorf("MonthWalk(%s) = %+v, want the report's %+v", tc.month, w, r.Walk)
		}
	}
}

// The report's rows place the payout on the day it was fronted, in the month it
// was fronted, and the row says where else it reads. October's list holds the
// two payouts; November's holds only its own claim's.
func TestFrontedPayoutRowsSitOnTheirFrontingDay(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f

	type placed struct {
		date    string
		amount  money.Amount
		payout  ReportPayout
		counted ReportLine
	}
	read := func(month string) []placed {
		t.Helper()
		r := monthlyReportAt(t, l, frontedNow, ReportParams{FundID: f.fundID, Month: month})
		var out []placed
		for _, row := range r.Rows {
			if row.Entry == nil || row.Entry.Kind != TransactionKindReimbursement {
				continue
			}
			if row.Entry.Payout == nil {
				t.Fatalf("%s: payout row has no Payout: %+v", month, row.Entry)
			}
			out = append(out, placed{row.Date, row.Entry.Amount, *row.Entry.Payout, row.Entry.Line})
		}
		return out
	}

	// Newest first: B (28 Okt) above A (27 Okt).
	wantOct := []placed{
		{"2026-10-28", 10_000, ReportPayout{FrontedOn: "2026-10-28", PaidOn: "2026-11-14"}, ReportLineOut},
		{"2026-10-27", 450_000, ReportPayout{FrontedOn: "2026-10-27", PaidOn: "2026-11-12"}, ReportLineOut},
	}
	wantNov := []placed{
		{"2026-11-05", 20_000, ReportPayout{FrontedOn: "2026-11-05", PaidOn: "2026-11-08"}, ReportLineOut},
	}
	for month, want := range map[string][]placed{"2026-10": wantOct, "2026-11": wantNov, "2026-12": nil, "2026-09": nil} {
		got := read(month)
		if len(got) != len(want) {
			t.Errorf("%s: payout rows = %+v, want %+v", month, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: payout row %d = %+v, want %+v", month, i, got[i], want[i])
			}
		}
	}

	// A payout's row dates the walk's line and the list alike: the subtotal of
	// the rows on the Out line is the walk's Total keluar, in every month.
	for _, month := range []string{"2026-09", "2026-10", "2026-11", "2026-12"} {
		r := monthlyReportAt(t, l, frontedNow, ReportParams{FundID: f.fundID, Month: month})
		var out money.Amount
		for _, e := range entries(r.Rows) {
			if e.Line == ReportLineOut {
				out += e.Amount
			}
		}
		if out != r.Walk.Out {
			t.Errorf("%s: the Out rows add to %d, the walk's Total keluar is %d", month, out, r.Walk.Out)
		}
		for _, row := range r.Rows {
			if row.Date[:7] != month {
				t.Errorf("%s: row dated %s is placed outside the month", month, row.Date)
			}
		}
	}
}

// Beranda's Arus kas bulan ini is the running month's In and Out, through the
// same derivation: a payout fronted in October and paid in November is not
// November's spending.
func TestFrontedPayoutIsNotTheRunningMonthsOutWhenFrontedEarlier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)

	in, out, err := l.at(jakartaAt(2026, time.November, 20, 12, 0, 0)).MonthInOut(ctx, s.f.fundID)
	if err != nil {
		t.Fatalf("MonthInOut() = %v, want no error", err)
	}
	// The dues in, and claim C (fronted and paid in November), but not A or B.
	if in != 600_000 || out != 20_000 {
		t.Errorf("MonthInOut() = %d/%d, want 600000/20000", in, out)
	}
}

// A date range is one period like a month: the bridge counts the payouts placed
// in it, minus those paid in it, so a range holding both days of a claim has
// none, and a range cutting between them has it.
func TestFrontedLineOverDateRanges(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f

	tests := []struct {
		name     string
		from, to string
		purpose  int64
		want     ReportWalk // Full, StartOn and EndOn filled in below
	}{
		// A, B and C all counted, A and C paid in it, B (14 Nov) after: 480.000 - 470.000.
		{"cut between B's two days, holding A's and C's", "2026-10-27", "2026-11-12", 0,
			ReportWalk{Start: 360_000, In: 600_000, Out: 480_000, PaidLater: 10_000, End: 490_000}},
		// Only C counted (5 Nov); A (12 Nov) and C paid: 20.000 - 470.000.
		{"from the first of November to A's payment", "2026-11-01", "2026-11-12", 0,
			ReportWalk{Start: 360_000, In: 600_000, Out: 20_000, PaidEarlier: 450_000, End: 490_000}},
		// Every claim fronted and paid inside it: the sets cancel.
		{"holding every claim's two days", "2026-10-20", "2026-11-30", 0,
			ReportWalk{Start: 360_000, In: 600_000, Out: 480_000, End: 480_000}},
		// The same cut for one pos: Kas Utama's A and C are both inside, so no bridge.
		{"Kas Utama over the cut", "2026-10-27", "2026-11-12", f.mainID,
			ReportWalk{Start: 350_000, In: 600_000, Out: 470_000, End: 480_000}},
		// Titipan's B is fronted in the range and paid after it.
		{"Titipan over the cut", "2026-10-27", "2026-11-12", f.passID,
			ReportWalk{Start: 10_000, Out: 10_000, PaidLater: 10_000, End: 10_000}},
		// A range ending before any payment: all spending, all bridge.
		{"October alone as a range", "2026-10-01", "2026-10-31", 0,
			ReportWalk{Start: 360_000, Out: 460_000, PaidLater: 460_000, End: 360_000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ReportParams{FundID: f.fundID, From: tt.from, To: tt.to}
			if tt.purpose != 0 {
				p.PurposeID = int64Ptr(tt.purpose)
			}
			r := monthlyReportAt(t, l, frontedNow, p)
			want := tt.want
			want.StartOn, want.EndOn = dayBefore(t, tt.from), tt.to
			sum := func(through string) money.Amount {
				if tt.purpose != 0 {
					return ledgerSumForPurposeThrough(t, l, f.fundID, tt.purpose, through)
				}
				return ledgerSumThrough(t, l, f.fundID, through)
			}
			assertWalk(t, tt.name, r.Walk, want, sum(tt.to), sum(want.StartOn))
		})
	}
}

// A pos filter reads the same bridge for that pos's payouts alone, and every
// pos's walk foots every month; the poses together make the fund's.
func TestFrontedLineForEachPos(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f

	tests := []struct {
		pos   string
		id    int64
		month string
		want  ReportWalk
	}{
		{"Kas Utama", f.mainID, "2026-10", ReportWalk{StartOn: "2026-09-30", EndOn: "2026-10-31", Start: 350_000, Out: 450_000, PaidLater: 450_000, End: 350_000}},
		// A and C paid in November, C alone counted: 20.000 - 470.000.
		{"Kas Utama", f.mainID, "2026-11", ReportWalk{StartOn: "2026-10-31", EndOn: "2026-11-30", Start: 350_000, In: 600_000, Out: 20_000, PaidEarlier: 450_000, End: 480_000}},
		{"Titipan", f.passID, "2026-10", ReportWalk{StartOn: "2026-09-30", EndOn: "2026-10-31", Start: 10_000, Out: 10_000, PaidLater: 10_000, End: 10_000}},
		{"Titipan", f.passID, "2026-11", ReportWalk{StartOn: "2026-10-31", EndOn: "2026-11-30", Start: 10_000, PaidEarlier: 10_000, End: 0}},
	}
	for _, tt := range tests {
		r := monthlyReportAt(t, l, frontedNow, ReportParams{FundID: f.fundID, Month: tt.month, PurposeID: int64Ptr(tt.id)})
		assertWalk(t, tt.pos+" "+tt.month, r.Walk, tt.want,
			ledgerSumForPurposeThrough(t, l, f.fundID, tt.id, tt.want.EndOn), ledgerSumForPurposeThrough(t, l, f.fundID, tt.id, tt.want.StartOn))

		w, err := l.at(frontedNow).MonthWalk(ctx, ReportParams{FundID: f.fundID, Month: tt.month, PurposeID: int64Ptr(tt.id)})
		if err != nil {
			t.Fatalf("MonthWalk(%s, %s) = %v, want no error", tt.pos, tt.month, err)
		}
		if w != r.Walk {
			t.Errorf("MonthWalk(%s, %s) = %+v, want the report's %+v", tt.pos, tt.month, w, r.Walk)
		}
	}

	// The poses' bridges add up to the fund's, month by month.
	for _, month := range []string{"2026-09", "2026-10", "2026-11", "2026-12"} {
		fund := monthlyReportAt(t, l, frontedNow, ReportParams{FundID: f.fundID, Month: month}).Walk
		var sum money.Amount
		for _, pos := range []int64{f.mainID, f.passID, f.incidenID} {
			sum += bridge(monthlyReportAt(t, l, frontedNow, ReportParams{FundID: f.fundID, Month: month, PurposeID: int64Ptr(pos)}).Walk)
		}
		if sum != bridge(fund) {
			t.Errorf("%s: the poses' Talangan lines add to %d, the fund's is %d", month, sum, bridge(fund))
		}
	}
}

// A location filter is a cash view: the payout sits on the day the cash left
// and the walk has no bridge. Kas Utama's A and C leave Cash in November, B
// leaves Bank.
func TestFrontedLocationWalkIsACashViewWithNoBridge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f

	tests := []struct {
		name      string
		accountID int64
		purposeID int64
		month     string
		want      ReportWalk
	}{
		{"Cash in October", f.cashID, 0, "2026-10", ReportWalk{StartOn: "2026-09-30", EndOn: "2026-10-31", Start: 310_000, End: 310_000}},
		{"Cash in November", f.cashID, 0, "2026-11", ReportWalk{StartOn: "2026-10-31", EndOn: "2026-11-30", Start: 310_000, In: 600_000, Out: 470_000, End: 440_000}},
		{"Bank in October", f.bankID, 0, "2026-10", ReportWalk{StartOn: "2026-09-30", EndOn: "2026-10-31", Start: 50_000, End: 50_000}},
		{"Bank in November", f.bankID, 0, "2026-11", ReportWalk{StartOn: "2026-10-31", EndOn: "2026-11-30", Start: 50_000, Out: 10_000, End: 40_000}},
		{"Cash and Kas Utama in November", f.cashID, f.mainID, "2026-11", ReportWalk{StartOn: "2026-10-31", EndOn: "2026-11-30", Start: 300_000, In: 600_000, Out: 470_000, End: 430_000}},
		// Titipan's 10.000 sat in Cash and B was paid from Bank, so Bank's share of
		// the pos goes below zero: the location walk is where the cash is.
		{"Bank and Titipan in November", f.bankID, f.passID, "2026-11", ReportWalk{StartOn: "2026-10-31", EndOn: "2026-11-30", Out: 10_000, End: -10_000}},
		{"Cash and Titipan in November", f.cashID, f.passID, "2026-11", ReportWalk{StartOn: "2026-10-31", EndOn: "2026-11-30", Start: 10_000, End: 10_000}},
	}
	for _, tt := range tests {
		p := ReportParams{FundID: f.fundID, Month: tt.month, AccountID: int64Ptr(tt.accountID)}
		if tt.purposeID != 0 {
			p.PurposeID = int64Ptr(tt.purposeID)
		}
		got, err := l.at(frontedNow).MonthWalk(ctx, p)
		if err != nil {
			t.Fatalf("%s: MonthWalk() = %v, want no error", tt.name, err)
		}
		want := tt.want
		want.Full = true
		if got != want {
			t.Errorf("%s: walk = %+v, want %+v", tt.name, got, want)
		}
		if got.PaidLater != 0 || got.PaidEarlier != 0 {
			t.Errorf("%s: PaidLater, PaidEarlier = %d, %d, want 0, 0: a location walk is cash end to end", tt.name, got.PaidLater, got.PaidEarlier)
		}
		if sum := locationFoots(t, got); sum != got.End {
			t.Errorf("%s: foots to %d, End = %d", tt.name, sum, got.End)
		}
		if want := ledgerSumForAccount(t, l, f.fundID, tt.accountID, tt.purposeID, got.EndOn); got.End != want {
			t.Errorf("%s: End = %d, but the ledger sums to %d", tt.name, got.End, want)
		}
	}

	// The cash view's rows sit on the payment day, naming the fronting day.
	monthStart, err := parseReportMonth("2026-11")
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := l.at(frontedNow).reportRows(ctx, ReportParams{FundID: f.fundID, AccountID: int64Ptr(f.cashID)}, monthPeriod(monthStart, frontedNow))
	if err != nil {
		t.Fatalf("reportRows(cash view) = %v, want no error", err)
	}
	want := map[string]string{"2026-11-12": "2026-10-27", "2026-11-08": "2026-11-05"}
	got := map[string]string{}
	for _, row := range rows {
		if row.Entry != nil && row.Entry.Payout != nil {
			got[row.Date] = row.Entry.Payout.FrontedOn
			if row.Date != row.Entry.Payout.PaidOn {
				t.Errorf("cash view: row dated %s, its payout was paid %s", row.Date, row.Entry.Payout.PaidOn)
			}
		}
	}
	if len(got) != len(want) || got["2026-11-12"] != want["2026-11-12"] || got["2026-11-08"] != want["2026-11-08"] {
		t.Errorf("cash view payout rows (paid -> fronted) = %v, want %v", got, want)
	}
}

// A member or direction filter walks only Total masuk and Total keluar: no
// balance, so no bridge, though the payouts still sit on the fronting day.
func TestFrontedPayoutUnderAMemberOrDirectionFilter(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f

	tests := []struct {
		name  string
		month string
		p     ReportParams
		out   money.Amount
	}{
		{"Jane in October", "2026-10", ReportParams{MemberID: int64Ptr(f.memberID)}, 460_000},
		{"Jane in November", "2026-11", ReportParams{MemberID: int64Ptr(f.memberID)}, 0},
		{"Budi in November", "2026-11", ReportParams{MemberID: int64Ptr(s.budiID)}, 20_000},
		{"Budi in October", "2026-10", ReportParams{MemberID: int64Ptr(s.budiID)}, 0},
		{"spending in October", "2026-10", ReportParams{Direction: ReportDirectionOut}, 460_000},
		{"spending in November", "2026-11", ReportParams{Direction: ReportDirectionOut}, 20_000},
	}
	for _, tt := range tests {
		tt.p.FundID, tt.p.Month = f.fundID, tt.month
		w := monthlyReportAt(t, l, frontedNow, tt.p).Walk
		if w.Full || w.Out != tt.out || w.PaidLater != 0 || w.PaidEarlier != 0 {
			t.Errorf("%s: Full, Out, PaidLater, PaidEarlier = %v, %d, %d, %d; want false, %d, 0, 0", tt.name, w.Full, w.Out, w.PaidLater, w.PaidEarlier, tt.out)
		}
	}
}

// Balances follow the cash. Every balance the app shows is the sum of the rows
// by their own date, whatever day the claim was fronted: the figures below are
// what they were before the claim's date was read anywhere.
func TestFrontedPayoutLeavesEveryBalanceOnItsPaymentDate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f
	at := l.at(frontedNow)

	// The header of each past month reads as of its last day, before and after A
	// is paid: 360.000 on 31 Okt (nothing paid yet), 480.000 on 30 Nov.
	for _, tc := range []struct {
		month         string
		balance       money.Amount
		main, pass    money.Amount
		owedToMembers money.Amount
	}{
		// A, B and D are owed, and E too: it is waived only on 2 Nov.
		{"2026-10", 360_000, 350_000, 10_000, 450_000 + 10_000 + 99_000 + 7_000},
		// Only D is owed once A, B and C are paid and E is waived.
		{"2026-11", 480_000, 480_000, 0, 99_000},
	} {
		r := monthlyReportAt(t, l, frontedNow, ReportParams{FundID: f.fundID, Month: tc.month})
		if r.Balance != tc.balance {
			t.Errorf("%s: Saldo kas = %d, want %d", tc.month, r.Balance, tc.balance)
		}
		if r.OwedToMembers != tc.owedToMembers {
			t.Errorf("%s: owed to members = %d, want %d", tc.month, r.OwedToMembers, tc.owedToMembers)
		}
		balances := map[string]money.Amount{}
		for _, p := range r.PurposeBalances {
			balances[p.Name] = p.Balance
		}
		if balances["Primary Cash"] != tc.main || balances["Pass-through"] != tc.pass {
			t.Errorf("%s: purpose balances = %v, want Primary Cash %d, Pass-through %d", tc.month, balances, tc.main, tc.pass)
		}
	}

	// The live balances: Cash pays A and C, Bank pays B.
	for name, tc := range map[string]struct {
		got  func() (money.Amount, error)
		want money.Amount
	}{
		"fund":       {func() (money.Amount, error) { return at.FundBalance(ctx, f.fundID) }, 480_000},
		"Cash":       {func() (money.Amount, error) { return at.AccountBalance(ctx, f.fundID, f.cashID) }, 440_000},
		"Bank":       {func() (money.Amount, error) { return at.AccountBalance(ctx, f.fundID, f.bankID) }, 40_000},
		"Kas Utama":  {func() (money.Amount, error) { return at.PurposeBalance(ctx, f.fundID, f.mainID) }, 480_000},
		"Titipan":    {func() (money.Amount, error) { return at.PurposeBalance(ctx, f.fundID, f.passID) }, 0},
		"Cash (raw)": {func() (money.Amount, error) { return ledgerSumForAccount(t, l, f.fundID, f.cashID, 0, ""), nil }, 440_000},
	} {
		got, err := tc.got()
		if err != nil {
			t.Fatalf("%s balance = %v, want no error", name, err)
		}
		if got != tc.want {
			t.Errorf("%s balance = %d, want %d", name, got, tc.want)
		}
	}

	// A cek kas freezes what is recorded in each location by the payment dates: 440.000
	// in Cash and 40.000 in Bank count as matched with no gap.
	rec, err := at.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{
			{AccountID: f.cashID, ActualAmount: 440_000, Resolution: "matched"},
			{AccountID: f.bankID, ActualAmount: 40_000, Resolution: "matched"},
		},
	})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}
	lines, err := store.New(l.db).ListReconciliationLines(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListReconciliationLines() = %v, want no error", err)
	}
	recorded := map[int64]int64{}
	for _, ln := range lines {
		recorded[ln.AccountID] = ln.RecordedAmount
		if ln.DifferenceAmount != 0 {
			t.Errorf("cek kas: account %d difference = %d, want 0", ln.AccountID, ln.DifferenceAmount)
		}
	}
	if recorded[f.cashID] != 440_000 || recorded[f.bankID] != 40_000 {
		t.Errorf("cek kas recorded = %v, want Cash 440000 and Bank 40000", recorded)
	}
}

// Every month, for the fund, each pos and each location: the walk foots, each
// month starts on the one before's end, and the bridge nets to nothing over the
// whole run (every payout is counted once and paid once).
func TestFrontedEveryWalkFootsAndTheBridgeNetsToZeroOverTheRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f
	months := []string{"2026-09", "2026-10", "2026-11", "2026-12"}

	views := map[string]ReportParams{
		"fund":      {},
		"Kas Utama": {PurposeID: int64Ptr(f.mainID)},
		"Titipan":   {PurposeID: int64Ptr(f.passID)},
		"Cash":      {AccountID: int64Ptr(f.cashID)},
		"Bank":      {AccountID: int64Ptr(f.bankID)},
	}
	for name, base := range views {
		var previousEnd, net money.Amount
		for i, month := range months {
			p := base
			p.FundID, p.Month = f.fundID, month
			w, err := l.at(frontedNow).MonthWalk(ctx, p)
			if err != nil {
				t.Fatalf("%s %s: MonthWalk() = %v, want no error", name, month, err)
			}
			if got := walkFootsMoved(t, w); got != w.End {
				t.Errorf("%s %s: foots to %d, End = %d (%+v)", name, month, got, w.End, w)
			}
			if i > 0 && w.Start != previousEnd {
				t.Errorf("%s %s: Start = %d, want the previous month's End %d", name, month, w.Start, previousEnd)
			}
			previousEnd = w.End
			net += bridge(w)
		}
		if net != 0 {
			t.Errorf("%s: the talangan lines net to %d over every month, want 0", name, net)
		}
	}
}

// Money cannot be repaid before it was fronted: a settle date before the
// claim's incurred_on is a typo, and the ledger refuses it by name (ADR-051).
// The fronting day itself is fine. A payout already stored that way (from
// before the rule) still foots - the two lines then read placed and paid
// outside the period - which the second half pins.
func TestSettlingBeforeTheFrontingDateIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-10-10", nil)
	c, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: f.fundID, MemberID: f.memberID, PurposeID: f.mainID, Amount: 5_000, IncurredOn: "2026-12-02", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}
	settle := func(on string) error {
		_, err := l.at(onTheDay("2026-12-05")).SettleReimbursement(ctx, SettleReimbursementParams{
			FundID: f.fundID, ReimbursementID: c.ID, AccountID: f.cashID, OccurredOn: on,
		})
		return err
	}
	if err := settle("2026-12-01"); !errors.Is(err, ErrSettledBeforeFronted) {
		t.Fatalf("settling the day before the fronting date = %v, want ErrSettledBeforeFronted", err)
	}
	if err := settle("2026-12-02"); err != nil {
		t.Fatalf("settling on the fronting date = %v, want no error", err)
	}

	// A legacy payout dated before its claim, written past the rule.
	old, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: f.fundID, MemberID: f.memberID, PurposeID: f.mainID, Amount: 3_000, IncurredOn: "2026-12-03", CreatedAt: 2,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID, Direction: string(DirectionOut), Amount: 3_000,
		OccurredOn: "2026-11-25", Kind: string(TransactionKindReimbursement), ReimbursementID: &old.ID, CreatedAt: 3,
	}); err != nil {
		t.Fatalf("CreateTransaction() = %v, want no error", err)
	}
	for _, month := range []string{"2026-11", "2026-12"} {
		w := monthlyReportAt(t, l, frontedNow, ReportParams{FundID: f.fundID, Month: month}).Walk
		if foots := walkFoots(t, w); foots != w.End {
			t.Errorf("%s: foots to %d, End = %d (%+v)", month, foots, w.End, w)
		}
	}
}

// The report's Talangan belum dibayar (ADR-051, #847) lists the claims behind
// OwedToMembers, by the same rule: owed as of the period's last day, so
// unfiltered they add up to the hero's figure. Settled, waived and
// fronted-after-the-period claims drop out; the pos and member filters narrow
// the list.
func TestReportOwedClaimsListWhatIsOwedAndAddUpToTheHero(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newFrontedScenario(t, l)
	f := s.f

	type claim struct {
		on      string
		amount  money.Amount
		purpose string
	}
	tests := []struct {
		name     string
		p        ReportParams
		filtered bool
		want     []claim
	}{
		// D unsettled; E waived only on 2 Nov; A and B paid in November; C
		// fronted in November.
		{"October", ReportParams{Month: "2026-10"}, false,
			[]claim{{"2026-10-20", 99_000, "Primary Cash"}, {"2026-10-21", 7_000, "Primary Cash"}, {"2026-10-27", 450_000, "Primary Cash"}, {"2026-10-28", 10_000, "Pass-through"}}},
		// Everything else settled or waived by the end of November.
		{"November", ReportParams{Month: "2026-11"}, false, []claim{{"2026-10-20", 99_000, "Primary Cash"}}},
		{"September, before any claim", ReportParams{Month: "2026-09"}, false, nil},
		{"the running month", ReportParams{Month: "2026-12"}, false, []claim{{"2026-10-20", 99_000, "Primary Cash"}}},
		// Through 12 Nov: A paid that day and E waived before it; B paid after.
		{"a range ending on A's payment", ReportParams{From: "2026-10-27", To: "2026-11-12"}, false,
			[]claim{{"2026-10-20", 99_000, "Primary Cash"}, {"2026-10-28", 10_000, "Pass-through"}}},
		{"October, one pos", ReportParams{Month: "2026-10", PurposeID: int64Ptr(f.passID)}, true, []claim{{"2026-10-28", 10_000, "Pass-through"}}},
		{"October, a member with no claim then", ReportParams{Month: "2026-10", MemberID: int64Ptr(s.budiID)}, true, nil},
		{"November, the member whose claim is open", ReportParams{Month: "2026-11", MemberID: int64Ptr(f.memberID)}, true, []claim{{"2026-10-20", 99_000, "Primary Cash"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.p
			p.FundID = f.fundID
			r := monthlyReportAt(t, l, frontedNow, p)
			var got []claim
			var sum money.Amount
			for _, c := range r.OwedClaims {
				got = append(got, claim{c.FrontedOn, c.Amount, c.PurposeName})
				sum += c.Amount
				if c.MemberName == "" {
					t.Errorf("claim on %s has no member name", c.FrontedOn)
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("claims = %+v, want %+v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("claim %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
			if !tt.filtered && sum != r.OwedToMembers {
				t.Errorf("claims add up to %d, the hero's Talangan is %d", sum, r.OwedToMembers)
			}
		})
	}
}

// #849: a talangan fronted before the fund's first transaction and settled
// after it places its payout in a month before the first transaction's. The
// report offers that month and its first day, so the row can be opened; an
// unsettled claim places no row and moves neither bound.
func TestReportOffersTheMonthOfATalanganFrontedBeforeTheFirstTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	now := jakartaAt(2026, time.October, 10, 12, 0, 0)

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 300_000, "2026-09-15", nil)
	bounds := func() (string, string) {
		t.Helper()
		months, err := l.at(now).ReportMonths(ctx, f.fundID)
		if err != nil {
			t.Fatalf("ReportMonths() = %v, want no error", err)
		}
		first, _, err := l.at(now).ReportDays(ctx, f.fundID)
		if err != nil {
			t.Fatalf("ReportDays() = %v, want no error", err)
		}
		return months[0], first
	}
	claim := func(amount int64, incurredOn string) store.Reimbursement {
		t.Helper()
		c, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
			FundID: f.fundID, MemberID: f.memberID, PurposeID: f.mainID, Amount: amount, IncurredOn: incurredOn, CreatedAt: 1,
		})
		if err != nil {
			t.Fatalf("CreateReimbursement(%s) = %v, want no error", incurredOn, err)
		}
		return c
	}

	// Unsettled, it is off the ledger: no row, no earlier month.
	early := claim(40_000, "2026-08-20")
	claim(5_000, "2026-07-01")
	if month, day := bounds(); month != "2026-09" || day != "2026-09-15" {
		t.Fatalf("with only unsettled claims before it, the report starts %s / %s, want 2026-09 / 2026-09-15", month, day)
	}

	if _, err := l.at(onTheDay("2026-09-20")).SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: early.ID, AccountID: f.cashID, OccurredOn: "2026-09-20",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}
	if month, day := bounds(); month != "2026-08" || day != "2026-08-20" {
		t.Errorf("the report starts %s / %s, want 2026-08 / 2026-08-20: the payout sits on 20 Aug", month, day)
	}

	// The month opens and holds the payout, and its walk foots.
	r := monthlyReportAt(t, l, now, ReportParams{FundID: f.fundID, Month: "2026-08"})
	if r.Months[0] != "2026-08" {
		t.Errorf("Months = %v, want August first", r.Months)
	}
	if len(r.Rows) != 1 || r.Rows[0].Date != "2026-08-20" || r.Rows[0].Entry == nil || r.Rows[0].Entry.Amount != 40_000 {
		t.Fatalf("August rows = %+v, want the payout on 20 Aug", r.Rows)
	}
	assertWalk(t, "August", r.Walk, ReportWalk{StartOn: "2026-07-31", EndOn: "2026-08-31", Out: 40_000, PaidLater: 40_000},
		ledgerSumThrough(t, l, f.fundID, "2026-08-31"), ledgerSumThrough(t, l, f.fundID, "2026-07-31"))

	// A range may start on that day.
	r = monthlyReportAt(t, l, now, ReportParams{FundID: f.fundID, From: "2026-08-20", To: "2026-08-31"})
	if r.From != "2026-08-20" || len(r.Rows) != 1 {
		t.Errorf("range from 20 Aug = from %s, %d rows; want from 2026-08-20 with the payout", r.From, len(r.Rows))
	}
}
