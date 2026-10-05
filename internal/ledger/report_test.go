package ledger

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// The report's trust claims, tested the way the rest of the ledger is: against
// the real schema, with exact integers and no tolerance (ADR-015, ADR-028).

// --- helpers local to this file ----------------------------------------------

func jakartaAt(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, tz.Jakarta)
}

// reportNow is a fixed "now" after every date the scenarios post on, so the
// current month is October 2026 unless a test says otherwise.
var reportNow = jakartaAt(2026, time.October, 2, 12, 0, 0)

func int64Ptr(v int64) *int64 { return &v }

// postEntry posts one kind='normal' (or, with adjustment, kind='adjustment')
// row and returns it.
func postEntry(t *testing.T, l *Ledger, fundID, accountID, purposeID int64, direction string, amount money.Amount, date string, note *string) store.Transaction {
	t.Helper()
	posted, err := l.PostTransaction(context.Background(), PostTransactionParams{
		FundID: fundID, AccountID: accountID, PurposeID: purposeID,
		Direction: direction, Amount: amount, OccurredOn: date, Note: note,
	})
	if err != nil {
		t.Fatalf("PostTransaction(%s %d on %s) = %v, want no error", direction, amount, date, err)
	}
	return posted
}

func monthlyReport(t *testing.T, l *Ledger, p ReportParams) Report {
	t.Helper()
	if p.Now.IsZero() {
		p.Now = reportNow
	}
	r, err := l.MonthlyReport(context.Background(), p)
	if err != nil {
		t.Fatalf("MonthlyReport(%+v) = %v, want no error", p, err)
	}
	return r
}

func entries(rows []ReportRow) []ReportEntry {
	var out []ReportEntry
	for _, r := range rows {
		if r.Entry != nil {
			out = append(out, *r.Entry)
		}
	}
	return out
}

func moves(rows []ReportRow) []ReportMove {
	var out []ReportMove
	for _, r := range rows {
		if r.Move != nil {
			out = append(out, *r.Move)
		}
	}
	return out
}

// assertInOut checks the walk's Total masuk and Total keluar, the two lines
// every filter keeps; the rest of the walk has its own tests (report_walk_test.go).
func assertInOut(t *testing.T, got ReportWalk, in, out money.Amount) {
	t.Helper()
	if got.In != in || got.Out != out {
		t.Errorf("Walk In/Out = %d/%d, want %d/%d", got.In, got.Out, in, out)
	}
}

// secondFund is a second fund with its own account, purposes and member, all
// carrying rows - so a leak shows up as a figure, not as a missing assertion.
type secondFund struct {
	fundID, cashID, mainID, memberID int64
}

func newSecondFund(t *testing.T, l *Ledger) secondFund {
	t.Helper()
	q := store.New(l.db)
	other, err := q.CreateFund(context.Background(), store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	s := secondFund{fundID: other.ID}
	s.cashID = createAccount(t, q, other.ID, "cash", "Cash")
	s.mainID = createPurpose(t, q, other.ID, "main", "Other Main")
	s.memberID = createDuesMember(t, q, other.ID, duesMemberParams{name: "Other Member"})
	return s
}

// monthScenario is September 2026 in one fund, built to give every filter
// something to include and something to exclude:
//
//	in   200_000 main  donation (Sep 3)
//	in    25_000 main  dues Member One 2026-09, paid in full (Sep 5)
//	in    10_000 main  dues Member Two 2026-09, partial (Sep 6)
//	out   60_000 main  with a receipt (Sep 12)
//	out   20_000 pass  (Sep 15)
//	xfer  30_000 cash -> bank, hidden (Sep 20)
//
// plus one row in August that must never appear in September.
type monthScenario struct {
	f                                 fixture
	memberOne, memberTwo, memberThree int64
}

func newMonthScenario(t *testing.T, l *Ledger) monthScenario {
	t.Helper()
	ctx := context.Background()
	q := store.New(l.db)
	f := newFixture(t, l)

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	s := monthScenario{f: f}
	s.memberOne = createDuesMember(t, q, f.fundID, duesMemberParams{name: "Member One", tierID: &tierID})
	s.memberTwo = createDuesMember(t, q, f.fundID, duesMemberParams{name: "Member Two", tierID: &tierID})
	s.memberThree = createDuesMember(t, q, f.fundID, duesMemberParams{name: "Member Three", tierID: &tierID})

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 10_000, "2026-08-20", nil)

	donation := strPtr("Test donation")
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 200_000, "2026-09-03", donation)
	for _, d := range []struct {
		member int64
		amount money.Amount
		date   string
	}{{s.memberOne, 25_000, "2026-09-05"}, {s.memberTwo, 10_000, "2026-09-06"}} {
		if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID, MemberID: d.member,
			OccurredOn: d.date, Periods: []PeriodAmount{{DuesPeriod: "2026-09", Amount: d.amount}},
		}); err != nil {
			t.Fatalf("PostDuesPayments() = %v, want no error", err)
		}
	}
	withReceipt := postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 60_000, "2026-09-12", nil)
	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: f.fundID, TransactionID: &withReceipt.ID, Path: "private-receipt-path.jpg", UploadedAt: 1,
	}); err != nil {
		t.Fatalf("CreateReceipt() = %v, want no error", err)
	}
	postEntry(t, l, f.fundID, f.cashID, f.passID, "out", 20_000, "2026-09-15", nil)

	if _, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID, FromAccountID: f.cashID, ToAccountID: f.bankID,
		Amount: 30_000, OccurredOn: "2026-09-20",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
	}
	return s
}

// --- header ---------------------------------------------------------------------

