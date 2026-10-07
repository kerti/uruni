package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// The month-as-a-walk trust claim (ADR-038): for every month, the balance the
// month started on, plus the lines of the rows dated in it, is the balance it
// ended on - to the rupiah, with no tolerance. The scenario below holds every
// shape of row the ledger can post, so a new shape the classification forgets
// breaks a month's footing instead of slipping through.

// walkScenario is one fund from June to the running month (October 2026, "today"
// being reportNow = 2 Oct), holding every row shape. Dates are the rows' own;
// where a month's figures are hand-worked the arithmetic is in want.
type walkScenario struct {
	fundID int64

	// The purposes the scenario walks: Kas Utama, an envelope that is closed
	// and rolled (Sunatan, whose first contribution is corrected away and back,
	// a correction that folds to nothing on display), another that stays open
	// (Arisan), and a pass-through (Titipan).
	mainID, sunatanID, arisanID, passID int64
}

func newWalkScenario(t *testing.T, l *Ledger) walkScenario {
	t.Helper()
	ctx := context.Background()
	q := store.New(l.db)

	// June, the setup month: two locations born with their opening balances.
	setup, err := l.SetUpFund(ctx, SetUpFundParams{FundName: "Walk Fund", Accounts: []AccountInput{
		{Kind: "cash", Name: "Tunai", OpeningBalance: &OpeningBalance{Amount: 500_000, OccurredOn: "2026-06-01"}},
		{Kind: "bank", Name: "Bank", OpeningBalance: &OpeningBalance{Amount: 200_000, OccurredOn: "2026-06-01"}},
	}})
	if err != nil {
		t.Fatalf("SetUpFund() = %v, want no error", err)
	}
	fundID, mainID := setup.Fund.ID, setup.MainPurposeID
	cashID, bankID := setup.CashAccountID(t), setup.BankAccountID(t)

	tierID := createDuesTier(t, q, fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	one := createDuesMember(t, q, fundID, duesMemberParams{name: "Member One", tierID: &tierID})
	two := createDuesMember(t, q, fundID, duesMemberParams{name: "Member Two"})
	pass := createPurpose(t, q, fundID, "pass_through", "Titipan")

	payDues := func(member int64, date, period string) store.Transaction {
		t.Helper()
		paid, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: fundID, AccountID: cashID, PurposeID: mainID, MemberID: member,
			OccurredOn: date, Periods: []PeriodAmount{{DuesPeriod: period, Amount: 25_000}},
		})
		if err != nil {
			t.Fatalf("PostDuesPayments(%s) = %v, want no error", date, err)
		}
		return paid[0]
	}
	reverse := func(tx store.Transaction, date string) {
		t.Helper()
		if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: fundID, TransactionID: tx.ID, OccurredOn: date}); err != nil {
			t.Fatalf("ReverseDuesPayment(%s) = %v, want no error", date, err)
		}
	}
	post := func(purpose int64, direction string, amount money.Amount, date string, adjustment bool, member *int64) store.Transaction {
		t.Helper()
		posted, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: fundID, AccountID: cashID, PurposeID: purpose, Direction: direction, Amount: amount,
			OccurredOn: date, IsAdjustment: adjustment, MemberID: member,
		})
		if err != nil {
			t.Fatalf("PostTransaction(%s %d on %s) = %v, want no error", direction, amount, date, err)
		}
		return posted
	}

	// June.
	post(mainID, "in", 100_000, "2026-06-05", false, nil)
	juneDues := payDues(one, "2026-06-10", "2026-06")
	post(mainID, "out", 40_000, "2026-06-12", false, nil)
	if _, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: fundID, PurposeID: mainID, FromAccountID: cashID, ToAccountID: bankID, Amount: 50_000, OccurredOn: "2026-06-20",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
	}

	// July: a June payment reversed in July, so July's Total masuk is negative;
	// and a talangan paid out.
	reverse(juneDues, "2026-07-08")
	post(mainID, "out", 5_000, "2026-07-15", false, nil)
	claim, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: fundID, MemberID: two, PurposeID: mainID, Amount: 30_000, IncurredOn: "2026-07-02", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}
	if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: fundID, ReimbursementID: claim.ID, AccountID: cashID, OccurredOn: "2026-07-20",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}

	// August: a location added mid-life with money already in it; a pass-through;
	// two envelopes, one closed and rolled into Kas Utama; a purpose correction
	// and a Pindah pos.
	if _, err := l.CreateAccount(ctx, CreateAccountParams{
		FundID: fundID, Kind: "bank", Name: "Bank B", OpeningBalance: &OpeningBalance{Amount: 150_000, OccurredOn: "2026-08-02"},
	}); err != nil {
		t.Fatalf("CreateAccount() = %v, want no error", err)
	}
	sunatan := openTestIncidental(t, l, fundID, "Sunatan", "2026-08-01")
	arisan := openTestIncidental(t, l, fundID, "Arisan", "2026-08-15")
	post(pass, "in", 60_000, "2026-08-04", false, nil)
	post(sunatan.PurposeID, "in", 80_000, "2026-08-06", false, &two)
	sunatanSpend := post(sunatan.PurposeID, "out", 20_000, "2026-08-10", false, nil)
	misTagged := post(mainID, "out", 8_000, "2026-08-12", false, nil)
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: fundID, TransactionID: misTagged.ID, PurposeID: pass}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}
	// The spending is tagged to Kas Utama and then back to Sunatan (a named
	// contribution cannot be corrected, an unnamed row can): two corrections of
	// one row, which the display folds to nothing (#280) but which post four
	// legs, and each leg moves a pos's balance.
	for _, to := range []int64{mainID, sunatan.PurposeID} {
		if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: fundID, TransactionID: sunatanSpend.ID, PurposeID: to}); err != nil {
			t.Fatalf("PostPurposeCorrection(%d) = %v, want no error", to, err)
		}
	}
	arisanGift := post(arisan.PurposeID, "in", 15_000, "2026-08-16", false, &two)
	if _, err := l.PostPurposeMove(ctx, PostPurposeMoveParams{
		FundID: fundID, FromPurposeID: mainID, ToPurposeID: sunatan.PurposeID, AccountID: cashID, Amount: 10_000, OccurredOn: "2026-08-18",
	}); err != nil {
		t.Fatalf("PostPurposeMove() = %v, want no error", err)
	}
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: fundID, PurposeID: sunatan.PurposeID, AccountID: cashID, ClosedOn: "2026-08-28",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	// September: standalone adjustments in and out; a contribution reversed in a
	// later month than it was given; a cek kas that fixes both ways, one as an
	// adjustment and one as an added entry.
	post(mainID, "in", 7_000, "2026-09-01", true, nil)
	post(mainID, "out", 3_000, "2026-09-02", true, nil)
	reverse(arisanGift, "2026-09-05")
	post(mainID, "in", 60_000, "2026-09-10", false, nil)
	payDues(one, "2026-09-12", "2026-09")
	cash, err := l.AccountBalance(ctx, fundID, cashID)
	if err != nil {
		t.Fatalf("AccountBalance(cash) = %v, want no error", err)
	}
	bank, err := l.AccountBalance(ctx, fundID, bankID)
	if err != nil {
		t.Fatalf("AccountBalance(bank) = %v, want no error", err)
	}
	if _, err := l.TakeReconciliation(ctx, TakeReconciliationParams{FundID: fundID, Counts: []AccountCount{
		{AccountID: cashID, ActualAmount: cash - 6_000, Resolution: "adjusted",
			Fix: &Fix{PurposeID: mainID, Direction: "out", Amount: 6_000, OccurredOn: "2026-09-25"}},
		{AccountID: bankID, ActualAmount: bank + 2_500, Resolution: "entry_added",
			Fix: &Fix{PurposeID: mainID, Direction: "in", Amount: 2_500, OccurredOn: "2026-09-25"}},
	}}); err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}
	post(mainID, "out", 11_000, "2026-09-28", false, nil)

	// October, the running month ("today" is 2 Oct): two rows from today or
	// before, and three dated after it, which the unbounded balance counts.
	post(mainID, "in", 12_000, "2026-10-01", false, nil)
	post(mainID, "out", 2_000, "2026-10-02", false, nil)
	post(mainID, "in", 9_000, "2026-10-20", false, nil)
	post(mainID, "out", 1_000, "2026-10-31", false, nil)
	post(mainID, "in", 500, "2026-10-25", true, nil)

	return walkScenario{fundID: fundID, mainID: mainID, sunatanID: sunatan.PurposeID, arisanID: arisan.PurposeID, passID: pass}
}

