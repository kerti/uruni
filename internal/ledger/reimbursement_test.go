package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
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

// An unsettled claim is correctable in place: it is off the ledger, so
// CLAUDE.md rule 3's "corrections are new adjusting entries" does not reach
// it, and the schema agrees by carrying no immutability trigger.
func TestUpdateReimbursementCorrectsAnUnsettledClaim(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)

	amount := money.Amount(90_000)
	incurredOn := "2026-08-02"
	note := "the receipt said ninety"
	updated, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID,
		Amount: &amount, IncurredOn: &incurredOn, Note: &note, SetNote: true,
	})
	if err != nil {
		t.Fatalf("UpdateReimbursement() = %v, want no error", err)
	}
	if updated.Amount != 90_000 {
		t.Errorf("Amount = %d, want 90000", updated.Amount)
	}
	if updated.IncurredOn != "2026-08-02" {
		t.Errorf("IncurredOn = %q, want %q", updated.IncurredOn, "2026-08-02")
	}
	if updated.Note == nil || *updated.Note != note {
		t.Errorf("Note = %v, want %q", updated.Note, note)
	}
	// Untouched fields keep their values - an absent field is "leave alone",
	// never "clear it".
	if updated.MemberID != claim.MemberID || updated.PurposeID != claim.PurposeID {
		t.Errorf("member/purpose = %d/%d, want the claim's own %d/%d",
			updated.MemberID, updated.PurposeID, claim.MemberID, claim.PurposeID)
	}
	if updated.WaivedOn != nil {
		t.Errorf("WaivedOn = %v, want nil - it was never sent", updated.WaivedOn)
	}
}

// Waiving and un-waiving are the same call with SetWaivedOn, which is the
// whole reason waiving is a field rather than a route: the member who says
// "saya yang tanggung" can change their mind, and a dedicated /waive route
// would leave the claim stuck.
func TestUpdateReimbursementWaivesAndUnwaives(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)

	waivedOn := "2026-08-05"
	waived, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID,
		WaivedOn: &waivedOn, SetWaivedOn: true,
	})
	if err != nil {
		t.Fatalf("UpdateReimbursement() waiving = %v, want no error", err)
	}
	if waived.WaivedOn == nil || *waived.WaivedOn != waivedOn {
		t.Fatalf("WaivedOn = %v, want %q", waived.WaivedOn, waivedOn)
	}

	// A waived claim is no longer owed, so it leaves both outstanding views.
	outstanding, err := q.ListOutstandingReimbursementsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListOutstandingReimbursementsByFund() = %v, want no error", err)
	}
	if len(outstanding) != 0 {
		t.Errorf("outstanding claims = %d, want 0 - a waived claim is not owed", len(outstanding))
	}
	total, err := q.OutstandingReimbursementTotal(ctx, f.fundID)
	if err != nil {
		t.Fatalf("OutstandingReimbursementTotal() = %v, want no error", err)
	}
	if total != 0 {
		t.Errorf("outstanding total = %d, want 0", total)
	}

	// It is still history: the unfiltered list keeps it.
	all, err := q.ListReimbursementsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListReimbursementsByFund() = %v, want no error", err)
	}
	if len(all) != 1 {
		t.Errorf("all claims = %d, want 1 - waiving is not deleting", len(all))
	}

	unwaived, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, SetWaivedOn: true,
	})
	if err != nil {
		t.Fatalf("UpdateReimbursement() un-waiving = %v, want no error", err)
	}
	if unwaived.WaivedOn != nil {
		t.Errorf("WaivedOn = %v, want nil after un-waiving", unwaived.WaivedOn)
	}

	outstanding, err = q.ListOutstandingReimbursementsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListOutstandingReimbursementsByFund() = %v, want no error", err)
	}
	if len(outstanding) != 1 {
		t.Errorf("outstanding claims after un-waiving = %d, want 1", len(outstanding))
	}
}

// A waived claim still cannot be settled - the two rules compose, and
// waiving through this method reaches the same refusal a claim created
// waived already got.
func TestSettleRefusesAClaimWaivedThroughUpdate(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)

	waivedOn := "2026-08-05"
	if _, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, WaivedOn: &waivedOn, SetWaivedOn: true,
	}); err != nil {
		t.Fatalf("UpdateReimbursement() = %v, want no error", err)
	}

	_, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: "2026-08-12",
	})
	if !errors.Is(err, ErrReimbursementWaived) {
		t.Fatalf("SettleReimbursement() = %v, want ErrReimbursementWaived", err)
	}
}

// Settlement is the boundary. Once a payout row copies the claim's amount
// and purpose onto an immutable transaction, correcting or deleting the
// claim would let the two disagree while both look authoritative.
func TestUpdateAndDeleteRefuseASettledClaim(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)
	if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}

	amount := money.Amount(90_000)
	if _, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, Amount: &amount,
	}); !errors.Is(err, ErrReimbursementAlreadySettled) {
		t.Errorf("UpdateReimbursement() on a settled claim = %v, want ErrReimbursementAlreadySettled", err)
	}

	if err := l.DeleteReimbursement(ctx, f.fundID, claim.ID); !errors.Is(err, ErrReimbursementAlreadySettled) {
		t.Errorf("DeleteReimbursement() on a settled claim = %v, want ErrReimbursementAlreadySettled", err)
	}

	// And nothing was written: the claim still reads as it did.
	after, err := q.GetReimbursement(ctx, claim.ID)
	if err != nil {
		t.Fatalf("GetReimbursement() = %v, want no error", err)
	}
	if after.Amount != 75_000 {
		t.Errorf("Amount = %d, want the original 75000 - the refusal must not have written", after.Amount)
	}
}

// Waiving a settled claim is refused for the same reason: it is neither
// owed nor forgiven, it is paid.
func TestUpdateRefusesToWaiveASettledClaim(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)
	if _, err := l.SettleReimbursement(ctx, SettleReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v, want no error", err)
	}

	waivedOn := "2026-08-20"
	_, err := l.UpdateReimbursement(ctx, UpdateReimbursementParams{
		FundID: f.fundID, ReimbursementID: claim.ID, WaivedOn: &waivedOn, SetWaivedOn: true,
	})
	if !errors.Is(err, ErrReimbursementAlreadySettled) {
		t.Fatalf("UpdateReimbursement() waiving a settled claim = %v, want ErrReimbursementAlreadySettled", err)
	}
}

func TestDeleteReimbursementRemovesAnUnsettledClaim(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)

	if err := l.DeleteReimbursement(ctx, f.fundID, claim.ID); err != nil {
		t.Fatalf("DeleteReimbursement() = %v, want no error", err)
	}

	all, err := q.ListReimbursementsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListReimbursementsByFund() = %v, want no error", err)
	}
	if len(all) != 0 {
		t.Errorf("claims after delete = %d, want 0", len(all))
	}
}

// Argument-shape validation is this method's own (ADR-027), so the message
// names the field rather than leaving SQLite's CHECK to say "constraint".
func TestUpdateReimbursementRejectsBadArguments(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)

	zero := money.Amount(0)
	badDate := "2026-02-30"
	for _, tc := range []struct {
		name string
		p    UpdateReimbursementParams
	}{
		{"non-positive amount", UpdateReimbursementParams{Amount: &zero}},
		{"malformed incurred_on", UpdateReimbursementParams{IncurredOn: &badDate}},
		{"malformed waived_on", UpdateReimbursementParams{WaivedOn: &badDate, SetWaivedOn: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			p.FundID, p.ReimbursementID = f.fundID, claim.ID
			if _, err := l.UpdateReimbursement(ctx, p); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("UpdateReimbursement() = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
