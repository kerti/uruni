package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// transactionsForTransfer finds the two legs a transfer posted, the only
// way this package's own tests can inspect a pair's actual account/purpose
// values without a dedicated query - ListTransactionsByFund and a filter,
// the same approach every other transfer test in this package uses when it
// needs more than a balance.
func transactionsForTransfer(t *testing.T, q *store.Queries, fundID, transferID int64) []store.Transaction {
	t.Helper()
	rows, err := q.ListTransactionsByFund(context.Background(), fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	var legs []store.Transaction
	for _, row := range rows {
		if row.TransferID != nil && *row.TransferID == transferID {
			legs = append(legs, row)
		}
	}
	return legs
}

// legByDirection finds the one leg of legs carrying direction, failing the
// test if there is not exactly one - every pair postTransferPairTx writes
// has exactly one 'out' and one 'in'.
func legByDirection(t *testing.T, legs []store.Transaction, direction string) store.Transaction {
	t.Helper()
	var found []store.Transaction
	for _, l := range legs {
		if l.Direction == direction {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d legs with direction %q among %+v, want exactly 1", len(found), direction, legs)
	}
	return found[0]
}

// Value-neutrality (CLAUDE.md rule 2, ADR-033): a purpose correction is a
// same-account reclass_purpose pair, so the fund total and every account
// balance must be byte-identical before and after it - never assumed, and
// never allowed to drift by even one rupiah since money is int64 with no
// tolerance (ADR-015).
func TestPostPurposeCorrectionLeavesFundAndAccountBalancesUnchanged(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	// Seed both accounts so the fund is not accidentally starting at zero,
	// which would make "unchanged" trivially true.
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 500_000, OccurredOn: "2026-09-01",
	}); err != nil {
		t.Fatalf("seeding cash = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.bankID, PurposeID: f.mainID,
		Direction: "in", Amount: 300_000, OccurredOn: "2026-09-01",
	}); err != nil {
		t.Fatalf("seeding bank = %v, want no error", err)
	}

	// The row to correct: an expense on cash, mis-tagged to the pass-through
	// purpose instead of main.
	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "out", Amount: 120_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	fundBefore, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() before = %v, want no error", err)
	}
	cashBefore, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance(cash) before = %v, want no error", err)
	}
	bankBefore, err := l.AccountBalance(ctx, f.fundID, f.bankID)
	if err != nil {
		t.Fatalf("AccountBalance(bank) before = %v, want no error", err)
	}

	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	fundAfter, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after = %v, want no error", err)
	}
	cashAfter, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance(cash) after = %v, want no error", err)
	}
	bankAfter, err := l.AccountBalance(ctx, f.fundID, f.bankID)
	if err != nil {
		t.Fatalf("AccountBalance(bank) after = %v, want no error", err)
	}

	if fundBefore != fundAfter {
		t.Errorf("FundBalance() before=%d after=%d, want identical - a correction moves attribution, not money", fundBefore, fundAfter)
	}
	if cashBefore != cashAfter {
		t.Errorf("AccountBalance(cash) before=%d after=%d, want identical", cashBefore, cashAfter)
	}
	if bankBefore != bankAfter {
		t.Errorf("AccountBalance(bank) before=%d after=%d, want identical", bankBefore, bankAfter)
	}
}

// The crux (ADR-033's own words): an 'out' row mis-tagged leaves the wrong
// purpose as if the expense had never been tagged there (net back to zero)
// and the target as if it had always held it (net to -amount).
func TestPostPurposeCorrectionMovesPurposeBalancesForAnOutOriginal(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "out", Amount: 250_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	wrongAfter, err := l.PurposeBalance(ctx, f.fundID, f.passID)
	if err != nil {
		t.Fatalf("PurposeBalance(wrong) = %v, want no error", err)
	}
	if wrongAfter != 0 {
		t.Errorf("PurposeBalance(wrong tag) = %d, want 0 - the expense should read as if never tagged there", wrongAfter)
	}

	targetAfter, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(target) = %v, want no error", err)
	}
	if targetAfter != -250_000 {
		t.Errorf("PurposeBalance(target) = %d, want -250000 - as if the expense had always been tagged there", targetAfter)
	}
}

// The mirror image: an 'in' row mis-tagged (a contribution) must leave the
// wrong purpose back at zero and the target holding the full amount.
func TestPostPurposeCorrectionMovesPurposeBalancesForAnInOriginal(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "in", Amount: 90_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	}); err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	wrongAfter, err := l.PurposeBalance(ctx, f.fundID, f.passID)
	if err != nil {
		t.Fatalf("PurposeBalance(wrong) = %v, want no error", err)
	}
	if wrongAfter != 0 {
		t.Errorf("PurposeBalance(wrong tag) = %d, want 0 - the contribution should read as if never tagged there", wrongAfter)
	}

	targetAfter, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(target) = %v, want no error", err)
	}
	if targetAfter != 90_000 {
		t.Errorf("PurposeBalance(target) = %d, want 90000 - as if the contribution had always been tagged there", targetAfter)
	}
}

