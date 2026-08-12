package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// The point of the whole M2 epic (#21, #25): every situation the PRD describes
// has to be recordable, and every number the treasurer sees has to fall out of
// summing raw rows. No stored total appears anywhere below except inside a
// reconciliation snapshot, which is a historical claim rather than a balance.
//
// One scenario is deliberately absent because it already has a home: a
// correction posted outside any reconciliation lives in ledger_test.go's
// TestAdjustmentStandsAloneWithNoReconciliation.

// scenarioFund is a fund with both locations money can sit in and the routine
// purpose every ordinary entry carries.
type scenarioFund struct {
	fundID, cashID, bankID, mainID int64
}

func newScenarioFund(t *testing.T, sqlDB *sql.DB, name, slug string) scenarioFund {
	t.Helper()
	f := scenarioFund{}
	f.fundID = createFund(t, sqlDB, name, slug)
	f.cashID = createAccount(t, sqlDB, f.fundID, "cash", "Kas tunai")
	f.bankID = createAccount(t, sqlDB, f.fundID, "bank", "Rekening BRI")
	f.mainID = createPurpose(t, sqlDB, f.fundID, "main", "Kas Utama")
	return f
}

// entry posts one row and returns its id. Every scenario builds its ledger out
// of these, so the assertions below are always reading rows a treasurer could
// have recorded through the app.
func (f scenarioFund) entry(t *testing.T, sqlDB *sql.DB, p store.CreateTransactionParams) int64 {
	t.Helper()
	p.FundID = f.fundID
	if p.CreatedAt == 0 {
		p.CreatedAt = 1
	}
	tx, err := store.New(sqlDB).CreateTransaction(context.Background(), p)
	if err != nil {
		t.Fatalf("CreateTransaction(%s %s %d) = %v, want no error", p.Kind, p.Direction, p.Amount, err)
	}
	return tx.ID
}

func (f scenarioFund) fundBalance(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()
	got, err := store.New(sqlDB).FundBalance(context.Background(), f.fundID)
	if err != nil {
		t.Fatalf("FundBalance = %v, want no error", err)
	}
	return got
}

func (f scenarioFund) accountBalance(t *testing.T, sqlDB *sql.DB, accountID int64) int64 {
	t.Helper()
	got, err := store.New(sqlDB).AccountBalance(context.Background(), store.AccountBalanceParams{
		FundID: f.fundID, AccountID: accountID,
	})
	if err != nil {
		t.Fatalf("AccountBalance(%d) = %v, want no error", accountID, err)
	}
	return got
}

func (f scenarioFund) purposeBalance(t *testing.T, sqlDB *sql.DB, purposeID int64) int64 {
	t.Helper()
	got, err := store.New(sqlDB).PurposeBalance(context.Background(), store.PurposeBalanceParams{
		FundID: f.fundID, PurposeID: purposeID,
	})
	if err != nil {
		t.Fatalf("PurposeBalance(%d) = %v, want no error", purposeID, err)
	}
	return got
}

func TestOpeningBalancesAreLedgerRowsLikeAnyOther(t *testing.T) {
	sqlDB := migratedTestDB(t)
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)

	// Day one: the treasurer counts what is already there. Nothing about an
	// opening figure is special enough to earn a column of its own.
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 1_250_000,
		OccurredOn: "2026-01-01", Kind: "opening",
	})
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.bankID, PurposeID: f.mainID, Direction: "in", Amount: 4_000_000,
		OccurredOn: "2026-01-01", Kind: "opening",
	})

	if got, want := f.accountBalance(t, sqlDB, f.cashID), int64(1_250_000); got != want {
		t.Errorf("cash balance = %d, want %d", got, want)
	}
	if got, want := f.accountBalance(t, sqlDB, f.bankID), int64(4_000_000); got != want {
		t.Errorf("bank balance = %d, want %d", got, want)
	}
	if got, want := f.fundBalance(t, sqlDB), int64(5_250_000); got != want {
		t.Errorf("fund balance = %d, want %d", got, want)
	}
}