func TestMonthlyReportEmptyFundShowsAHeaderAtZeroAndTheCurrentMonthOnly(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID})

	if r.FundName != "Test Fund" {
		t.Errorf("FundName = %q, want %q", r.FundName, "Test Fund")
	}
	if r.Balance != 0 {
		t.Errorf("Balance = %d, want 0", r.Balance)
	}
	if r.Reconciliation != nil {
		t.Errorf("Reconciliation = %+v, want nil: a fund nobody has counted does not match", r.Reconciliation)
	}
	if r.Month != "2026-10" || len(r.Months) != 1 || r.Months[0] != "2026-10" {
		t.Errorf("Month = %q, Months = %v, want the current month alone", r.Month, r.Months)
	}
	if r.AsOf != "2026-10-02" {
		t.Errorf("AsOf = %q, want 2026-10-02", r.AsOf)
	}
	if len(r.Rows) != 0 {
		t.Errorf("Rows = %d, want none", len(r.Rows))
	}
	assertInOut(t, r.Walk, 0, 0)
	if len(r.Dues) != 0 || len(r.Envelopes) != 0 {
		t.Errorf("Dues = %d, Envelopes = %d, want none of either", len(r.Dues), len(r.Envelopes))
	}
	if len(r.PurposeBalances) == 0 || r.PurposeBalances[0].Kind != "main" {
		t.Fatalf("PurposeBalances = %+v, want Kas Utama first", r.PurposeBalances)
	}
	for _, b := range r.PurposeBalances {
		if b.Balance != 0 {
			t.Errorf("PurposeBalances[%q] = %d, want 0", b.Name, b.Balance)
		}
	}
}

func TestMonthlyReportHeaderBalanceIsTheLedgersAndEachPurposeSumsToIt(t *testing.T) {
	l := newTestLedger(t)
	s := newMonthScenario(t, l)

	r := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09"})

	// 10_000 + 200_000 + 25_000 + 10_000 - 60_000 - 20_000; the transfer is
	// value-neutral.
	if r.Balance != 165_000 {
		t.Errorf("Balance = %d, want 165000", r.Balance)
	}
	want := map[string]money.Amount{"Primary Cash": 185_000, "Pass-through": -20_000}
	var sum money.Amount
	for _, b := range r.PurposeBalances {
		sum += b.Balance
		if w, ok := want[b.Name]; ok && b.Balance != w {
			t.Errorf("PurposeBalances[%q] = %d, want %d", b.Name, b.Balance, w)
		}
	}
	if sum != r.Balance {
		t.Errorf("purpose balances sum to %d, want the fund balance %d", sum, r.Balance)
	}
}

func TestMonthlyReportReconciliationNeverCountedMatchedAndDifference(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	q := store.New(l.db)
	f := newFixture(t, l)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-09-03", nil)

	// 2026-09-30 17:30 UTC is 2026-10-01 00:30 in Jakarta: the report names the
	// treasurer's day, not the server's.
	performedAt := time.Date(2026, 9, 30, 17, 30, 0, 0, time.UTC).Unix()
	rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: performedAt, CreatedAt: performedAt,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation() = %v, want no error", err)
	}
	line := func(accountID int64, recorded, actual int64, resolution string) {
		t.Helper()
		if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
			FundID: f.fundID, ReconciliationID: rec.ID, AccountID: accountID,
			RecordedAmount: recorded, ActualAmount: actual, DifferenceAmount: actual - recorded, Resolution: resolution,
		}); err != nil {
			t.Fatalf("CreateReconciliationLine() = %v, want no error", err)
		}
	}

	line(f.cashID, 100_000, 100_000, "matched")
	r := monthlyReport(t, l, ReportParams{FundID: f.fundID})
	if r.Reconciliation == nil || !r.Reconciliation.Matched || r.Reconciliation.Difference != 0 {
		t.Fatalf("Reconciliation = %+v, want matched with no difference", r.Reconciliation)
	}
	if r.Reconciliation.Date != "2026-10-01" {
		t.Errorf("Reconciliation.Date = %q, want 2026-10-01 (Jakarta)", r.Reconciliation.Date)
	}

	// A short location and an over one must not cancel into "matched": the
	// difference is the sum of magnitudes, as the home banner computes it.
	rec2, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: performedAt + 60, CreatedAt: performedAt + 60,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation() #2 = %v, want no error", err)
	}
	rec = rec2
	line(f.cashID, 100_000, 95_000, "left_open")
	line(f.bankID, 0, 3_000, "left_open")

	r = monthlyReport(t, l, ReportParams{FundID: f.fundID})
	if r.Reconciliation == nil || r.Reconciliation.Matched || r.Reconciliation.Difference != 8_000 {
		t.Fatalf("Reconciliation = %+v, want a difference of 8000 (5000 short + 3000 over)", r.Reconciliation)
	}
}

func TestMonthlyReportReconciliationAdjustingEntryIsFlaggedAndCounted(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-09-03", nil)

	today := time.Now().In(tz.Jakarta).Format("2006-01-02")
	if _, err := l.TakeReconciliation(ctx, TakeReconciliationParams{
		FundID: f.fundID,
		Counts: []AccountCount{{
			AccountID: f.cashID, ActualAmount: 97_000, Resolution: "adjusted",
			Fix: &Fix{PurposeID: f.mainID, Direction: "out", Amount: 3_000, OccurredOn: today},
		}},
	}); err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}

	month := today[:7]
	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: month, Now: time.Now()})

	var fix *ReportEntry
	for _, e := range entries(r.Rows) {
		e := e
		if e.IsReconciliationFix {
			fix = &e
		}
	}
	if fix == nil {
		t.Fatalf("no row flagged IsReconciliationFix in %d rows", len(r.Rows))
	}
	if fix.Kind != "adjustment" || fix.Direction != "out" || fix.Amount != 3_000 {
		t.Errorf("fix = %+v, want an outgoing adjustment of 3000", *fix)
	}
	if r.Reconciliation == nil || !r.Reconciliation.Matched {
		t.Errorf("Reconciliation = %+v, want matched: the adjusted line is resolved", r.Reconciliation)
	}
	if r.Balance != 97_000 {
		t.Errorf("Balance = %d, want 97000", r.Balance)
	}
}

// --- transfers ------------------------------------------------------------------

func TestMonthlyReportLocationTransferIsHiddenAndInNoTotal(t *testing.T) {
	l := newTestLedger(t)
	s := newMonthScenario(t, l)

	r := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09"})

	for _, row := range r.Rows {
		if row.Date == "2026-09-20" {
			t.Errorf("a row dated on the transfer's day is shown: %+v", row)
		}
	}
	if len(moves(r.Rows)) != 0 {
		t.Errorf("moves = %d, want 0: a location transfer is not a purpose move", len(moves(r.Rows)))
	}
	// Both legs would have added 30_000 to in and to out; neither is counted.
	assertInOut(t, r.Walk, 235_000, 80_000)
	if len(r.Rows) != 5 {
		t.Errorf("Rows = %d, want 5 (donation, two dues, two outgoing)", len(r.Rows))
	}
}