// ledgerSumThrough sums the fund's rows dated on or before through ("" for
// every row) by raw SQL, a second route to the balance that shares nothing
// with the report's own queries.
func ledgerSumThrough(t *testing.T, l *Ledger, fundID int64, through string) money.Amount {
	t.Helper()
	var sum int64
	err := l.db.QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0)
		FROM "transaction"
		WHERE fund_id = ? AND (? = '' OR occurred_on <= ?)`, fundID, through, through).Scan(&sum)
	if err != nil {
		t.Fatalf("summing the ledger through %q = %v, want no error", through, err)
	}
	return money.FromDB(sum)
}

// walkFoots is Start + Openings + In - Out + Adjustments, checked.
func walkFoots(t *testing.T, w ReportWalk) money.Amount {
	t.Helper()
	sum := w.Start
	var err error
	if sum, err = sum.Add(w.Openings); err != nil {
		t.Fatalf("Start + Openings: %v", err)
	}
	if sum, err = sum.Add(w.In); err != nil {
		t.Fatalf("+ In: %v", err)
	}
	if sum, err = sum.Sub(w.Out); err != nil {
		t.Fatalf("- Out: %v", err)
	}
	if sum, err = sum.Add(w.Adjustments); err != nil {
		t.Fatalf("+ Adjustments: %v", err)
	}
	return sum
}

func TestMonthlyReportWalkFootsEveryMonthOfAScenarioHoldingEveryRowShape(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	// Hand-worked, so the test is not the code agreeing with itself.
	//   Jun  0 + 700000 openings + (100000+25000) in - 40000 out            = 785000
	//   Jul  785000 + (-25000) in - (5000+30000) out                        = 725000
	//   Aug  725000 + 150000 openings + (60000+80000+15000) in - (20000+8000) out = 1002000
	//   Sep  1002000 + (-15000+60000+25000+2500) in - 11000 out + (7000-3000-6000) adj = 1061500
	//   Oct  1061500 + (12000+9000) in - (2000+1000) out + 500 adj          = 1080000
	months := []struct {
		month string
		want  ReportWalk
	}{
		{"2026-05", ReportWalk{StartOn: "2026-04-30", EndOn: "2026-05-31"}},
		{"2026-06", ReportWalk{StartOn: "2026-05-31", EndOn: "2026-06-30", Openings: 700_000, In: 125_000, Out: 40_000, End: 785_000}},
		{"2026-07", ReportWalk{StartOn: "2026-06-30", EndOn: "2026-07-31", Start: 785_000, In: -25_000, Out: 35_000, End: 725_000}},
		{"2026-08", ReportWalk{StartOn: "2026-07-31", EndOn: "2026-08-31", Start: 725_000, Openings: 150_000, In: 155_000, Out: 28_000, End: 1_002_000}},
		{"2026-09", ReportWalk{StartOn: "2026-08-31", EndOn: "2026-09-30", Start: 1_002_000, In: 72_500, Out: 11_000, Adjustments: -2_000, End: 1_061_500}},
		{"2026-10", ReportWalk{StartOn: "2026-09-30", EndOn: "2026-10-02", Start: 1_061_500, In: 21_000, Out: 3_000, Adjustments: 500, End: 1_080_000}},
	}

	var previousEnd money.Amount
	for i, m := range months {
		t.Run(m.month, func(t *testing.T) {
			r := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: m.month})
			want := m.want
			want.Full = true
			if r.Walk != want {
				t.Errorf("Walk = %+v, want %+v", r.Walk, want)
			}

			// The footing, to the rupiah.
			if got := walkFoots(t, r.Walk); got != r.Walk.End {
				t.Errorf("Start + Openings + In - Out + Adjustments = %d, End = %d", got, r.Walk.End)
			}
			// A month starts where the last one ended.
			if i > 0 && r.Walk.Start != previousEnd {
				t.Errorf("Start = %d, want the previous month's End %d", r.Walk.Start, previousEnd)
			}
			previousEnd = r.Walk.End

			// And both ends are the ledger's own sums, by a route of their own.
			if end := ledgerSumThrough(t, l, s.fundID, r.Walk.EndOn); !r.Running && end != r.Walk.End {
				t.Errorf("End = %d, but the ledger through %s sums to %d", r.Walk.End, r.Walk.EndOn, end)
			}
			if r.Running {
				if all := ledgerSumThrough(t, l, s.fundID, ""); all != r.Walk.End {
					t.Errorf("running End = %d, but the whole ledger sums to %d", r.Walk.End, all)
				}
				if fb, err := l.FundBalance(context.Background(), s.fundID); err != nil || fb != r.Walk.End {
					t.Errorf("running End = %d, FundBalance = %d (%v)", r.Walk.End, fb, err)
				}
			}
			if start := ledgerSumThrough(t, l, s.fundID, r.Walk.StartOn); start != r.Walk.Start {
				t.Errorf("Start = %d, but the ledger through %s sums to %d", r.Walk.Start, r.Walk.StartOn, start)
			}
			if r.Walk.End != r.Balance {
				t.Errorf("End = %d, want the header's Balance %d", r.Walk.End, r.Balance)
			}
		})
	}
}

// The scenario must really hold every shape, or the footing above proves less
// than it says.
func TestWalkScenarioHoldsEveryRowShapeOnItsOwnLine(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	type shape struct {
		kind       string
		direction  string
		reversal   bool
		dues       bool
		wantLine   ReportLine
		seenInMany int
	}
	shapes := []*shape{
		{kind: "opening", direction: "in", wantLine: ReportLineOpening},
		{kind: "normal", direction: "in", wantLine: ReportLineIn},
		{kind: "normal", direction: "out", wantLine: ReportLineOut},
		{kind: "dues", direction: "in", wantLine: ReportLineIn},
		{kind: "reimbursement", direction: "out", wantLine: ReportLineOut},
		{kind: "adjustment", direction: "out", reversal: true, dues: true, wantLine: ReportLineIn},
		{kind: "adjustment", direction: "out", reversal: true, wantLine: ReportLineIn},
		{kind: "adjustment", direction: "in", wantLine: ReportLineAdjustment},
		{kind: "adjustment", direction: "out", wantLine: ReportLineAdjustment},
	}
	var moveRows int
	for _, month := range []string{"2026-06", "2026-07", "2026-08", "2026-09", "2026-10"} {
		r := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: month})
		moveRows += len(moves(r.Rows))
		for _, e := range entries(r.Rows) {
			for _, sh := range shapes {
				if e.Kind == sh.kind && e.Direction == sh.direction && e.IsReversal == sh.reversal &&
					(e.DuesPeriod != nil) == (sh.dues || sh.kind == "dues") {
					sh.seenInMany++
					if e.Line != sh.wantLine {
						t.Errorf("%s: %+v lands on %q, want %q", month, e, e.Line, sh.wantLine)
					}
				}
			}
		}
	}
	for _, sh := range shapes {
		if sh.seenInMany == 0 {
			t.Errorf("the scenario holds no row of shape %+v", *sh)
		}
	}
	if moveRows == 0 {
		t.Error("the scenario shows no purpose move (a correction, a Pindah pos or a roll)")
	}
}

// A member or direction filter, with or without a purpose, has no balance to
// walk: only the two lines of the rows that matched, and the ends left at zero.
func TestMonthlyReportWalkUnderAFilterIsOnlyMasukAndKeluar(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)
	var mainID int64
	if err := l.db.QueryRowContext(context.Background(), `SELECT id FROM purpose WHERE fund_id = ? AND kind = 'main'`, s.fundID).Scan(&mainID); err != nil {
		t.Fatalf("finding Kas Utama: %v", err)
	}
	var memberTwo int64
	if err := l.db.QueryRowContext(context.Background(), `SELECT id FROM member WHERE fund_id = ? AND name = 'Member Two'`, s.fundID).Scan(&memberTwo); err != nil {
		t.Fatalf("finding Member Two: %v", err)
	}

	tests := []struct {
		name         string
		month        string
		p            ReportParams
		in, out      money.Amount
		wantNoOpens  bool
		wantNoAdjust bool
	}{
		{"direction in hides openings and adjustments", "2026-06", ReportParams{Direction: "in"}, 125_000, 0, true, true},
		{"direction in keeps a reversal as a minus", "2026-07", ReportParams{Direction: "in"}, -25_000, 0, true, true},
		{"direction out hides the reversal", "2026-07", ReportParams{Direction: "out"}, 0, 35_000, true, true},
		{"direction in, september", "2026-09", ReportParams{Direction: "in"}, 72_500, 0, true, true},
		{"member two", "2026-09", ReportParams{MemberID: &memberTwo}, -15_000, 0, true, true},
		{"member with a purpose still has no balance", "2026-09", ReportParams{MemberID: &memberTwo, PurposeID: &mainID}, 0, 0, true, true},
		{"direction with a purpose still has no balance", "2026-06", ReportParams{Direction: "in", PurposeID: &mainID}, 125_000, 0, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.p
			p.FundID, p.Month = s.fundID, tt.month
			r := monthlyReport(t, l, p)
			if r.Walk.Full {
				t.Errorf("Walk.Full = true under %+v, want only Total masuk and Total keluar", p)
			}
			if r.Walk.Start != 0 || r.Walk.End != 0 {
				t.Errorf("Start/End = %d/%d under a filter, want 0/0: a balance has no meaning for a slice", r.Walk.Start, r.Walk.End)
			}
			assertInOut(t, r.Walk, tt.in, tt.out)
			if tt.wantNoOpens && r.Walk.Openings != 0 {
				t.Errorf("Openings = %d, want 0: an opening is neither masuk nor keluar", r.Walk.Openings)
			}
			if tt.wantNoAdjust && r.Walk.Adjustments != 0 {
				t.Errorf("Adjustments = %d, want 0", r.Walk.Adjustments)
			}
			for _, e := range entries(r.Rows) {
				if (p.Direction != "" || p.MemberID != nil) && e.Line != ReportLineIn && e.Line != ReportLineOut {
					t.Errorf("row %+v is listed under a member or direction filter, want only masuk and keluar rows", e)
				}
				if p.Direction != "" && string(e.Line) != p.Direction {
					t.Errorf("row %+v (%q) is listed under direction %q", e, e.Line, p.Direction)
				}
			}
		})
	}
}

// An opening and a Penyesuaian are listed when nothing narrows the month.
func TestMonthlyReportListsOpeningsAndAdjustmentsUnfiltered(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	count := func(month string, line ReportLine) int {
		n := 0
		for _, e := range entries(monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: month}).Rows) {
			if e.Line == line {
				n++
			}
		}
		return n
	}
	if got := count("2026-06", ReportLineOpening); got != 2 {
		t.Errorf("June openings listed = %d, want 2 (cash and bank)", got)
	}
	if got := count("2026-08", ReportLineOpening); got != 1 {
		t.Errorf("August openings listed = %d, want 1 (the location added mid-life)", got)
	}
	if got := count("2026-09", ReportLineAdjustment); got != 3 {
		t.Errorf("September Penyesuaian listed = %d, want 3 (two standalone, one cek kas fix)", got)
	}
}

// The running month reads from its first day with no upper bound, so a row
// dated after today is both listed and in the walk.
func TestMonthlyReportRunningMonthIncludesRowsDatedAfterToday(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	r := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: "2026-10"})
	var dates []string
	for _, row := range r.Rows {
		dates = append(dates, row.Date)
	}
	want := []string{"2026-10-31", "2026-10-25", "2026-10-20", "2026-10-02", "2026-10-01"}
	if len(dates) != len(want) {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
	for i := range want {
		if dates[i] != want[i] {
			t.Fatalf("dates = %v, want %v", dates, want)
		}
	}

	// The same month, as a past one (a clock in November), stops where it ends:
	// the walk is the same, because nothing is dated beyond the month.
	past := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: "2026-10", Now: jakartaAt(2026, time.November, 3, 12, 0, 0)})
	if past.Running {
		t.Fatal("October is still the running month on 3 Nov")
	}
	if past.Walk.End != r.Walk.End || past.Walk.In != r.Walk.In {
		t.Errorf("October as a past month = %+v, want the running month's %+v", past.Walk, r.Walk)
	}
}

// A reversal in a month with nothing else in it takes Total masuk below zero,
// and an adjustment is signed. Both are accepted by ADR-038, and both foot.
func TestMonthlyReportWalkAllowsANegativeTotalMasuk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	paid, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID, MemberID: f.memberID,
		OccurredOn: "2026-08-05", Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, TransactionID: paid[0].ID, OccurredOn: "2026-09-02"}); err != nil {
		t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})
	want := ReportWalk{Full: true, StartOn: "2026-08-31", EndOn: "2026-09-30", Start: 25_000, In: -25_000, End: 0}
	if r.Walk != want {
		t.Errorf("Walk = %+v, want %+v", r.Walk, want)
	}
}

func TestClassifyReportRowRefusesAKindItHasNoLineFor(t *testing.T) {
	t.Parallel()
	if _, err := classifyReportRow("transfer", "in", false); err == nil {
		t.Error("classifyReportRow(transfer) = nil error, want a refusal: transfers are folded or skipped before the switch")
	}
	if _, err := classifyReportRow("mystery", "in", false); err == nil {
		t.Error("classifyReportRow(mystery) = nil error, want a refusal rather than a guessed line")
	}
}

// purposeLedgerSum sums one purpose's rows by raw SQL, through a day ("" for
// every row): a route to the pos's balance that shares nothing with the
// report's own queries.
func purposeLedgerSum(t *testing.T, l *Ledger, fundID, purposeID int64, through string) money.Amount {
	t.Helper()
	var sum int64
	err := l.db.QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(CASE direction WHEN 'in' THEN amount ELSE -amount END), 0)
		FROM "transaction"
		WHERE fund_id = ? AND purpose_id = ? AND (? = '' OR occurred_on <= ?)`, fundID, purposeID, through, through).Scan(&sum)
	if err != nil {
		t.Fatalf("summing purpose %d through %q = %v, want no error", purposeID, through, err)
	}
	return money.FromDB(sum)
}

