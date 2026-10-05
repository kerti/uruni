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
	post(sunatan.PurposeID, "out", 20_000, "2026-08-10", false, nil)
	misTagged := post(mainID, "out", 8_000, "2026-08-12", false, nil)
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: fundID, TransactionID: misTagged.ID, PurposeID: pass}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
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

	return walkScenario{fundID: fundID}
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
	l := newTestLedger(t)
	s := newWalkScenario(t, l)

	type shape struct {
		kind       string
		direction  string
		reversal   bool
		dues       bool
		fix        bool
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
		{kind: "adjustment", direction: "out", fix: true, wantLine: ReportLineAdjustment},
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
					(e.DuesPeriod != nil) == (sh.dues || sh.kind == "dues") && e.IsReconciliationFix == sh.fix {
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

// A member, direction or purpose filter has no balance to walk: only the two
// lines of the rows that matched, and the ends left at zero.
func TestMonthlyReportWalkUnderAFilterIsOnlyMasukAndKeluar(t *testing.T) {
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
		{"purpose main (its own walk is the next slice)", "2026-06", ReportParams{PurposeID: &mainID}, 125_000, 40_000, false, true},
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
	if _, err := classifyReportRow("transfer", "in", false); err == nil {
		t.Error("classifyReportRow(transfer) = nil error, want a refusal: transfers are folded or skipped before the switch")
	}
	if _, err := classifyReportRow("mystery", "in", false); err == nil {
		t.Error("classifyReportRow(mystery) = nil error, want a refusal rather than a guessed line")
	}
}
