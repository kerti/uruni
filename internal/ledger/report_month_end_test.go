package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// monthEndScenario spans August to October 2026 with an event on each side of
// the August/September boundary (#408, ADR-037), so each month-end figure can
// be shown to include the one and leave out the other.
type monthEndScenario struct {
	f        fixture
	envelope store.Incidental
}

func newMonthEndScenario(t *testing.T, l *Ledger) monthEndScenario {
	t.Helper()
	ctx := context.Background()
	q := store.New(l.db)
	f := newFixture(t, l)
	s := monthEndScenario{f: f}

	// Either side of the boundary: 31 August counts for August, 1 September
	// does not.
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-08-31", nil)
	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 50_000, "2026-09-01", nil)

	// An envelope opened and given to in August, then closed in September -
	// open at August's end, gone by September's.
	s.envelope = openTestIncidental(t, l, f.fundID, "Halal bihalal", "2026-08-10")
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: s.envelope.PurposeID,
		Direction: "in", Amount: 20_000, OccurredOn: "2026-08-15", MemberID: &f.memberID,
	}); err != nil {
		t.Fatalf("PostTransaction(contribution) = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: s.envelope.PurposeID,
		Direction: "in", Amount: 5_000, OccurredOn: "2026-09-02", MemberID: &f.memberID,
	}); err != nil {
		t.Fatalf("PostTransaction(late contribution) = %v, want no error", err)
	}
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: s.envelope.PurposeID, AccountID: f.cashID, ClosedOn: "2026-09-05",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	// Three claims: one settled in September, one waived in September, one
	// incurred in September.
	settled := createReimbursement(t, q, f, 7_000, "2026-08-20", nil)
	waived := createReimbursement(t, q, f, 3_000, "2026-08-21", nil)
	createReimbursement(t, q, f, 11_000, "2026-09-10", nil)
	if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: settled.ID, AccountID: f.cashID, OccurredOn: "2026-09-02",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}
	waivedOn := "2026-09-03"
	if _, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: waived.ID, WaivedOn: &waivedOn, SetWaivedOn: true,
	}); err != nil {
		t.Fatalf("UpdateReimbursement() waiving = %v, want no error", err)
	}

	// A count in August left a gap open; a count in September matched it.
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
	count(jakartaAt(2026, time.August, 25, 20, 0, 0), 5_000, "left_open")
	count(jakartaAt(2026, time.September, 10, 20, 0, 0), 0, "matched")
	return s
}

func purposeBalanceNamed(r Report, name string) (money.Amount, bool) {
	for _, p := range r.PurposeBalances {
		if p.Name == name {
			return p.Balance, true
		}
	}
	return 0, false
}