// The leg values themselves, not just the derived balances: an 'out'
// original's correction posts its 'out' leg at the target and its 'in' leg
// at the effective (here, stored) tag - the direction flip
// PostPurposeCorrection's own doc comment works out.
func TestPostPurposeCorrectionLegDirectionForAnOutOriginal(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "out", Amount: 40_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	correction, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	})
	if err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	legs := transactionsForTransfer(t, q, f.fundID, correction.ID)
	outLeg := legByDirection(t, legs, "out")
	inLeg := legByDirection(t, legs, "in")

	if outLeg.PurposeID != f.mainID {
		t.Errorf("out leg PurposeID = %d, want %d (target)", outLeg.PurposeID, f.mainID)
	}
	if inLeg.PurposeID != f.passID {
		t.Errorf("in leg PurposeID = %d, want %d (effective/original tag)", inLeg.PurposeID, f.passID)
	}
	if outLeg.AccountID != f.cashID || inLeg.AccountID != f.cashID {
		t.Errorf("legs AccountID = %d/%d, want both %d (the original row's own account)", outLeg.AccountID, inLeg.AccountID, f.cashID)
	}
}

// The second-correction case ADR-033 exists to prevent getting wrong: A->B,
// then B->C. The second pair must be built from B (the effective tag), not
// from A (the stored, now-stale tag) - and A's own balance must be
// untouched by the second correction.
func TestPostPurposeCorrectionSecondCorrectionUsesTheEffectiveTag(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	thirdID := createPurpose(t, q, f.fundID, "pass_through", "Third Purpose")

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID, // A
		Direction: "out", Amount: 70_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID, // A -> B
	}); err != nil {
		t.Fatalf("first PostPurposeCorrection() = %v, want no error", err)
	}

	balanceAAfterFirst, err := l.PurposeBalance(ctx, f.fundID, f.passID)
	if err != nil {
		t.Fatalf("PurposeBalance(A) after first = %v, want no error", err)
	}

	second, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: thirdID, // B -> C
	})
	if err != nil {
		t.Fatalf("second PostPurposeCorrection() = %v, want no error", err)
	}

	// The second pair's legs must name B (mainID) and C (thirdID) - never A
	// (passID), which a stored-tag bug would reach for instead.
	legs := transactionsForTransfer(t, q, f.fundID, second.ID)
	outLeg := legByDirection(t, legs, "out")
	inLeg := legByDirection(t, legs, "in")
	if outLeg.PurposeID != thirdID {
		t.Errorf("second correction's out leg PurposeID = %d, want %d (C, the new target)", outLeg.PurposeID, thirdID)
	}
	if inLeg.PurposeID != f.mainID {
		t.Errorf("second correction's in leg PurposeID = %d, want %d (B, the effective tag) - not %d (A, the stale stored tag)", inLeg.PurposeID, f.mainID, f.passID)
	}

	balanceAAfterSecond, err := l.PurposeBalance(ctx, f.fundID, f.passID)
	if err != nil {
		t.Fatalf("PurposeBalance(A) after second = %v, want no error", err)
	}
	if balanceAAfterSecond != balanceAAfterFirst {
		t.Errorf("PurposeBalance(A) changed from %d to %d after correcting B -> C; A must be untouched by a correction that no longer involves it", balanceAAfterFirst, balanceAAfterSecond)
	}

	balanceB, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(B) = %v, want no error", err)
	}
	if balanceB != 0 {
		t.Errorf("PurposeBalance(B) = %d, want 0 - the second correction should undo B's holding entirely", balanceB)
	}

	balanceC, err := l.PurposeBalance(ctx, f.fundID, thirdID)
	if err != nil {
		t.Fatalf("PurposeBalance(C) = %v, want no error", err)
	}
	if balanceC != -70_000 {
		t.Errorf("PurposeBalance(C) = %d, want -70000", balanceC)
	}
}

// A correction's own legs are kind='transfer', and ErrPurposeCorrectionTransfer
// is what makes correcting a correction impossible - no depth limit, no
// cycle rule (ADR-033).
func TestPostPurposeCorrectionCannotCorrectACorrection(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "out", Amount: 10_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	correction, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	})
	if err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	legs := transactionsForTransfer(t, q, f.fundID, correction.ID)
	for _, leg := range legs {
		if leg.Kind != "transfer" {
			t.Fatalf("correction leg Kind = %q, want %q", leg.Kind, "transfer")
		}
		_, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
			FundID: f.fundID, TransactionID: leg.ID, PurposeID: f.passID,
		})
		if !errors.Is(err, ErrPurposeCorrectionTransfer) {
			t.Errorf("PostPurposeCorrection(a correction's own leg, id=%d) = %v, want ErrPurposeCorrectionTransfer", leg.ID, err)
		}
	}
}

