package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// createReimbursement is the test helper for a raw claim: there is no
// domain wrapper (ADR-027 - "no CreateReimbursement domain wrapper"), so M4
// and these tests both call store.Queries directly, exactly like members.
func createReimbursement(t *testing.T, q *store.Queries, f fixture, amount int64, incurredOn string, waivedOn *string) store.Reimbursement {
	t.Helper()
	r, err := q.CreateReimbursement(context.Background(), store.CreateReimbursementParams{
		FundID:     f.fundID,
		MemberID:   f.memberID,
		PurposeID:  f.mainID,
		Amount:     amount,
		IncurredOn: incurredOn,
		WaivedOn:   waivedOn,
		CreatedAt:  1,
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}
	return r
}

// Settling posts exactly one kind='reimbursement', direction='out' row
// carrying the claim's own amount, purpose_id and id - never a figure or
// purpose the caller supplies.
func TestSettleReimbursementPostsOneOutRowOfClaimAmount(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)

	posted, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID,
		OccurredOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}
	if posted.Kind != "reimbursement" {
		t.Errorf("Kind = %q, want %q", posted.Kind, "reimbursement")
	}
	if posted.Direction != "out" {
		t.Errorf("Direction = %q, want %q", posted.Direction, "out")
	}
	if posted.Amount != claim.Amount {
		t.Errorf("Amount = %d, want the claim's own amount %d", posted.Amount, claim.Amount)
	}
	if posted.PurposeID != claim.PurposeID {
		t.Errorf("PurposeID = %d, want the claim's own purpose_id %d", posted.PurposeID, claim.PurposeID)
	}
	if posted.ReimbursementID == nil || *posted.ReimbursementID != claim.ID {
		t.Errorf("ReimbursementID = %v, want a pointer to %d", posted.ReimbursementID, claim.ID)
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ledger holds %d rows, want exactly 1", len(rows))
	}
}

// ADR-024's whole reason reimbursement is its own table: creating a claim
// does not move the kas, so FundBalance must be unchanged the moment the
// claim exists, and only moves by exactly the amount when it is settled.
// Both halves are asserted, not just the second.
func TestSettleReimbursementFundBalanceUnchangedUntilSettled(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 200_000, OccurredOn: "2026-07-01",
	}); err != nil {
		t.Fatalf("PostOpeningBalance() = %v, want no error", err)
	}

	baseline, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if baseline != 200_000 {
		t.Fatalf("baseline FundBalance() = %d, want %d", baseline, 200_000)
	}

	claim := createReimbursement(t, q, f, 30_000, "2026-08-01", nil)

	// Creating the claim - no ledger row, no domain call at all - must not
	// move the fund's balance.
	afterClaim, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after claim creation = %v, want no error", err)
	}
	if afterClaim != baseline {
		t.Errorf("FundBalance() after claim creation = %d, want unchanged at %d - a reimbursement claim must sit off the ledger until settled", afterClaim, baseline)
	}

	if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID,
		OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}

	afterSettle, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after settlement = %v, want no error", err)
	}
	if want := baseline - 30_000; afterSettle != want {
		t.Errorf("FundBalance() after settlement = %d, want %d (baseline %d minus the claim's %d)", afterSettle, want, baseline, 30_000)
	}
}

// Settling twice is refused with the named sentinel, and posts nothing on
// the second call - proven by asserting the row count, not only the error.
func TestSettleReimbursementTwiceIsRefused(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 40_000, "2026-08-01", nil)

	if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID,
		OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("first SettleReimbursement() = %v, want no error", err)
	}

	_, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.bankID,
		OccurredOn: "2026-08-13",
	})
	if !errors.Is(err, ErrReimbursementAlreadySettled) {
		t.Fatalf("second SettleReimbursement() = %v, want an error wrapping ErrReimbursementAlreadySettled", err)
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Errorf("ledger holds %d rows after a refused second settlement, want exactly 1", len(rows))
	}
}