func TestSeveralMonthsOfDuesPaidInOneVisit(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)
	memberID := createMember(t, sqlDB, f.fundID, "Bu Sri")

	// Three months settled at the door. One row per period, never one row for
	// 75000 - otherwise "which months has she paid?" stops being answerable.
	for _, period := range []string{"2026-06", "2026-07", "2026-08"} {
		p := period
		f.entry(t, sqlDB, store.CreateTransactionParams{
			AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 25_000,
			OccurredOn: "2026-08-12", Kind: "dues", MemberID: &memberID, DuesPeriod: &p,
		})
	}

	paid, err := store.New(sqlDB).ListDuesPaymentsByMember(ctx, &memberID)
	if err != nil {
		t.Fatalf("ListDuesPaymentsByMember = %v, want no error", err)
	}
	if len(paid) != 3 {
		t.Fatalf("dues rows = %d, want 3 - one per period", len(paid))
	}
	for i, want := range []string{"2026-06", "2026-07", "2026-08"} {
		if paid[i].DuesPeriod == nil || *paid[i].DuesPeriod != want {
			t.Errorf("dues row %d period = %v, want %q", i, paid[i].DuesPeriod, want)
		}
	}
	if got, want := f.fundBalance(t, sqlDB), int64(75_000); got != want {
		t.Errorf("fund balance = %d, want %d", got, want)
	}
}

func TestPartialAndAdvanceDuesAreOrdinaryRows(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)
	tierID := createDuesTier(t, sqlDB, f.fundID, "warga tetap")
	if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
		TierID: tierID, Amount: 25_000, EffectiveFrom: "2026-01", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateDuesRate = %v, want no error", err)
	}
	memberID, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: f.fundID, Name: "Pak Budi", TierID: &tierID, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember = %v, want no error", err)
	}

	// Ten thousand now, the rest whenever. The schema takes no view on what a
	// period "should" total - the rate is an expectation, not a constraint, and
	// the arrears arithmetic is M3's job over exactly these rows.
	august := "2026-08"
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 10_000,
		OccurredOn: "2026-08-05", Kind: "dues", MemberID: &memberID.ID, DuesPeriod: &august,
	})
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 15_000,
		OccurredOn: "2026-08-20", Kind: "dues", MemberID: &memberID.ID, DuesPeriod: &august,
	})

	// And a month paid before it starts, recorded on the day the money arrived.
	december := "2026-12"
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 25_000,
		OccurredOn: "2026-08-20", Kind: "dues", MemberID: &memberID.ID, DuesPeriod: &december,
	})

	rate, err := q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{
		TierID: tierID, EffectiveFrom: august,
	})
	if err != nil {
		t.Fatalf("GetEffectiveDuesRate = %v, want no error", err)
	}

	paid, err := q.ListDuesPaymentsByMember(ctx, &memberID.ID)
	if err != nil {
		t.Fatalf("ListDuesPaymentsByMember = %v, want no error", err)
	}
	var forAugust int64
	for _, p := range paid {
		if p.DuesPeriod != nil && *p.DuesPeriod == august {
			forAugust += p.Amount
		}
	}
	if forAugust != rate.Amount {
		t.Errorf("August dues paid = %d, want the two partials to reach the rate of %d", forAugust, rate.Amount)
	}
	if got, want := f.fundBalance(t, sqlDB), int64(50_000); got != want {
		t.Errorf("fund balance = %d, want %d - the advance is money in hand today", got, want)
	}
}

func TestIncidentalOverCollectsThenRollsItsLeftoverIntoKasUtama(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)

	kurbanID := createPurpose(t, sqlDB, f.fundID, "incidental", "Kurban 2026")
	target := int64(3_000_000)
	if _, err := q.CreateIncidental(ctx, store.CreateIncidentalParams{
		PurposeID: kurbanID, Occasion: "Kurban 2026", TargetAmount: &target,
		OpenedOn: "2026-05-01", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateIncidental = %v, want no error", err)
	}

	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: kurbanID, Direction: "in", Amount: 3_200_000,
		OccurredOn: "2026-05-20", Kind: "normal",
	})
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: kurbanID, Direction: "out", Amount: 3_000_000,
		OccurredOn: "2026-06-05", Kind: "normal",
	})

	if got, want := f.purposeBalance(t, sqlDB, kurbanID), int64(200_000); got != want {
		t.Fatalf("leftover in the envelope = %d, want %d", got, want)
	}
	before := f.fundBalance(t, sqlDB)

	closed := "2026-06-10"
	if _, err := q.CloseIncidental(ctx, store.CloseIncidentalParams{ClosedOn: &closed, PurposeID: kurbanID}); err != nil {
		t.Fatalf("CloseIncidental = %v, want no error", err)
	}
	if got := f.fundBalance(t, sqlDB); got != before {
		t.Errorf("fund balance after closing the envelope = %d, want %d - closing moves no money", got, before)
	}

	// The roll-in is a reclass: the same rupiah, a different label. Two rows in
	// one transfer, because a single row would change the fund's total and
	// nothing actually moved.
	transferID := createTransfer(t, sqlDB, f.fundID, "reclass_purpose")
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: kurbanID, Direction: "out", Amount: 200_000,
		OccurredOn: "2026-06-10", Kind: "transfer", TransferID: &transferID,
	})
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 200_000,
		OccurredOn: "2026-06-10", Kind: "transfer", TransferID: &transferID,
	})

	if got := f.fundBalance(t, sqlDB); got != before {
		t.Errorf("fund balance after the roll-in = %d, want %d unchanged", got, before)
	}
	if got, want := f.purposeBalance(t, sqlDB, kurbanID), int64(0); got != want {
		t.Errorf("envelope after the roll-in = %d, want %d", got, want)
	}
	if got, want := f.purposeBalance(t, sqlDB, f.mainID), int64(200_000); got != want {
		t.Errorf("Kas Utama after the roll-in = %d, want %d", got, want)
	}
}