func TestPostPurposeCorrectionRejectsOpeningBalance(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	account, err := l.CreateAccount(ctx, CreateAccountParams{
		FundID: f.fundID, Kind: "cash", Name: "Third Account",
		OpeningBalance: &OpeningBalance{Amount: 100_000, OccurredOn: "2026-09-01"},
	})
	if err != nil {
		t.Fatalf("CreateAccount() = %v, want no error", err)
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	var openingID int64
	for _, row := range rows {
		if row.Kind == "opening" && row.AccountID == account.ID {
			openingID = row.ID
		}
	}
	if openingID == 0 {
		t.Fatal("no opening balance row found for the created account")
	}

	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: openingID, PurposeID: f.passID,
	})
	if !errors.Is(err, ErrPurposeCorrectionOpening) {
		t.Errorf("PostPurposeCorrection(opening) = %v, want ErrPurposeCorrectionOpening", err)
	}
}

func TestPostPurposeCorrectionRejectsDuesPayment(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, OccurredOn: "2026-09-05",
		Periods: []PeriodAmount{{DuesPeriod: "2026-09", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted[0].ID, PurposeID: f.passID,
	})
	if !errors.Is(err, ErrPurposeCorrectionDues) {
		t.Errorf("PostPurposeCorrection(dues) = %v, want ErrPurposeCorrectionDues", err)
	}
}

func TestPostPurposeCorrectionRejectsReimbursementPayout(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 60_000, "2026-09-01", nil)
	posted, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID,
		AccountID: f.cashID, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}

	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.passID,
	})
	if !errors.Is(err, ErrPurposeCorrectionReimbursement) {
		t.Errorf("PostPurposeCorrection(reimbursement) = %v, want ErrPurposeCorrectionReimbursement", err)
	}
}

func TestPostPurposeCorrectionRejectsTransferLeg(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	transfer, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID,
		FromAccountID: f.cashID, ToAccountID: f.bankID,
		Amount: 15_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
	}

	legs := transactionsForTransfer(t, q, f.fundID, transfer.ID)
	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: legs[0].ID, PurposeID: f.passID,
	})
	if !errors.Is(err, ErrPurposeCorrectionTransfer) {
		t.Errorf("PostPurposeCorrection(transfer leg) = %v, want ErrPurposeCorrectionTransfer", err)
	}
}

func TestPostPurposeCorrectionRejectsDuesReversal(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: f.memberID, OccurredOn: "2026-09-05",
		Periods: []PeriodAmount{{DuesPeriod: "2026-09", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	reversal, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: posted[0].ID, OccurredOn: "2026-09-06",
	})
	if err != nil {
		t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
	}

	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: reversal.ID, PurposeID: f.passID,
	})
	if !errors.Is(err, ErrPurposeCorrectionDuesReversal) {
		t.Errorf("PostPurposeCorrection(dues reversal) = %v, want ErrPurposeCorrectionDuesReversal", err)
	}
}

// An ordinary reconciliation fix - kind='adjustment' with no
// reverses_transaction_id - is the other adjustment shape, and ADR-033
// says it is eligible, not refused.
func TestPostPurposeCorrectionAcceptsOrdinaryAdjustment(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-09-05",
		IsAdjustment: true,
	})
	if err != nil {
		t.Fatalf("PostTransaction(adjustment) = %v, want no error", err)
	}

	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	}); err != nil {
		t.Errorf("PostPurposeCorrection(ordinary adjustment) = %v, want no error", err)
	}
}

// Correcting into a closed incidental is refused in both directions
// (ADR-031, ADR-033). This is the target-side case: the row being corrected
// is an ordinary mis-tag, but the requested destination is closed.
func TestPostPurposeCorrectionRejectsClosedTarget(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	closedEnvelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Closed Envelope", OpenedOn: "2026-09-01",
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}
	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: closedEnvelope.PurposeID,
		AccountID: f.cashID, ClosedOn: "2026-09-02",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "out", Amount: 20_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: closedEnvelope.PurposeID,
	})
	if !errors.Is(err, ErrPurposeCorrectionTargetClosed) {
		t.Errorf("PostPurposeCorrection(into closed incidental) = %v, want ErrPurposeCorrectionTargetClosed", err)
	}
}