func TestMonthlyReportPurposeCorrectionIsOneMoveRowMatchedOnEitherSide(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)

	posted := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 50_000, "2026-09-10", strPtr("Test row"))
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.passID,
	}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	tests := []struct {
		name        string
		purposeID   *int64
		wantEntries int
		wantMoves   int
		wantIn      money.Amount
	}{
		{"no filter", nil, 1, 1, 50_000},
		{"filter on the from side", int64Ptr(f.mainID), 1, 1, 50_000},
		// The stored tag of the row is main; the move names pass on its to side.
		{"filter on the to side", int64Ptr(f.passID), 0, 1, 0},
		{"filter on neither side", int64Ptr(f.incidenID), 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", PurposeID: tt.purposeID})
			if got := len(entries(r.Rows)); got != tt.wantEntries {
				t.Errorf("entries = %d, want %d", got, tt.wantEntries)
			}
			mv := moves(r.Rows)
			if len(mv) != tt.wantMoves {
				t.Fatalf("moves = %d, want %d (the pair is two ledger rows but one report row)", len(mv), tt.wantMoves)
			}
			if tt.wantMoves == 1 {
				m := mv[0]
				if m.Amount != 50_000 || m.FromPurposeName != "Primary Cash" || m.ToPurposeName != "Pass-through" || !m.IsCorrection {
					t.Errorf("move = %+v, want 50000 from Primary Cash to Pass-through as a correction", m)
				}
			}
			assertInOut(t, r.Walk, tt.wantIn, 0)
		})
	}

	// A move is neither in nor out and names no member.
	if r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", Direction: "in"}); len(moves(r.Rows)) != 0 {
		t.Errorf("direction filter left %d moves, want 0", len(moves(r.Rows)))
	}
	if r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", MemberID: int64Ptr(1)}); len(moves(r.Rows)) != 0 {
		t.Errorf("member filter left %d moves, want 0", len(moves(r.Rows)))
	}

	// The ledger still nets to the same balance and the purposes moved.
	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})
	if r.Balance != 50_000 {
		t.Errorf("Balance = %d, want 50000: a move changes no balance", r.Balance)
	}
}

func TestMonthlyReportEnvelopeRollIsOneMoveRowOutsideTheTotals(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)

	envelope := openTestIncidental(t, l, f.fundID, "Test Collection", "2026-09-01")
	postEntry(t, l, f.fundID, f.cashID, envelope.PurposeID, "in", 100_000, "2026-09-05", nil)
	postEntry(t, l, f.fundID, f.cashID, envelope.PurposeID, "out", 30_000, "2026-09-10", nil)
	rolled, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-09-25",
	})
	if err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}
	if rolled != 70_000 {
		t.Fatalf("rolled = %d, want 70000", rolled)
	}

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})

	mv := moves(r.Rows)
	if len(mv) != 1 {
		t.Fatalf("moves = %d, want 1", len(mv))
	}
	if m := mv[0]; m.Amount != 70_000 || m.FromPurposeName != "Test Collection" || m.ToPurposeName != "Primary Cash" || m.IsCorrection || m.IsAllocation {
		t.Errorf("move = %+v, want 70000 from Test Collection to Primary Cash, neither a correction nor an allocation", m)
	}
	// The roll's 70_000 is in neither total: the envelope's own 100_000 in and
	// 30_000 out are all the month moved.
	assertInOut(t, r.Walk, 100_000, 30_000)
	if r.Balance != 70_000 {
		t.Errorf("Balance = %d, want 70000", r.Balance)
	}

	// Filtering on the envelope matches the move through its from side.
	r = monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", PurposeID: &envelope.PurposeID})
	if len(moves(r.Rows)) != 1 {
		t.Errorf("moves under the envelope filter = %d, want 1", len(moves(r.Rows)))
	}
	// ...and filtering on Kas Utama through its to side, with no entry of its own.
	r = monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", PurposeID: &f.mainID})
	if len(moves(r.Rows)) != 1 || len(entries(r.Rows)) != 0 {
		t.Errorf("under the Kas Utama filter: moves = %d, entries = %d, want 1 and 0", len(moves(r.Rows)), len(entries(r.Rows)))
	}
	assertInOut(t, r.Walk, 0, 0)
}

// --- rows, totals and filters ---------------------------------------------------

func TestMonthlyReportTotalsUnderEachFilter(t *testing.T) {
	l := newTestLedger(t)
	s := newMonthScenario(t, l)
	f := s.f

	tests := []struct {
		name     string
		p        ReportParams
		wantRows int
		in, out  money.Amount
	}{
		{"no filter", ReportParams{}, 5, 235_000, 80_000},
		{"direction in", ReportParams{Direction: "in"}, 3, 235_000, 0},
		{"direction out", ReportParams{Direction: "out"}, 2, 0, 80_000},
		{"purpose main", ReportParams{PurposeID: &f.mainID}, 4, 235_000, 60_000},
		{"purpose pass-through", ReportParams{PurposeID: &f.passID}, 1, 0, 20_000},
		{"purpose and direction", ReportParams{PurposeID: &f.mainID, Direction: "out"}, 1, 0, 60_000},
		{"member one", ReportParams{MemberID: &s.memberOne}, 1, 25_000, 0},
		{"member two", ReportParams{MemberID: &s.memberTwo}, 1, 10_000, 0},
		{"member with no rows", ReportParams{MemberID: &s.memberThree}, 0, 0, 0},
		{"member and wrong direction", ReportParams{MemberID: &s.memberOne, Direction: "out"}, 0, 0, 0},
		{"unknown purpose", ReportParams{PurposeID: int64Ptr(999_999)}, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.p
			p.FundID, p.Month = f.fundID, "2026-09"
			r := monthlyReport(t, l, p)
			if len(r.Rows) != tt.wantRows {
				t.Errorf("Rows = %d, want %d", len(r.Rows), tt.wantRows)
			}
			assertInOut(t, r.Walk, tt.in, tt.out)
		})
	}
}