// A waived claim can never be settled - it will never be repaid - and the
// refusal posts nothing.
func TestSettleReimbursementWaivedClaimIsRefused(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	waivedOn := "2026-08-05"
	claim := createReimbursement(t, q, f, 20_000, "2026-08-01", &waivedOn)

	_, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID,
		OccurredOn: "2026-08-12",
	})
	if !errors.Is(err, ErrReimbursementWaived) {
		t.Fatalf("SettleReimbursement(waived claim) = %v, want an error wrapping ErrReimbursementWaived", err)
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a refused waived settlement, want 0", len(rows))
	}
}

// The schema's reimbursement_settled_once index is the actual guarantee,
// independent of the domain's pre-check: inserting a second
// kind='reimbursement' row for the same claim directly through raw
// store.Queries, bypassing SettleReimbursement entirely, must still be
// refused. This is the test that would catch the pre-check being removed -
// the same shape as opening_balance_test.go's index test.
func TestReimbursementSettledOnceIndexRefusesASecondRowInsertedDirectly(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 50_000, "2026-08-01", nil)

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: claim.PurposeID,
		Direction: "out", Amount: claim.Amount, OccurredOn: "2026-08-12",
		Kind: "reimbursement", ReimbursementID: &claim.ID, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("first CreateTransaction() = %v, want no error", err)
	}

	_, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.bankID, PurposeID: claim.PurposeID,
		Direction: "out", Amount: claim.Amount, OccurredOn: "2026-08-13",
		Kind: "reimbursement", ReimbursementID: &claim.ID, CreatedAt: 2,
	})
	if err == nil {
		t.Fatal("second CreateTransaction(kind='reimbursement') for the same claim = nil error, want a unique constraint violation")
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Errorf("ledger holds %d rows after a refused direct insert, want exactly 1", len(rows))
	}
}

// ADR-024's accepted cost, made visible rather than assumed: the expense
// lands on the ledger at the settle date, while incurred_on keeps the truth
// about when the member actually spent the money. A claim incurred in one
// month and settled the next shows each field carrying a different date.
func TestSettleReimbursementLedgerDateIsTheSettleDateNotIncurredOn(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	const incurredOn = "2026-07-15" // the member spent their own money in July
	const settledOn = "2026-08-12"  // the fund pays it back in August

	claim := createReimbursement(t, q, f, 60_000, incurredOn, nil)

	posted, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID,
		OccurredOn: settledOn,
	})
	if err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}

	if posted.OccurredOn != settledOn {
		t.Errorf("posted.OccurredOn = %q, want the settle date %q", posted.OccurredOn, settledOn)
	}

	refetched, err := q.GetReimbursement(ctx, claim.ID)
	if err != nil {
		t.Fatalf("GetReimbursement() = %v, want no error", err)
	}
	if refetched.IncurredOn != incurredOn {
		t.Errorf("reimbursement.IncurredOn = %q, want the untouched incurred date %q", refetched.IncurredOn, incurredOn)
	}

	if posted.OccurredOn == refetched.IncurredOn {
		t.Fatalf("posted.OccurredOn (%q) and reimbursement.IncurredOn (%q) must differ for this test to prove anything", posted.OccurredOn, refetched.IncurredOn)
	}
}

// A malformed occurred_on is rejected before any write reaches the schema,
// exactly like PostTransaction's and PostOpeningBalance's own check.
func TestSettleReimbursementRejectsInvalidOccurredOn(t *testing.T) {
	tests := []struct {
		name       string
		occurredOn string
	}{
		{"malformed", "12 August 2026"},
		{"calendar-invalid", "2026-02-30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()
			q := store.New(l.db)

			claim := createReimbursement(t, q, f, 15_000, "2026-08-01", nil)

			_, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
				FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID,
				OccurredOn: tt.occurredOn,
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("SettleReimbursement() = %v, want an error wrapping ErrInvalidArgument", err)
			}

			rows, err := q.ListTransactionsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
			}
			if len(rows) != 0 {
				t.Errorf("ledger holds %d rows after a rejected settlement, want 0 - the claim lookup should never have been reached", len(rows))
			}
		})
	}
}