func envelopeNamed(t *testing.T, r Report, name string) ReportEnvelope {
	t.Helper()
	for _, e := range r.Envelopes {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no envelope %q among %+v", name, r.Envelopes)
	return ReportEnvelope{}
}

// A past month is as of its last day: every figure, the summary and
// the envelope card alike, stops there.
func TestMonthlyReportPastMonthIsAsOfItsLastDay(t *testing.T) {
	l := newTestLedger(t)
	s := newMonthEndScenario(t, l)

	aug := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-08", Now: reportNow})
	if aug.Running || aug.AsOf != "2026-08-31" {
		t.Errorf("August Running, AsOf = %v, %q; want false, 2026-08-31", aug.Running, aug.AsOf)
	}
	if aug.Balance != 120_000 {
		t.Errorf("August balance = %d, want 120000 - 31 August in, 1 September out", aug.Balance)
	}
	if aug.OwedToMembers != 10_000 {
		t.Errorf("August owed = %d, want 10000 - settled and waived only in September", aug.OwedToMembers)
	}
	if rec := aug.Reconciliation; rec == nil || rec.Date != "2026-08-25" || rec.Matched || rec.Difference != 5_000 {
		t.Errorf("August reconciliation = %+v, want the 25 August count with 5000 still open", rec)
	}
	if bal, ok := purposeBalanceNamed(aug, "Halal bihalal"); !ok || bal != 20_000 {
		t.Errorf("August Saldo per pos for the envelope = %d, %v; want 20000, shown", bal, ok)
	}
	env := envelopeNamed(t, aug, "Halal bihalal")
	if env.Balance != 20_000 || env.Collected != 20_000 || env.ClosedOn != nil {
		t.Errorf("August envelope card = balance %d, collected %d, closed %v; want 20000, 20000, still open", env.Balance, env.Collected, env.ClosedOn)
	}
	if len(env.Unexpected)+len(env.Expected) == 0 {
		t.Fatalf("August envelope card has no participants")
	}
	var given money.Amount
	for _, p := range env.Expected {
		given += p.Amount
	}
	for _, u := range env.Unexpected {
		given += u.Amount
	}
	if given != 20_000 {
		t.Errorf("August envelope card's givers sum to %d, want 20000 - the September gift had not happened", given)
	}

	sep := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-09", Now: reportNow})
	if sep.Running || sep.AsOf != "2026-09-30" {
		t.Errorf("September Running, AsOf = %v, %q; want false, 2026-09-30", sep.Running, sep.AsOf)
	}
	// 120000 + 50000 + 5000 (late gift) - 7000 (settlement); the roll nets to zero.
	if sep.Balance != 168_000 {
		t.Errorf("September balance = %d, want 168000", sep.Balance)
	}
	if sep.OwedToMembers != 11_000 {
		t.Errorf("September owed = %d, want 11000", sep.OwedToMembers)
	}
	if rec := sep.Reconciliation; rec == nil || rec.Date != "2026-09-10" || !rec.Matched {
		t.Errorf("September reconciliation = %+v, want the matched 10 September count", rec)
	}
	if _, ok := purposeBalanceNamed(sep, "Halal bihalal"); ok {
		t.Error("September Saldo per pos still shows the envelope closed on 5 September")
	}
	if env := envelopeNamed(t, sep, "Halal bihalal"); env.ClosedOn == nil || *env.ClosedOn != "2026-09-05" {
		t.Errorf("September envelope card closed = %v, want 2026-09-05", env.ClosedOn)
	}
}

// The running month reads every row: the same figures Beranda shows.
func TestMonthlyReportRunningMonthIsToday(t *testing.T) {
	l := newTestLedger(t)
	s := newMonthEndScenario(t, l)
	ctx := context.Background()

	oct := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Now: reportNow})
	if !oct.Running || oct.Month != "2026-10" || oct.AsOf != "2026-10-02" {
		t.Errorf("Running, Month, AsOf = %v, %q, %q; want true, 2026-10, 2026-10-02", oct.Running, oct.Month, oct.AsOf)
	}
	fund, err := l.FundBalance(ctx, s.f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v", err)
	}
	owed, err := l.OwedToMembers(ctx, s.f.fundID)
	if err != nil {
		t.Fatalf("OwedToMembers() = %v", err)
	}
	if oct.Balance != fund || oct.OwedToMembers != owed {
		t.Errorf("running month balance, owed = %d, %d; want Beranda's %d, %d", oct.Balance, oct.OwedToMembers, fund, owed)
	}
	for _, p := range oct.PurposeBalances {
		want, err := l.PurposeBalance(ctx, s.f.fundID, p.PurposeID)
		if err != nil {
			t.Fatalf("PurposeBalance() = %v", err)
		}
		if p.Balance != want {
			t.Errorf("running month %s = %d, want Beranda's %d", p.Name, p.Balance, want)
		}
	}
	if rec := oct.Reconciliation; rec == nil || rec.Date != "2026-09-10" || !rec.Matched {
		t.Errorf("running month reconciliation = %+v, want the latest count, matched", rec)
	}
}