func TestCashDepositedAtTheBankMovesWithoutChangingTheTotal(t *testing.T) {
	sqlDB := migratedTestDB(t)
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)

	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 1_000_000,
		OccurredOn: "2026-01-01", Kind: "opening",
	})
	before := f.fundBalance(t, sqlDB)

	transferID := createTransfer(t, sqlDB, f.fundID, "between_accounts")
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 600_000,
		OccurredOn: "2026-08-12", Kind: "transfer", TransferID: &transferID,
	})
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.bankID, PurposeID: f.mainID, Direction: "in", Amount: 600_000,
		OccurredOn: "2026-08-12", Kind: "transfer", TransferID: &transferID,
	})

	if got := f.fundBalance(t, sqlDB); got != before {
		t.Errorf("fund balance after the deposit = %d, want %d unchanged", got, before)
	}
	if got, want := f.accountBalance(t, sqlDB, f.cashID), int64(400_000); got != want {
		t.Errorf("cash after the deposit = %d, want %d", got, want)
	}
	if got, want := f.accountBalance(t, sqlDB, f.bankID), int64(600_000); got != want {
		t.Errorf("bank after the deposit = %d, want %d", got, want)
	}
}

func TestPassThroughMoneyCountsWhileItIsHeld(t *testing.T) {
	sqlDB := migratedTestDB(t)
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)
	bidangID := createPurpose(t, sqlDB, f.fundID, "pass_through", "Kas Bidang")

	// PRD §7.6 as amended (ADR-024): pass-through is descriptive, and drives no
	// arithmetic. While the money sits in the tin it is in the tin, so it is in
	// the balance - two headline figures that disagree is the wrong thing to put
	// on the calmest screen in the app.
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: bidangID, Direction: "in", Amount: 500_000,
		OccurredOn: "2026-08-01", Kind: "normal",
	})
	if got, want := f.fundBalance(t, sqlDB), int64(500_000); got != want {
		t.Errorf("fund balance while the money is held = %d, want %d", got, want)
	}

	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: bidangID, Direction: "out", Amount: 500_000,
		OccurredOn: "2026-08-09", Kind: "normal",
	})
	if got, want := f.fundBalance(t, sqlDB), int64(0); got != want {
		t.Errorf("fund balance after forwarding = %d, want %d", got, want)
	}
	if got, want := f.purposeBalance(t, sqlDB, bidangID), int64(0); got != want {
		t.Errorf("Kas Bidang after forwarding = %d, want %d", got, want)
	}
}