func TestMonthlyReportRowsAreTheMonthsOnlyNewestFirstWithTheirFacts(t *testing.T) {
	l := newTestLedger(t)
	s := newMonthScenario(t, l)

	r := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09"})

	var dates []string
	for _, row := range r.Rows {
		dates = append(dates, row.Date)
	}
	wantDates := []string{"2026-09-15", "2026-09-12", "2026-09-06", "2026-09-05", "2026-09-03"}
	if len(dates) != len(wantDates) {
		t.Fatalf("dates = %v, want %v", dates, wantDates)
	}
	for i := range dates {
		if dates[i] != wantDates[i] {
			t.Fatalf("dates = %v, want %v", dates, wantDates)
		}
	}

	byDate := map[string]ReportEntry{}
	for _, row := range r.Rows {
		byDate[row.Date] = *row.Entry
	}

	dues := byDate["2026-09-05"]
	if dues.Kind != "dues" || dues.MemberName == nil || *dues.MemberName != "Member One" ||
		dues.DuesPeriod == nil || *dues.DuesPeriod != "2026-09" || dues.IsReversal {
		t.Errorf("dues row = %+v, want kind dues, Member One, period 2026-09", dues)
	}
	if got := byDate["2026-09-03"]; got.Note == nil || *got.Note != "Test donation" || got.MemberName != nil || got.HasReceipt {
		t.Errorf("donation row = %+v, want its note, no member, no receipt", got)
	}
	if got := byDate["2026-09-12"]; !got.HasReceipt || got.Direction != "out" || got.Amount != 60_000 {
		t.Errorf("receipt row = %+v, want an outgoing 60000 with a receipt", got)
	}
	if got := byDate["2026-09-15"]; got.HasReceipt || got.PurposeName != "Pass-through" {
		t.Errorf("pass-through row = %+v, want no receipt and the Pass-through tag", got)
	}
}

func TestMonthlyReportDuesReversalIsShownAndNetsAgainstThePaymentInTotalMasuk(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	s := newMonthScenario(t, l)

	paid, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: s.f.fundID, AccountID: s.f.cashID, PurposeID: s.f.mainID, MemberID: s.memberThree,
		OccurredOn: "2026-09-21", Periods: []PeriodAmount{{DuesPeriod: "2026-09", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: s.f.fundID, TransactionID: paid[0].ID, OccurredOn: "2026-09-22",
	}); err != nil {
		t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09", MemberID: &s.memberThree})

	es := entries(r.Rows)
	if len(es) != 2 {
		t.Fatalf("rows for the member = %d, want the payment and its reversal", len(es))
	}
	var reversals int
	for _, e := range es {
		if e.IsReversal {
			reversals++
			if e.DuesPeriod == nil || *e.DuesPeriod != "2026-09" || e.Direction != "out" {
				t.Errorf("reversal = %+v, want an outgoing row carrying period 2026-09", e)
			}
		}
	}
	if reversals != 1 {
		t.Errorf("reversals = %d, want 1", reversals)
	}
	// ADR-038: the reversal lands on Total masuk as a minus, so the pair nets
	// to nothing there and Total keluar stays empty. Before, it read 25000 in
	// and 25000 out.
	assertInOut(t, r.Walk, 0, 0)
	for _, e := range es {
		if e.IsReversal && e.Line != ReportLineIn {
			t.Errorf("reversal Line = %q, want %q: it filters and counts as Uang masuk", e.Line, ReportLineIn)
		}
	}

	// Unfiltered, September's in 235000 already held the payment, so the
	// netted pair leaves it exactly where it was.
	all := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09"})
	assertInOut(t, all.Walk, 235_000, 80_000)

	// The reversal filters as Uang masuk: listed under it, gone under Uang keluar.
	in := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09", Direction: "in"})
	out := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09", Direction: "out"})
	if n := countReversals(in.Rows); n != 1 {
		t.Errorf("reversals under direction in = %d, want 1", n)
	}
	if n := countReversals(out.Rows); n != 0 {
		t.Errorf("reversals under direction out = %d, want 0", n)
	}
}

func countReversals(rows []ReportRow) int {
	n := 0
	for _, e := range entries(rows) {
		if e.IsReversal {
			n++
		}
	}
	return n
}

func TestMonthlyReportSettledClaimNamesItsMemberAndCarriesTheClaimsNoteAndReceipt(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	q := store.New(l.db)
	f := newFixture(t, l)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-09-01", nil)

	claimNote := "Test claim note"
	claim, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: f.fundID, MemberID: f.memberID, PurposeID: f.mainID, Amount: 40_000,
		IncurredOn: "2026-09-02", Note: &claimNote, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}
	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: f.fundID, ReimbursementID: &claim.ID, Path: "private-claim-receipt.jpg", UploadedAt: 1,
	}); err != nil {
		t.Fatalf("CreateReceipt() = %v, want no error", err)
	}
	if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: "2026-09-10",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", MemberID: &f.memberID})

	es := entries(r.Rows)
	if len(es) != 1 {
		t.Fatalf("rows for the member = %d, want the payout alone", len(es))
	}
	e := es[0]
	if e.Kind != "reimbursement" || e.MemberName == nil || *e.MemberName != "Jane" {
		t.Errorf("payout = %+v, want kind reimbursement naming Jane", e)
	}
	if e.Note == nil || *e.Note != claimNote {
		t.Errorf("payout note = %v, want the claim's %q", e.Note, claimNote)
	}
	if !e.HasReceipt {
		t.Error("payout HasReceipt = false, want true: the claim's receipt is the payout's proof")
	}
	assertInOut(t, r.Walk, 0, 40_000)
}

func TestMonthlyReportOpeningBalanceIsItsOwnLineNotIncome(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	postOpeningBalance(t, l, f.fundID, f.cashID, f.mainID, 500_000, "2026-09-01")

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})

	es := entries(r.Rows)
	if len(es) != 1 || es[0].Kind != "opening" || es[0].Direction != "in" || es[0].Amount != 500_000 {
		t.Fatalf("entries = %+v, want one incoming opening row of 500000", es)
	}
	// ADR-038: an opening is Saldo awal, not Total masuk, so the setup month
	// starts at zero instead of showing the whole starting balance as income.
	want := ReportWalk{Full: true, StartOn: "2026-08-31", EndOn: "2026-09-30", Openings: 500_000, End: 500_000}
	if r.Walk != want {
		t.Errorf("Walk = %+v, want %+v", r.Walk, want)
	}
	if es[0].Line != ReportLineOpening {
		t.Errorf("opening Line = %q, want %q", es[0].Line, ReportLineOpening)
	}
}