// Which month is running is Jakarta's call: 31 October 18:00 UTC is already
// 1 November there, so October is a finished month, and a count taken half
// an hour into November Jakarta time is not October's.
func TestMonthlyReportRunningMonthTurnsOverInJakarta(t *testing.T) {
	l := newTestLedger(t)
	s := newMonthEndScenario(t, l)
	q := store.New(l.db)

	at := time.Date(2026, time.October, 31, 17, 30, 0, 0, time.UTC) // 1 November 00:30 in Jakarta
	if _, err := q.CreateReconciliation(context.Background(), store.CreateReconciliationParams{
		FundID: s.f.fundID, PerformedAt: at.Unix(), CreatedAt: at.Unix(),
	}); err != nil {
		t.Fatalf("CreateReconciliation() = %v, want no error", err)
	}

	now := time.Date(2026, time.October, 31, 18, 0, 0, 0, time.UTC)
	oct := monthlyReport(t, l, ReportParams{FundID: s.f.fundID, Month: "2026-10", Now: now})
	if oct.Running || oct.AsOf != "2026-10-31" {
		t.Errorf("October Running, AsOf = %v, %q; want false, 2026-10-31", oct.Running, oct.AsOf)
	}
	if rec := oct.Reconciliation; rec == nil || rec.Date != "2026-09-10" {
		t.Errorf("October reconciliation = %+v, want the 10 September count, not November's", rec)
	}
}