func TestOneMemberIsRepaidAndAnotherWaivesTheClaim(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)
	sri := createMember(t, sqlDB, f.fundID, "Bu Sri")
	budi := createMember(t, sqlDB, f.fundID, "Pak Budi")
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 500_000,
		OccurredOn: "2026-08-01", Kind: "opening",
	})

	// Fronting your own money does not move the kas, so neither claim is on the
	// ledger yet and the balance has not budged.
	sriClaim := createReimbursement(t, sqlDB, f.fundID, sri, f.mainID, 150_000)
	budiClaim, err := q.CreateReimbursement(ctx, store.CreateReimbursementParams{
		FundID: f.fundID, MemberID: budi, PurposeID: f.mainID, Amount: 80_000,
		IncurredOn: "2026-08-02", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement = %v, want no error", err)
	}
	if got, want := f.fundBalance(t, sqlDB), int64(500_000); got != want {
		t.Errorf("fund balance with two open claims = %d, want %d - a claim is off-ledger", got, want)
	}

	total, err := q.OutstandingReimbursementTotal(ctx, f.fundID)
	if err != nil {
		t.Fatalf("OutstandingReimbursementTotal = %v, want no error", err)
	}
	if want := int64(230_000); total != want {
		t.Errorf("outstanding total = %d, want %d", total, want)
	}

	// Bu Sri is paid back: one real 'out'. Pak Budi tells the treasurer to keep
	// it, which closes his claim without any money moving.
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 150_000,
		OccurredOn: "2026-08-15", Kind: "reimbursement", ReimbursementID: &sriClaim,
	})
	waived := "2026-08-16"
	if _, err := sqlDB.Exec(`UPDATE reimbursement SET waived_on = ? WHERE id = ?`, waived, budiClaim.ID); err != nil {
		t.Fatalf("waiving a claim = %v, want no error - reimbursement carries no immutability trigger", err)
	}

	open, err := q.ListOutstandingReimbursementsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListOutstandingReimbursementsByFund = %v, want no error", err)
	}
	if len(open) != 0 {
		t.Errorf("outstanding claims = %d, want 0 - one settled, one waived", len(open))
	}
	if got, want := f.fundBalance(t, sqlDB), int64(350_000); got != want {
		t.Errorf("fund balance = %d, want %d - only the payout touched the kas", got, want)
	}
}

func TestReconciliationRecordsWhatWasCountedAndHowItWasResolved(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)

	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 500_000,
		OccurredOn: "2026-08-01", Kind: "opening",
	})
	cutoff := f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.bankID, PurposeID: f.mainID, Direction: "in", Amount: 2_000_000,
		OccurredOn: "2026-08-01", Kind: "opening",
	})

	rec, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: 1_000, ThroughTransactionID: &cutoff, CreatedAt: 1_000,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}

	// The bank statement agrees to the rupiah.
	bankRecorded := frozenBalance(t, sqlDB, f, f.bankID, cutoff)
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.bankID,
		RecordedAmount: bankRecorded, ActualAmount: bankRecorded, DifferenceAmount: 0,
		Resolution: "matched",
	}); err != nil {
		t.Fatalf("a matched line = %v, want no error", err)
	}

	// The tin is 20000 short. The treasurer posts an adjusting entry - never an
	// edit to a posted row (CLAUDE.md rule 3) - and the line names it.
	cashRecorded := frozenBalance(t, sqlDB, f, f.cashID, cutoff)
	note := "selisih kas tunai"
	fix := f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 20_000,
		OccurredOn: "2026-08-31", Kind: "adjustment", Note: &note,
	})
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: f.fundID, ReconciliationID: rec.ID, AccountID: f.cashID,
		RecordedAmount: cashRecorded, ActualAmount: cashRecorded - 20_000,
		DifferenceAmount: -20_000, Resolution: "adjusted", AdjustmentTransactionID: &fix,
	}); err != nil {
		t.Fatalf("an adjusted line = %v, want no error", err)
	}

	diff, err := q.ReconciliationDifferenceTotal(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ReconciliationDifferenceTotal = %v, want no error", err)
	}
	if want := int64(-20_000); diff != want {
		t.Errorf("snapshot difference = %d, want %d", diff, want)
	}
	if got, want := f.fundBalance(t, sqlDB), int64(2_480_000); got != want {
		t.Errorf("fund balance after the adjustment = %d, want %d", got, want)
	}

	// A second fund, counted the same day, where the treasurer decides to sleep
	// on the difference instead. 'left_open' is a resolution, not a failure.
	g := newScenarioFund(t, sqlDB, "Kas RW", "zyxwvutsrqponmlkjihgfe")
	g.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: g.cashID, PurposeID: g.mainID, Direction: "in", Amount: 100_000,
		OccurredOn: "2026-08-01", Kind: "opening",
	})
	rec2, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: g.fundID, PerformedAt: 1_000, CreatedAt: 1_000,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: g.fundID, ReconciliationID: rec2.ID, AccountID: g.cashID,
		RecordedAmount: 100_000, ActualAmount: 95_000, DifferenceAmount: -5_000,
		Resolution: "left_open",
	}); err != nil {
		t.Fatalf("a left_open line = %v, want no error", err)
	}
	open, err := q.ListOpenReconciliationLinesByFund(ctx, g.fundID)
	if err != nil {
		t.Fatalf("ListOpenReconciliationLinesByFund = %v, want no error", err)
	}
	if len(open) != 1 {
		t.Fatalf("open lines = %d, want 1", len(open))
	}
	if open[0].DifferenceAmount != -5_000 {
		t.Errorf("open difference = %d, want -5000", open[0].DifferenceAmount)
	}
}