// The source-side case: the row's effective peruntukan (still its stored
// tag - nothing has corrected it) is a now-closed incidental. Correcting it
// out would leave that envelope's balance non-zero, breaking the rollover
// invariant #270 reads (ADR-031, ADR-033).
func TestPostPurposeCorrectionRejectsClosedSource(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	envelope, err := l.OpenIncidental(ctx, OpenIncidentalParams{
		FundID: f.fundID, Occasion: "Birthday", OpenedOn: "2026-09-01",
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v, want no error", err)
	}

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: envelope.PurposeID,
		Direction: "in", Amount: 10_000, OccurredOn: "2026-09-02",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	if _, err := l.CloseIncidentalAndRoll(ctx, CloseIncidentalAndRollParams{
		FundID: f.fundID, PurposeID: envelope.PurposeID,
		AccountID: f.cashID, ClosedOn: "2026-09-10",
	}); err != nil {
		t.Fatalf("CloseIncidentalAndRoll() = %v, want no error", err)
	}

	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.passID,
	})
	if !errors.Is(err, ErrPurposeCorrectionSourceClosed) {
		t.Errorf("PostPurposeCorrection(out of closed incidental) = %v, want ErrPurposeCorrectionSourceClosed", err)
	}
}

func TestPostPurposeCorrectionRejectsNoop(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.passID,
	})
	if !errors.Is(err, ErrPurposeCorrectionNoop) {
		t.Errorf("PostPurposeCorrection(no-op) = %v, want ErrPurposeCorrectionNoop", err)
	}
}

// A transaction id real in one fund but named from another must answer the
// same not-found error as an id that does not exist at all (ADR-029's own
// fund-scoped-fetch reasoning, applied here) - never be found and only then
// rejected.
func TestPostPurposeCorrectionIsFundScoped(t *testing.T) {
	l := newTestLedger(t)
	f1 := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f1.fundID, AccountID: f1.cashID, PurposeID: f1.passID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	// A second fund, built by hand: newFixture hard-codes report_slug, so a
	// second fund needs its own distinct one (the same approach
	// TestReverseDuesPaymentRefusesAnotherFundsTransaction uses in
	// dues_reversal_test.go).
	other, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	otherMainID := createPurpose(t, q, other.ID, "main", "Primary Cash")

	_, err = l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: other.ID, TransactionID: posted.ID, PurposeID: otherMainID,
	})
	if !errors.Is(err, ErrPurposeCorrectionNotFound) {
		t.Errorf("PostPurposeCorrection(cross-fund id) = %v, want ErrPurposeCorrectionNotFound", err)
	}

	// And the row must still be correctable, untouched, from its real fund -
	// proving the refusal above was scoping, not a corrupted row.
	if _, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f1.fundID, TransactionID: posted.ID, PurposeID: f1.mainID,
	}); err != nil {
		t.Errorf("PostPurposeCorrection(fund 1, fund 1's own transaction) = %v, want no error", err)
	}
}

func TestPostPurposeCorrectionUnknownTransactionID(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: 999_999, PurposeID: f.mainID,
	})
	if !errors.Is(err, ErrPurposeCorrectionNotFound) {
		t.Errorf("PostPurposeCorrection(unknown id) = %v, want ErrPurposeCorrectionNotFound", err)
	}
}

// The pair carries the original row's own account_id and occurred_on - the
// date is not an input (ADR-033) - never today's date or a caller-supplied
// account.
func TestPostPurposeCorrectionLegsCarryTheOriginalAccountAndDate(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.bankID, PurposeID: f.passID,
		Direction: "out", Amount: 5_000, OccurredOn: "2019-01-15",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	correction, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	})
	if err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	for _, leg := range transactionsForTransfer(t, q, f.fundID, correction.ID) {
		if leg.AccountID != f.bankID {
			t.Errorf("leg AccountID = %d, want %d (the original row's own account)", leg.AccountID, f.bankID)
		}
		if leg.OccurredOn != "2019-01-15" {
			t.Errorf("leg OccurredOn = %q, want %q (the original row's own date, not today)", leg.OccurredOn, "2019-01-15")
		}
	}
}

// The transfer row itself carries corrects_transaction_id naming the
// original row (ADR-033) - the link a roll never sets.
func TestPostPurposeCorrectionSetsCorrectsTransactionID(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.passID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-09-05",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	correction, err := l.PostPurposeCorrection(ctx, PostPurposeCorrectionParams{
		FundID: f.fundID, TransactionID: posted.ID, PurposeID: f.mainID,
	})
	if err != nil {
		t.Fatalf("PostPurposeCorrection() = %v, want no error", err)
	}

	if correction.CorrectsTransactionID == nil || *correction.CorrectsTransactionID != posted.ID {
		t.Errorf("CorrectsTransactionID = %v, want %d", correction.CorrectsTransactionID, posted.ID)
	}
	if correction.Kind != "reclass_purpose" {
		t.Errorf("Kind = %q, want %q", correction.Kind, "reclass_purpose")
	}
}