// Every bound at its edge (#408 review): an event on the month's last day
// belongs to the month, one on the next day does not, and Saldo per pos still
// sums to Saldo kas when a purpose was created after the month it holds money
// for.
func TestMonthlyReportLastDayBoundaries(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()
	q := store.New(l.db)
	f := newFixture(t, l)
	aug := func() Report {
		return monthlyReport(t, l, ReportParams{FundID: f.fundID, Month: "2026-08", Now: reportNow})
	}
	contribute := func(purposeID int64, amount money.Amount, on string) store.Transaction {
		t.Helper()
		tx, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: purposeID,
			Direction: "in", Amount: amount, OccurredOn: on, MemberID: &f.memberID,
		})
		if err != nil {
			t.Fatalf("PostTransaction(contribution) = %v, want no error", err)
		}
		return tx
	}
	reverse := func(tx store.Transaction, on string) {
		t.Helper()
		if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{FundID: f.fundID, TransactionID: tx.ID, OccurredOn: on}); err != nil {
			t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
		}
	}
	waive := func(id int64, on string) {
		t.Helper()
		if _, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{FundID: f.fundID, ReimbursementID: id, WaivedOn: &on, SetWaivedOn: true}); err != nil {
			t.Fatalf("UpdateReimbursement() waiving = %v, want no error", err)
		}
	}

	postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 100_000, "2026-08-01", nil)

	// Claims: paid out on the last day, waived on the last day, waived the
	// day after. Only the last is still owed at the end of August.
	settled := createReimbursement(t, q, f, 5_000, "2026-08-10", nil)
	if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: settled.ID, AccountID: f.cashID, OccurredOn: "2026-08-31",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}
	waive(createReimbursement(t, q, f, 3_000, "2026-08-10", nil).ID, "2026-08-31")
	waive(createReimbursement(t, q, f, 2_000, "2026-08-10", nil).ID, "2026-09-01")

	// Envelope A closes on the last day: gone from Saldo per pos, its card
	// already closed.
	closedLastDay := openTestIncidental(t, l, f.fundID, "Closed last day", "2026-08-01")
	contribute(closedLastDay.PurposeID, 10_000, "2026-08-05")
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: closedLastDay.PurposeID, AccountID: f.cashID, ClosedOn: "2026-08-31",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	// Envelope B: a gift reversed the day after still counts for August, a
	// gift reversed on the last day does not, and money moved in the day
	// after is not August's.
	reversals := openTestIncidental(t, l, f.fundID, "Reversals", "2026-08-01")
	reverse(contribute(reversals.PurposeID, 10_000, "2026-08-05"), "2026-09-01")
	reverse(contribute(reversals.PurposeID, 4_000, "2026-08-06"), "2026-08-31")
	if _, err := l.PostPurposeMove(ctx, PostPurposeMoveParams{
		FundID: f.fundID, FromPurposeID: f.mainID, ToPurposeID: reversals.PurposeID,
		AccountID: f.cashID, Amount: 6_000, OccurredOn: "2026-09-01",
	}); err != nil {
		t.Fatalf("PostPurposeMove() = %v, want no error", err)
	}

	// Envelope C opens on the last day: it belongs to August.
	openTestIncidental(t, l, f.fundID, "Opened last day", "2026-08-31")

	// A Titipan created in October for money that arrived in August.
	late, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: f.fundID, Kind: "pass_through", Name: "Titipan susulan", CreatedAt: jakartaAt(2026, time.October, 1, 9, 0, 0).Unix(),
	})
	if err != nil {
		t.Fatalf("CreatePurpose() = %v, want no error", err)
	}
	postEntry(t, l, f.fundID, f.cashID, late.ID, "in", 7_000, "2026-08-20", nil)

	// Counts: one inside August left 1000 open; one at the very first
	// instant of September left 9000 open on the same location.
	count := func(at time.Time, difference int64) {
		t.Helper()
		rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{FundID: f.fundID, PerformedAt: at.Unix(), CreatedAt: at.Unix()})
		if err != nil {
			t.Fatalf("CreateReconciliation() = %v, want no error", err)
		}
		if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
			FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.cashID,
			ActualAmount: difference, DifferenceAmount: difference, Resolution: "left_open",
		}); err != nil {
			t.Fatalf("CreateReconciliationLine() = %v, want no error", err)
		}
	}
	count(jakartaAt(2026, time.August, 20, 12, 0, 0), 1_000)
	count(jakartaAt(2026, time.September, 1, 0, 0, 0), 9_000)

	r := aug()

	if r.OwedToMembers != 2_000 {
		t.Errorf("owed = %d, want 2000 - paid out or waived on the last day is no longer owed", r.OwedToMembers)
	}
	if rec := r.Reconciliation; rec == nil || rec.Date != "2026-08-20" || rec.Difference != 1_000 {
		t.Errorf("reconciliation = %+v, want the 20 August count with 1000 open - not the one at 00:00 on 1 September", rec)
	}

	if _, ok := purposeBalanceNamed(r, "Closed last day"); ok {
		t.Error("Saldo per pos shows the envelope closed on the last day")
	}
	if env := envelopeNamed(t, r, "Closed last day"); env.ClosedOn == nil || env.Balance != 0 {
		t.Errorf("closed-last-day card = closed %v, balance %d; want closed, 0", env.ClosedOn, env.Balance)
	}
	if bal, ok := purposeBalanceNamed(r, "Opened last day"); !ok || bal != 0 {
		t.Errorf("opened-last-day in Saldo per pos = %d, %v; want 0, shown", bal, ok)
	}
	if bal, ok := purposeBalanceNamed(r, "Titipan susulan"); !ok || bal != 7_000 {
		t.Errorf("late Titipan in Saldo per pos = %d, %v; want 7000, shown", bal, ok)
	}

	env := envelopeNamed(t, r, "Reversals")
	if env.Balance != 10_000 || env.Collected != 10_000 {
		t.Errorf("reversals card = balance %d, collected %d; want 10000, 10000", env.Balance, env.Collected)
	}
	var given money.Amount
	for _, p := range env.Expected {
		given += p.Amount
	}
	for _, u := range env.Unexpected {
		given += u.Amount
	}
	if given != 10_000 {
		t.Errorf("reversals card givers sum to %d, want 10000", given)
	}

	var sum money.Amount
	for _, p := range r.PurposeBalances {
		sum += p.Balance
	}
	if sum != r.Balance {
		t.Errorf("Saldo per pos sums to %d, Saldo kas is %d; they must agree", sum, r.Balance)
	}
}