// purposeMovedInMonth sums the raw reclass_purpose legs on one purpose dated in
// a month ("YYYY-MM"), and counts how many there were.
func purposeMovedInMonth(t *testing.T, l *Ledger, fundID, purposeID int64, month string) (money.Amount, int) {
	t.Helper()
	var sum int64
	var n int
	err := l.db.QueryRowContext(context.Background(), `
		SELECT COALESCE(SUM(CASE t.direction WHEN 'in' THEN t.amount ELSE -t.amount END), 0), COUNT(*)
		FROM "transaction" t JOIN transfer tr ON tr.id = t.transfer_id
		WHERE t.fund_id = ? AND t.purpose_id = ? AND tr.kind = 'reclass_purpose' AND substr(t.occurred_on, 1, 7) = ?`,
		fundID, purposeID, month).Scan(&sum, &n)
	if err != nil {
		t.Fatalf("summing moved legs of purpose %d in %s = %v, want no error", purposeID, month, err)
	}
	return money.FromDB(sum), n
}

// walkFootsMoved is walkFoots plus Moved, the purpose walk's own line.
func walkFootsMoved(t *testing.T, w ReportWalk) money.Amount {
	t.Helper()
	sum, err := walkFoots(t, w).Add(w.Moved)
	if err != nil {
		t.Fatalf("+ Moved: %v", err)
	}
	return sum
}