func TestMonthlyReportTotalsRefuseToWrap(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	big := money.Amount(math.MaxInt64/2 + 1)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", big, "2026-09-01", nil)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", big, "2026-09-02", nil)

	// SQLite's own SUM() in FundBalance trips first, before the checked Add
	// over the rows could; either way an error, never a wrapped figure.
	_, err := l.MonthlyReport(context.Background(), ReportParams{FundID: f.fundID, Month: "2026-09", Now: reportNow})
	if err == nil {
		t.Fatal("MonthlyReport() = nil error, want an overflow refused rather than a wrapped total")
	}
}

// --- dues -----------------------------------------------------------------------

func TestMonthlyReportDuesStatusFilter(t *testing.T) {
	l := newTestLedger(t)
	s := newMonthScenario(t, l)

	names := func(r Report) []string {
		var out []string
		for _, d := range r.Dues {
			out = append(out, d.MemberName)
		}
		return out
	}
	tests := []struct {
		filter string
		want   []string
	}{
		{"", []string{"Member One", "Member Two", "Member Three"}},
		{"paid", []string{"Member One"}},
		{"partial", []string{"Member Two"}},
		{"unpaid", []string{"Member Three"}},
	}
	for _, tt := range tests {
		t.Run("filter "+tt.filter, func(t *testing.T) {
			r := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09", DuesStatus: tt.filter})
			got := names(r)
			if len(got) != len(tt.want) {
				t.Fatalf("dues members = %v, want %v", got, tt.want)
			}
			seen := map[string]bool{}
			for _, n := range got {
				seen[n] = true
			}
			for _, w := range tt.want {
				if !seen[w] {
					t.Errorf("dues members = %v, want %v", got, tt.want)
				}
			}
			// The dues filter never narrows the transactions (ADR-035).
			if len(r.Rows) != 5 {
				t.Errorf("Rows = %d under a dues filter, want 5", len(r.Rows))
			}
		})
	}

	r := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09"})
	for _, d := range r.Dues {
		if d.TierName != "Tier A" {
			t.Errorf("%s TierName = %q, want %q", d.MemberName, d.TierName, "Tier A")
		}
		switch d.MemberName {
		case "Member One":
			if d.Owed != 25_000 || d.Paid != 25_000 || d.Status != DuesStatusPaid {
				t.Errorf("Member One = %+v, want owed 25000, paid 25000, paid", d)
			}
		case "Member Two":
			if d.Owed != 25_000 || d.Paid != 10_000 || d.Status != DuesStatusPartial {
				t.Errorf("Member Two = %+v, want owed 25000, paid 10000, partial", d)
			}
		case "Member Three":
			if d.Owed != 25_000 || d.Paid != 0 || d.Status != DuesStatusUnpaid {
				t.Errorf("Member Three = %+v, want owed 25000, paid 0, unpaid", d)
			}
		}
	}
}

func TestMonthlyReportPaidFilterIncludesAMemberWhoPaidInAdvance(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	s := newMonthScenario(t, l)
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: s.f.fundID, AccountID: s.f.cashID, PurposeID: s.f.mainID, MemberID: s.memberOne,
		OccurredOn: "2026-09-07", Periods: []PeriodAmount{{DuesPeriod: "2026-10", Amount: 25_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09", DuesStatus: "paid"})

	if len(r.Dues) != 1 || r.Dues[0].MemberName != "Member One" || r.Dues[0].Status != DuesStatusPaidInAdvance {
		t.Fatalf("Dues = %+v, want Member One, paid in advance, under the paid filter", r.Dues)
	}
	if r.Dues[0].PaidThrough != "2026-10" {
		t.Errorf("PaidThrough = %q, want 2026-10", r.Dues[0].PaidThrough)
	}
}

// --- envelopes ------------------------------------------------------------------

func TestMonthlyReportEnvelopesOpenClosedThisMonthAndNotClosedEarlier(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)

	open := openTestIncidental(t, l, f.fundID, "Open Collection", "2026-08-01")
	closedNow := openTestIncidental(t, l, f.fundID, "Closed This Month", "2026-08-05")
	closedEarlier := openTestIncidental(t, l, f.fundID, "Closed Earlier", "2026-07-01")
	postEntry(t, l, f.fundID, f.cashID, open.PurposeID, "in", 40_000, "2026-08-10", nil)
	postEntry(t, l, f.fundID, f.cashID, closedNow.PurposeID, "in", 15_000, "2026-08-10", nil)
	for _, c := range []struct {
		purposeID int64
		on        string
	}{{closedNow.PurposeID, "2026-09-10"}, {closedEarlier.PurposeID, "2026-08-20"}} {
		if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
			FundID: f.fundID, PurposeID: c.purposeID, AccountID: f.cashID, ClosedOn: c.on,
		}); err != nil {
			t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
		}
	}

	names := func(r Report) []string {
		var out []string
		for _, e := range r.Envelopes {
			out = append(out, e.Name)
		}
		return out
	}
	equal := slices.Equal[[]string]

	sep := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})
	if got, want := names(sep), []string{"Open Collection", "Closed This Month"}; !equal(got, want) {
		t.Errorf("September envelopes = %v, want %v (oldest opened first: open, plus closed this month, not closed before it)", got, want)
	}
	aug := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-08"})
	if got, want := names(aug), []string{"Closed Earlier", "Open Collection", "Closed This Month"}; !equal(got, want) {
		t.Errorf("August envelopes = %v, want %v", got, want)
	}
	// An envelope opened after the month did not exist in it yet.
	jul := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-07"})
	if got, want := names(jul), []string{"Closed Earlier"}; !equal(got, want) {
		t.Errorf("July envelopes = %v, want %v (Open Collection opened in August)", got, want)
	}
	oct := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-10"})
	if got, want := names(oct), []string{"Open Collection"}; !equal(got, want) {
		t.Errorf("October envelopes = %v, want %v", got, want)
	}

	// The header lists Kas Utama, then only the envelope still open: a closed
	// one rolled its leftover into Kas Utama and is gone from it.
	var headerNames []string
	for _, b := range sep.PurposeBalances {
		headerNames = append(headerNames, b.Name)
	}
	for _, n := range headerNames {
		if n == "Closed This Month" || n == "Closed Earlier" {
			t.Errorf("header balances = %v, want no closed envelope", headerNames)
		}
	}
	if len(headerNames) == 0 || headerNames[0] != "Primary Cash" {
		t.Errorf("header balances = %v, want Kas Utama first", headerNames)
	}
	for _, b := range sep.PurposeBalances {
		if b.Name == "Open Collection" && b.Balance != 40_000 {
			t.Errorf("Open Collection balance = %d, want 40000", b.Balance)
		}
	}
}