func TestALeftOpenDifferenceIsRevisitedAsASecondSnapshot(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Kas RT 05", validSlug)

	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 300_000,
		OccurredOn: "2026-08-01", Kind: "opening",
	})
	cutoff := f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 50_000,
		OccurredOn: "2026-08-05", Kind: "normal",
	})

	first, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: 1_000, ThroughTransactionID: &cutoff, CreatedAt: 1_000,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}
	recorded := frozenBalance(t, sqlDB, f, f.cashID, cutoff)
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: f.fundID, ReconciliationID: first.ID, AccountID: f.cashID,
		RecordedAmount: recorded, ActualAmount: 240_000, DifferenceAmount: 240_000 - recorded,
		Resolution: "left_open",
	}); err != nil {
		t.Fatalf("a left_open line = %v, want no error", err)
	}

	// A week later the missing receipt turns up: a 10000 expense from the 3rd,
	// posted now. It is backdated in occurred_on and current in id, which is the
	// whole reason the cutoff is an id - the old snapshot's arithmetic must not
	// change under it, and today's balance must.
	late := f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 10_000,
		OccurredOn: "2026-08-03", Kind: "adjustment",
	})
	if late <= cutoff {
		t.Fatalf("the late entry's id = %d, want it above the cutoff %d", late, cutoff)
	}
	if got := frozenBalance(t, sqlDB, f, f.cashID, cutoff); got != recorded {
		t.Errorf("the first snapshot's frozen figure = %d, want %d unchanged", got, recorded)
	}
	if got, want := f.accountBalance(t, sqlDB, f.cashID), int64(240_000); got != want {
		t.Errorf("today's cash balance = %d, want %d - the late entry counts now", got, want)
	}

	// Counted again, and this time it agrees. The first snapshot is untouched:
	// revisiting means a new row, never an edit.
	second, err := q.CreateReconciliation(ctx, store.CreateReconciliationParams{
		FundID: f.fundID, PerformedAt: 2_000, ThroughTransactionID: &late, CreatedAt: 2_000,
	})
	if err != nil {
		t.Fatalf("CreateReconciliation = %v, want no error", err)
	}
	if _, err := q.CreateReconciliationLine(ctx, store.CreateReconciliationLineParams{
		FundID: f.fundID, ReconciliationID: second.ID, AccountID: f.cashID,
		RecordedAmount: 240_000, ActualAmount: 240_000, DifferenceAmount: 0,
		Resolution: "matched",
	}); err != nil {
		t.Fatalf("the second snapshot's line = %v, want no error", err)
	}

	stillOpen, err := q.ListOpenReconciliationLinesByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListOpenReconciliationLinesByFund = %v, want no error", err)
	}
	if len(stillOpen) != 1 || stillOpen[0].ReconciliationID != first.ID {
		t.Errorf("open lines = %+v, want only the first snapshot's - history is not rewritten", stillOpen)
	}
	latest, err := q.LatestReconciliation(ctx, f.fundID)
	if err != nil {
		t.Fatalf("LatestReconciliation = %v, want no error", err)
	}
	if latest.ID != second.ID {
		t.Errorf("latest snapshot = %d, want %d", latest.ID, second.ID)
	}
}

// frozenBalance is what a snapshot stores in recorded_amount: the ledger summed
// up to the cutoff id, not to today.
func frozenBalance(t *testing.T, sqlDB *sql.DB, f scenarioFund, accountID, throughID int64) int64 {
	t.Helper()
	got, err := store.New(sqlDB).AccountBalanceThrough(context.Background(), store.AccountBalanceThroughParams{
		FundID: f.fundID, AccountID: accountID, ID: throughID,
	})
	if err != nil {
		t.Fatalf("AccountBalanceThrough(%d, %d) = %v, want no error", accountID, throughID, err)
	}
	return got
}