// Every month is walked again for each pos on its own (ADR-038): Kas Utama, a
// closed envelope that rolled into it, an open one, and a Titipan. The pos's
// Start and End are its own bounded balances, Dipindah is the net of its raw
// reclass legs, and the poses together make the fund's walk.
func TestMonthlyReportWalkFootsEveryMonthForEveryPurpose(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	// Hand-worked, one pos at a time. Dipindah is the raw legs: in August
	//   Kas Utama  +8.000 (the 8.000 spent on the wrong pos, corrected to Titipan)
	//              -10.000 (Pindah pos to Sunatan) +70.000 (Sunatan's leftover rolled in) = +68.000
	//   Sunatan    +10.000 - 70.000 = -60.000; the 20.000 spending tagged away and back
	//              posted four legs (two here) that cancel
	//   Titipan    -8.000
	type W = ReportWalk
	byPurpose := map[string][]struct {
		month string
		want  W
	}{
		"Kas Utama": {
			{"2026-05", W{}},
			{"2026-06", W{Openings: 700_000, In: 125_000, Out: 40_000, End: 785_000}},
			{"2026-07", W{Start: 785_000, In: -25_000, Out: 35_000, End: 725_000}},
			{"2026-08", W{Start: 725_000, Openings: 150_000, Out: 8_000, Moved: 68_000, End: 935_000}},
			{"2026-09", W{Start: 935_000, In: 87_500, Out: 11_000, Adjustments: -2_000, End: 1_009_500}},
			{"2026-10", W{Start: 1_009_500, In: 21_000, Out: 3_000, Adjustments: 500, End: 1_028_000}},
		},
		"Sunatan": {
			{"2026-07", W{}},
			{"2026-08", W{In: 80_000, Out: 20_000, Moved: -60_000, End: 0}},
			{"2026-09", W{}},
			{"2026-10", W{}},
		},
		"Arisan": {
			{"2026-08", W{In: 15_000, End: 15_000}},
			{"2026-09", W{Start: 15_000, In: -15_000, End: 0}},
			{"2026-10", W{}},
		},
		"Titipan": {
			{"2026-06", W{}},
			{"2026-08", W{In: 60_000, Moved: -8_000, End: 52_000}},
			{"2026-09", W{Start: 52_000, End: 52_000}},
			{"2026-10", W{Start: 52_000, End: 52_000}},
		},
	}
	ids := map[string]int64{"Kas Utama": s.mainID, "Sunatan": s.sunatanID, "Arisan": s.arisanID, "Titipan": s.passID}
	months := []string{"2026-05", "2026-06", "2026-07", "2026-08", "2026-09", "2026-10"}

	// Each pos's End by month, to compare against the next Start and the fund.
	ends := map[string]map[string]money.Amount{}
	for name, id := range ids {
		ends[name] = map[string]money.Amount{}
		wantByMonth := map[string]W{}
		for _, m := range byPurpose[name] {
			wantByMonth[m.month] = m.want
		}
		var previousEnd money.Amount
		for i, month := range months {
			t.Run(name+"/"+month, func(t *testing.T) {
				id := id
				r := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: month, PurposeID: &id})
				got := r.Walk

				want := wantByMonth[month]
				if _, listed := wantByMonth[month]; !listed {
					// A month the table leaves out is a quiet one: the balance
					// carried through, no lines. Its figures are asserted by the
					// footing and the ledger sums below.
					want = W{Start: got.Start, End: got.End}
				}
				want.Full = true
				want.StartOn, want.EndOn = r.Walk.StartOn, r.Walk.EndOn
				if got != want {
					t.Errorf("Walk = %+v, want %+v", got, want)
				}

				// The footing, to the rupiah, with Dipindah on the line.
				if foots := walkFootsMoved(t, got); foots != got.End {
					t.Errorf("Start + Openings + In - Out + Adjustments + Moved = %d, End = %d", foots, got.End)
				}
				// A month starts where the last one ended.
				if i > 0 && got.Start != previousEnd {
					t.Errorf("Start = %d, want the previous month's End %d", got.Start, previousEnd)
				}
				previousEnd = got.End
				ends[name][month] = got.End

				// Both ends are the pos's own ledger sums by a second route, and
				// Dipindah is the raw legs (not the folded display rows).
				if start := purposeLedgerSum(t, l, s.fundID, id, got.StartOn); start != got.Start {
					t.Errorf("Start = %d, but the ledger through %s sums to %d for this pos", got.Start, got.StartOn, start)
				}
				endThrough := got.EndOn
				if r.Running {
					endThrough = ""
				}
				if end := purposeLedgerSum(t, l, s.fundID, id, endThrough); end != got.End {
					t.Errorf("End = %d, but the ledger sums to %d for this pos", got.End, end)
				}
				if moved, _ := purposeMovedInMonth(t, l, s.fundID, id, month); moved != got.Moved {
					t.Errorf("Moved = %d, but the raw reclass legs of this pos in %s sum to %d", got.Moved, month, moved)
				}
				// Openings only ever land on Kas Utama.
				if name != "Kas Utama" && got.Openings != 0 {
					t.Errorf("Openings = %d on %s, want 0: an opening is always Kas Utama's", got.Openings, name)
				}
				// And its End is the figure the header gives this pos.
				for _, pb := range r.PurposeBalances {
					if pb.PurposeID == id && pb.Balance != got.End {
						t.Errorf("End = %d, but the header's Saldo per pos line for %s reads %d", got.End, name, pb.Balance)
					}
				}
			})
		}
	}

	// The poses together are the fund: every month's Start and End sum to the
	// whole fund's, so a purpose that lost a row to no line would show here.
	for _, month := range months {
		fund := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: month})
		var start, end money.Amount
		for name, id := range ids {
			id := id
			r := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: month, PurposeID: &id})
			start += r.Walk.Start
			end += r.Walk.End
			if r.Walk.End != ends[name][month] {
				t.Errorf("%s %s: End drifted between runs", name, month)
			}
		}
		if start != fund.Walk.Start || end != fund.Walk.End {
			t.Errorf("%s: the poses sum to %d -> %d, the fund walks %d -> %d", month, start, end, fund.Walk.Start, fund.Walk.End)
		}
	}
}