func TestMonthlyReportEnvelopeCarriesItsParticipation(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	q := store.New(l.db)
	f := newFixture(t, l)
	memberTwo := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Member Two"})
	recipient := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Member Three"})

	minimum := money.Amount(20_000)
	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Test Collection", OpenedOn: "2026-09-01", MinimumPerMember: &minimum,
		RecipientMemberIDs: []int64{recipient},
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID, Direction: "in",
		Amount: 25_000, OccurredOn: "2026-09-05", MemberID: &f.memberID,
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID, Direction: "in",
		Amount: 5_000, OccurredOn: "2026-09-06", MemberID: &memberTwo,
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})

	if len(r.Envelopes) != 1 {
		t.Fatalf("Envelopes = %d, want 1", len(r.Envelopes))
	}
	e := r.Envelopes[0]
	if e.Balance != 30_000 || e.ClosedOn != nil || e.OpenedOn != "2026-09-01" {
		t.Errorf("envelope = %+v, want balance 30000, open, opened 2026-09-01", e)
	}
	if e.Collected != 30_000 {
		t.Errorf("Collected = %d, want 30000 (what the occasion took in)", e.Collected)
	}
	if e.MinimumPerMember == nil || *e.MinimumPerMember != 20_000 || e.TargetAmount != nil {
		t.Errorf("envelope minimum = %v, target = %v, want 20000 and none", e.MinimumPerMember, e.TargetAmount)
	}
	want := []ReportParticipant{
		{MemberName: "Jane", Amount: 25_000, State: ParticipationSudah},
		{MemberName: "Member Two", Amount: 5_000, State: ParticipationKurang},
	}
	if len(e.Expected) != len(want) {
		t.Fatalf("Expected = %+v, want %+v", e.Expected, want)
	}
	for i := range want {
		if e.Expected[i] != want[i] {
			t.Errorf("Expected[%d] = %+v, want %+v", i, e.Expected[i], want[i])
		}
	}
	if len(e.Unexpected) != 0 {
		t.Errorf("Unexpected = %+v, want none", e.Unexpected)
	}
	if got, want := e.Recipients, []string{"Member Three"}; !slices.Equal(got, want) {
		t.Errorf("Recipients = %v, want %v (the member the envelope is for, never expected)", got, want)
	}
}

// --- month range ----------------------------------------------------------------

func TestMonthlyReportMonthRangeIsReckonedInJakartaAcrossTheMonthBoundary(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 1_000, "2026-08-31", nil)

	tests := []struct {
		name       string
		now        time.Time
		wantMonths []string
		wantAsOf   string
	}{
		{
			// 16:59:59 UTC is 23:59:59 in Jakarta: still September.
			"last second of September in Jakarta",
			time.Date(2026, 9, 30, 16, 59, 59, 0, time.UTC),
			[]string{"2026-08", "2026-09"}, "2026-09-30",
		},
		{
			// One second later it is 00:00:00 on October 1 in Jakarta, though
			// the server's own UTC clock still says September.
			"first second of October in Jakarta",
			time.Date(2026, 9, 30, 17, 0, 0, 0, time.UTC),
			[]string{"2026-08", "2026-09", "2026-10"}, "2026-10-01",
		},
		{
			"a clock in another zone reads the same instant",
			time.Date(2026, 9, 30, 12, 0, 0, 0, time.FixedZone("UTC-5", -5*3600)), // 17:00 UTC
			[]string{"2026-08", "2026-09", "2026-10"}, "2026-10-01",
		},
		{
			"across a year boundary",
			time.Date(2027, 1, 3, 0, 0, 0, 0, tz.Jakarta),
			[]string{"2026-08", "2026-09", "2026-10", "2026-11", "2026-12", "2027-01"}, "2027-01-03",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Now: tt.now})

			if len(r.Months) != len(tt.wantMonths) {
				t.Fatalf("Months = %v, want %v", r.Months, tt.wantMonths)
			}
			for i := range tt.wantMonths {
				if r.Months[i] != tt.wantMonths[i] {
					t.Fatalf("Months = %v, want %v", r.Months, tt.wantMonths)
				}
			}
			if last := tt.wantMonths[len(tt.wantMonths)-1]; r.Month != last {
				t.Errorf("Month = %q, want the current month %q by default", r.Month, last)
			}
			if r.AsOf != tt.wantAsOf {
				t.Errorf("AsOf = %q, want %q", r.AsOf, tt.wantAsOf)
			}
		})
	}
}

func TestMonthlyReportMonthRangeWhenTheFirstRowIsThisMonthOrLater(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 1_000, "2026-10-01", nil)

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID})
	if len(r.Months) != 1 || r.Months[0] != "2026-10" {
		t.Errorf("Months = %v, want [2026-10]", r.Months)
	}

	// A first row dated after today still leaves the current month offered.
	l2 := newTestLedger(t)
	f2 := newFixture(t, l2)
	postEntry(t, l2, f2.fundID, f2.cashID, f2.mainID, "in", 1_000, "2027-03-01", nil)
	r = monthlyReport(t, l2, ReportParams{FundID: f2.fundID})
	if len(r.Months) != 1 || r.Months[0] != "2026-10" {
		t.Errorf("Months = %v, want [2026-10]: the range never inverts", r.Months)
	}
}