// A correction that folds to nothing on display still posted legs, and each one
// moved a pos's balance. Netted over the pair they cancel on every pos, so
// Dipindah stays true to the balance whichever way it is summed - but it is the
// raw legs that make it so, not the display.
func TestPurposeWalkMovedCountsLegsADisplayFoldsAway(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	r := monthlyReport(t, l, ReportParams{FundID: s.fundID, Month: "2026-08", PurposeID: &s.sunatanID})
	for _, m := range moves(r.Rows) {
		if m.IsCorrection {
			t.Errorf("Sunatan lists a correction %+v, want the away-and-back pair folded to nothing", m)
		}
	}
	if len(moves(r.Rows)) != 2 {
		t.Errorf("Sunatan lists %d moves, want 2 (the Pindah pos in, the roll out)", len(moves(r.Rows)))
	}

	// Sunatan carries two correction legs (out when tagged away, in when tagged
	// back), plus the allocation and the roll: four raw legs.
	moved, legs := purposeMovedInMonth(t, l, s.fundID, s.sunatanID, "2026-08")
	if legs != 4 {
		t.Errorf("raw reclass legs on Sunatan in August = %d, want 4", legs)
	}
	if moved != r.Walk.Moved || moved != -60_000 {
		t.Errorf("Moved = %d, raw legs sum to %d, want both -60000", r.Walk.Moved, moved)
	}
}