func TestMonthlyReportAMonthOutsideTheRangeIsEmptyNotAnError(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 1_000, "2026-09-01", nil)

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2020-01"})
	if r.Month != "2020-01" || len(r.Rows) != 0 {
		t.Errorf("Month = %q, Rows = %d, want an empty 2020-01", r.Month, len(r.Rows))
	}
}

// --- fund scoping and validation ------------------------------------------------

func TestMonthlyReportAnotherFundsRowsAreInvisible(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	s := newMonthScenario(t, l)
	other := newSecondFund(t, l)

	// The other fund has a row on every report surface: money, a transfer
	// pair's purpose, dues, an envelope, a reconciliation.
	postEntry(t, l, other.fundID, other.cashID, other.mainID, "in", 900_000, "2026-09-04", strPtr("Other note"))
	postEntry(t, l, other.fundID, other.cashID, other.mainID, "out", 111_000, "2026-09-05", nil)
	openTestIncidental(t, l, other.fundID, "Other Collection", "2026-09-01")
	q := store.New(l.db)
	if _, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: other.fundID, PerformedAt: 1_790_000_000, CreatedAt: 1_790_000_000,
	}); err != nil {
		t.Fatalf("CreateReconciliation() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09"})

	if r.FundName != "Test Fund" {
		t.Errorf("FundName = %q, want %q", r.FundName, "Test Fund")
	}
	if r.Balance != 165_000 {
		t.Errorf("Balance = %d, want 165000: the other fund's money is not in it", r.Balance)
	}
	assertInOut(t, r.Walk, 235_000, 80_000)
	if len(r.Rows) != 5 {
		t.Errorf("Rows = %d, want 5", len(r.Rows))
	}
	if r.Reconciliation != nil {
		t.Errorf("Reconciliation = %+v, want nil: this fund was never counted", r.Reconciliation)
	}
	if len(r.Envelopes) != 0 {
		t.Errorf("Envelopes = %+v, want none", r.Envelopes)
	}
	for _, d := range r.Dues {
		if d.MemberName == "Other Member" {
			t.Errorf("Dues carries the other fund's member")
		}
	}
	for _, b := range r.PurposeBalances {
		if b.Name == "Other Main" || b.Name == "Other Collection" {
			t.Errorf("PurposeBalances carries the other fund's %q", b.Name)
		}
	}

	// Naming the other fund's purpose or member as a filter finds nothing.
	r = monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09", PurposeID: &other.mainID})
	if len(r.Rows) != 0 {
		t.Errorf("Rows under the other fund's purpose = %d, want 0", len(r.Rows))
	}
	r = monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09", MemberID: &other.memberID})
	if len(r.Rows) != 0 {
		t.Errorf("Rows under the other fund's member = %d, want 0", len(r.Rows))
	}

	// And the other fund's own report is its own.
	ro := monthlyReport(t, l, ReportParams{FundID: other.fundID, Month: "2026-09"})
	assertInOut(t, ro.Walk, 900_000, 111_000)
	if ro.Balance != 789_000 || ro.FundName != "Other Fund" {
		t.Errorf("other fund: Balance = %d, FundName = %q, want 789000 and Other Fund", ro.Balance, ro.FundName)
	}
	if ro.Reconciliation == nil {
		t.Error("other fund Reconciliation = nil, want its own")
	}
}

func TestMonthlyReportRefusesMalformedArguments(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)

	tests := []struct {
		name string
		p    ReportParams
	}{
		{"month not a month", ReportParams{Month: "2026-13"}},
		{"month with a day", ReportParams{Month: "2026-09-01"}},
		{"month unpadded", ReportParams{Month: "2026-9"}},
		{"month words", ReportParams{Month: "september"}},
		{"direction", ReportParams{Direction: "sideways"}},
		{"dues status", ReportParams{DuesStatus: "late"}},
		{"dues status paid in advance is not a filter value", ReportParams{DuesStatus: "paid_in_advance"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.p
			p.FundID, p.Now = f.fundID, reportNow
			if _, err := l.MonthlyReport(context.Background(), p); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("MonthlyReport(%+v) error = %v, want ErrInvalidArgument", p, err)
			}
		})
	}
}

func TestMonthlyReportUnknownFundIsAnError(t *testing.T) {
	l := newTestLedger(t)
	if _, err := l.MonthlyReport(context.Background(), ReportParams{FundID: 424_242, Now: reportNow}); err == nil {
		t.Error("MonthlyReport(unknown fund) = nil error, want one")
	}
}

func TestMonthlyReportEnvelopeCarriesItsTargetAndUnexpectedGivers(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	q := store.New(l.db)
	f := newFixture(t, l)

	target := money.Amount(500_000)
	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Test Collection", OpenedOn: "2026-09-01", TargetAmount: &target,
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}
	// Joined after the envelope opened, so never expected - but a gift still shows.
	joined := "2026-09-10"
	late := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Late Joiner", joinedOn: &joined})
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID, Direction: "in",
		Amount: 12_000, OccurredOn: "2026-09-12", MemberID: &late,
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", Now: reportNow})
	if len(r.Envelopes) != 1 {
		t.Fatalf("Envelopes = %d, want 1", len(r.Envelopes))
	}
	e := r.Envelopes[0]
	if e.TargetAmount == nil || *e.TargetAmount != 500_000 {
		t.Errorf("TargetAmount = %v, want 500000", e.TargetAmount)
	}
	want := []ReportContributor{{MemberName: "Late Joiner", Amount: 12_000}}
	if !slices.Equal(e.Unexpected, want) {
		t.Errorf("Unexpected = %+v, want %+v", e.Unexpected, want)
	}
	for _, p := range e.Expected {
		if p.MemberName == "Late Joiner" {
			t.Errorf("Expected includes %q, who joined after the envelope opened", p.MemberName)
		}
	}
}

func TestMonthlyReportEnvelopesOpenedTheSameDayAreOrderedByName(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	openTestIncidental(t, l, f.fundID, "Zeta Collection", "2026-09-01")
	openTestIncidental(t, l, f.fundID, "Alpha Collection", "2026-09-01")

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", Now: reportNow})

	var envelopes, header []string
	for _, e := range r.Envelopes {
		envelopes = append(envelopes, e.Name)
	}
	for _, b := range r.PurposeBalances {
		// The fixture carries an incidental purpose of its own; only the two
		// opened here are compared.
		if b.Name == "Alpha Collection" || b.Name == "Zeta Collection" {
			header = append(header, b.Name)
		}
	}
	want := []string{"Alpha Collection", "Zeta Collection"}
	if !slices.Equal(envelopes, want) {
		t.Errorf("Envelopes = %v, want %v (same day, then by name - never by id)", envelopes, want)
	}
	if !slices.Equal(header, want) {
		t.Errorf("header envelopes = %v, want %v", header, want)
	}
}

func TestMonthlyReportWithNoClockReadsTodayInJakarta(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)

	r, err := l.MonthlyReport(context.Background(), ReportParams{FundID: f.fundID})
	if err != nil {
		t.Fatalf("MonthlyReport() = %v, want no error", err)
	}
	today := time.Now().In(tz.Jakarta)
	// Straddling midnight in Jakarta between the two reads is the only way this
	// differs; accept either side of it.
	if r.AsOf != today.Format("2006-01-02") && r.AsOf != today.Add(-time.Minute).Format("2006-01-02") {
		t.Errorf("AsOf = %q, want today in Jakarta (%s)", r.AsOf, today.Format("2006-01-02"))
	}
	if r.Month != r.AsOf[:7] {
		t.Errorf("Month = %q, want the current Jakarta month %q", r.Month, r.AsOf[:7])
	}
}

// A treasurer's purpose move (ADR-036) folds into one move row like a roll,
// but carries its reason so the public page can say "Pindah pos"
// instead of "Tutup amplop". Outside both totals, no balance moves.
func TestMonthlyReportAllocationIsOneMoveRowCarryingItsReason(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)

	envelope := openTestIncidental(t, l, f.fundID, "Bereavement", "2026-09-01")
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 300_000, "2026-09-02", nil)
	if _, err := l.PostPurposeMove(ctx, PostPurposeMoveParams{
		FundID: f.fundID, FromPurposeID: f.mainID, ToPurposeID: envelope.PurposeID, AccountID: f.cashID,
		Amount: 120_000, OccurredOn: "2026-09-12",
	}); err != nil {
		t.Fatalf("PostPurposeMove() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})

	mv := moves(r.Rows)
	if len(mv) != 1 {
		t.Fatalf("moves = %d, want 1", len(mv))
	}
	if m := mv[0]; m.Amount != 120_000 || m.FromPurposeName != "Primary Cash" || m.ToPurposeName != "Bereavement" || m.IsCorrection || !m.IsAllocation {
		t.Errorf("move = %+v, want an allocation of 120000 from Primary Cash to Bereavement", m)
	}
	assertInOut(t, r.Walk, 300_000, 0)
	if r.Balance != 300_000 {
		t.Errorf("Balance = %d, want 300000: a move changes no balance", r.Balance)
	}
}

// A row corrected more than once reads as one move from where it started to
// where it ended, and one corrected back to where it started reads as none
// (#280). The legs all stay posted: only the report's rows fold.
func TestMonthlyReportFoldsARowsCorrectionsIntoTheirNetMove(t *testing.T) {
	ctx := context.Background()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	otherID := createPurpose(t, q, f.fundID, "pass_through", "Other pass-through")

	correct := func(txID, purposeID int64) {
		t.Helper()
		if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{FundID: f.fundID, TransactionID: txID, PurposeID: purposeID}); err != nil {
			t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
		}
	}

	// Corrected three times: main -> pass -> other -> pass.
	thrice := postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 50_000, "2026-09-10", nil)
	correct(thrice.ID, f.passID)
	correct(thrice.ID, otherID)
	correct(thrice.ID, f.passID)

	// Corrected and corrected back: main -> pass -> main.
	back := postEntry(t, l, f.fundID, f.cashID, f.mainID, "out", 20_000, "2026-09-11", nil)
	correct(back.ID, f.passID)
	correct(back.ID, f.mainID)

	// Money moved on purpose is not a correction and never folds.
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 30_000, "2026-09-01", nil)
	envelope := openTestIncidental(t, l, f.fundID, "Envelope", "2026-09-01")
	if _, err := l.PostPurposeMove(ctx, PostPurposeMoveParams{
		FundID: f.fundID, FromPurposeID: f.mainID, ToPurposeID: envelope.PurposeID, AccountID: f.cashID, Amount: 5_000, OccurredOn: "2026-09-12",
	}); err != nil {
		t.Fatalf("PostPurposeMove() = %v, want no error", err)
	}

	r := monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09"})
	mv := moves(r.Rows)
	if len(mv) != 2 {
		t.Fatalf("moves = %+v, want 2: the net correction and the allocation", mv)
	}
	var correction, allocation *ReportMove
	for i := range mv {
		if mv[i].IsCorrection {
			correction = &mv[i]
		}
		if mv[i].IsAllocation {
			allocation = &mv[i]
		}
	}
	if correction == nil || correction.Amount != 50_000 || correction.FromPurposeName != "Primary Cash" || correction.ToPurposeName != "Pass-through" {
		t.Errorf("net correction = %+v, want 50000 from Primary Cash to Pass-through", correction)
	}
	if allocation == nil || allocation.Amount != 5_000 {
		t.Errorf("allocation = %+v, want the 5000 move untouched", allocation)
	}
	if got := len(entries(r.Rows)); got != 3 {
		t.Errorf("entries = %d, want all three posted rows", got)
	}

	// The purpose the money only passed through has nothing to show.
	r = monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-09", PurposeID: &otherID})
	if mv := moves(r.Rows); len(mv) != 0 {
		t.Errorf("moves under the intermediate purpose = %+v, want none", mv)
	}

	// Balances still sum every posted leg.
	for _, c := range []struct {
		id   int64
		want money.Amount
	}{{f.mainID, 30_000 - 20_000 - 5_000}, {f.passID, 50_000}, {otherID, 0}, {envelope.PurposeID, 5_000}} {
		got, err := l.PurposeBalance(ctx, f.fundID, c.id)
		if err != nil {
			t.Fatalf("PurposeBalance() = %v", err)
		}
		if got != c.want {
			t.Errorf("purpose %d balance = %d, want %d", c.id, got, c.want)
		}
	}
}
