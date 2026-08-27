package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// TestReverseDuesPaymentRoundTripsBalanceAndPaidStatus is the acceptance
// criterion from #82, both halves in one test: pay, then reverse, and (a)
// the member reads as UNPAID for that period again, and (b) the fund
// balance is back to exactly what it was before the payment - an integer
// comparison, no tolerance (ADR-015).
func TestReverseDuesPaymentRoundTripsBalanceAndPaidStatus(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	balanceBefore, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() before payment = %v, want no error", err)
	}

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	paymentID := posted[0].ID

	balanceAfterPayment, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after payment = %v, want no error", err)
	}
	if balanceAfterPayment != balanceBefore+25_000 {
		t.Fatalf("FundBalance() after payment = %d, want %d", balanceAfterPayment, balanceBefore+25_000)
	}

	statusBefore, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-08")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() before reversal = %v, want no error", err)
	}
	gotBefore, ok := statusFor(t, statusBefore, memberID)
	if !ok || gotBefore.Status != DuesStatusPaid {
		t.Fatalf("member status before reversal = %+v, ok=%v, want Paid", gotBefore, ok)
	}

	reversal, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: paymentID, OccurredOn: "2026-08-15",
	})
	if err != nil {
		t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
	}
	if reversal.Kind != "adjustment" {
		t.Errorf("reversal Kind = %q, want %q", reversal.Kind, "adjustment")
	}
	if reversal.Direction != "out" {
		t.Errorf("reversal Direction = %q, want %q", reversal.Direction, "out")
	}
	if reversal.Amount != 25_000 {
		t.Errorf("reversal Amount = %d, want 25000 (copied from the original)", reversal.Amount)
	}
	if reversal.MemberID == nil || *reversal.MemberID != memberID {
		t.Errorf("reversal MemberID = %v, want %d (copied from the original)", reversal.MemberID, memberID)
	}
	if reversal.DuesPeriod == nil || *reversal.DuesPeriod != "2026-08" {
		t.Errorf("reversal DuesPeriod = %v, want %q (copied from the original)", reversal.DuesPeriod, "2026-08")
	}

	// (a) the member reads as UNPAID for that period again.
	statusAfter, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-08")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod() after reversal = %v, want no error", err)
	}
	gotAfter, ok := statusFor(t, statusAfter, memberID)
	if !ok {
		t.Fatalf("member %d missing from roster after reversal", memberID)
	}
	if gotAfter.Status != DuesStatusUnpaid {
		t.Errorf("Status after reversal = %q, want %q", gotAfter.Status, DuesStatusUnpaid)
	}
	if gotAfter.PaidAmount != 0 {
		t.Errorf("PaidAmount after reversal = %d, want 0", gotAfter.PaidAmount)
	}

	// (b) the fund balance is back to exactly what it was before the
	// payment - an integer comparison, no tolerance.
	balanceAfterReversal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after reversal = %v, want no error", err)
	}
	if balanceAfterReversal != balanceBefore {
		t.Fatalf("FundBalance() after reversal = %d, want %d (exactly the pre-payment balance)", balanceAfterReversal, balanceBefore)
	}
}

// TestReverseDuesPaymentTwiceIsRefused: a payment is reversible at most
// once - the dues_payment_reversed_once partial unique index, surfaced as
// the named ErrDuesPaymentAlreadyReversed rather than a raw constraint
// string.
func TestReverseDuesPaymentTwiceIsRefused(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	paymentID := posted[0].ID

	if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: paymentID, OccurredOn: "2026-08-15",
	}); err != nil {
		t.Fatalf("first ReverseDuesPayment() = %v, want no error", err)
	}

	_, err = l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: paymentID, OccurredOn: "2026-08-20",
	})
	if !errors.Is(err, ErrDuesPaymentAlreadyReversed) {
		t.Fatalf("second ReverseDuesPayment() = %v, want an error wrapping ErrDuesPaymentAlreadyReversed", err)
	}
}

// TestReverseDuesPaymentRefusesANonDuesTransaction: only a kind='dues' row
// can be reversed through this method - PRD §4 keeps it exactly as wide as
// dues, never a generic reverse-any-transaction primitive.
func TestReverseDuesPaymentRefusesANonDuesTransaction(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	ordinary, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-01",
	})
	if err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	_, err = l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: ordinary.ID, OccurredOn: "2026-08-02",
	})
	if !errors.Is(err, ErrNotADuesPayment) {
		t.Fatalf("ReverseDuesPayment(ordinary transaction) = %v, want an error wrapping ErrNotADuesPayment", err)
	}
}

// TestReverseDuesPaymentRefusesReversingAReversal: a reversal is itself
// posted as kind='adjustment', never kind='dues' (ADR-029), so it fails the
// same ErrNotADuesPayment check as any other non-dues row - ruling out
// reversing a reversal without a separate check.
func TestReverseDuesPaymentRefusesReversingAReversal(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	reversal, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: posted[0].ID, OccurredOn: "2026-08-15",
	})
	if err != nil {
		t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
	}

	_, err = l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: reversal.ID, OccurredOn: "2026-08-20",
	})
	if !errors.Is(err, ErrNotADuesPayment) {
		t.Fatalf("ReverseDuesPayment(a reversal) = %v, want an error wrapping ErrNotADuesPayment", err)
	}
}

// TestReverseDuesPaymentRefusesAnotherFundsTransaction: the fetch behind
// ReverseDuesPayment is fund-scoped (GetTransactionForFund: WHERE fund_id =
// ? AND id = ?), which is what the composite FK on reverses_transaction_id
// exists to back at the schema level. A transaction id that is real but
// belongs to another fund must fail for the SAME reason as one that does
// not exist at all - ErrDuesPaymentNotFound, not a generic error and not a
// silent cross-fund reversal.
func TestReverseDuesPaymentRefusesAnotherFundsTransaction(t *testing.T) {
	l := newTestLedger(t)
	f1 := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tier1 := createDuesTier(t, q, f1.fundID, "Tier A")
	createDuesRate(t, q, tier1, 25_000, "2026-01")
	member1 := createDuesMember(t, q, f1.fundID, duesMemberParams{name: "Jane", tierID: &tier1})

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f1.fundID, AccountID: f1.cashID, PurposeID: f1.mainID,
		MemberID: member1, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}
	paymentID := posted[0].ID

	// A second fund, built by hand (mirrors TestDuesStatusForPeriodScopesToOneFund
	// in dues_status_test.go): newFixture hard-codes report_slug, so a second
	// fund needs its own distinct one.
	other, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}

	_, err = l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: other.ID, TransactionID: paymentID, OccurredOn: "2026-08-15",
	})
	if !errors.Is(err, ErrDuesPaymentNotFound) {
		t.Fatalf("ReverseDuesPayment(fund 2, fund 1's transaction id) = %v, want an error wrapping ErrDuesPaymentNotFound", err)
	}

	// And the payment must still be reversible, untouched, from its real fund -
	// proving the refusal above was scoping, not a corrupted or consumed row.
	if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f1.fundID, TransactionID: paymentID, OccurredOn: "2026-08-16",
	}); err != nil {
		t.Fatalf("ReverseDuesPayment(fund 1, fund 1's own transaction) = %v, want no error", err)
	}
}

// TestReverseDuesPaymentRejectsInvalidOccurredOn mirrors
// PostDuesPayments' own argument-shape validation (ADR-027): checked before
// anything is fetched or written.
func TestReverseDuesPaymentRejectsInvalidOccurredOn(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	_, err = l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: posted[0].ID, OccurredOn: "2026-02-30",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ReverseDuesPayment() = %v, want an error wrapping ErrInvalidArgument", err)
	}
}

// TestReverseDuesPaymentReversedPeriodNoLongerCountsTowardDuesPaidByPeriod
// exercises DuesPaidByPeriod directly (not through DuesStatusForPeriod),
// proving the AND NOT EXISTS clause itself: a reversed period must vanish
// from the paid-amount sum, not merely read as Unpaid through some
// downstream classification.
func TestReverseDuesPaymentReversedPeriodNoLongerCountsTowardDuesPaidByPeriod(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane"})

	posted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-08-12",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments() = %v, want no error", err)
	}

	period := "2026-08"
	before, err := q.DuesPaidByPeriod(ctx, store.DuesPaidByPeriodParams{FundID: f.fundID, DuesPeriod: &period})
	if err != nil {
		t.Fatalf("DuesPaidByPeriod() before reversal = %v, want no error", err)
	}
	if len(before) != 1 || before[0].PaidAmount != 25_000 {
		t.Fatalf("DuesPaidByPeriod() before reversal = %+v, want one row of 25000", before)
	}

	if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: posted[0].ID, OccurredOn: "2026-08-15",
	}); err != nil {
		t.Fatalf("ReverseDuesPayment() = %v, want no error", err)
	}

	after, err := q.DuesPaidByPeriod(ctx, store.DuesPaidByPeriodParams{FundID: f.fundID, DuesPeriod: &period})
	if err != nil {
		t.Fatalf("DuesPaidByPeriod() after reversal = %v, want no error", err)
	}
	if len(after) != 0 {
		t.Fatalf("DuesPaidByPeriod() after reversal = %+v, want no rows - the reversed period contributes nothing", after)
	}
}

// TestReverseDuesPaymentReversedPeriodDoesNotReadAsPaidInAdvance is the
// specific bug ADR-029's chosen design exists to avoid, named directly in
// the ADR: a member who paid March, then had a wrongly-entered June payment
// reversed, must NOT read as paid in advance through June. Under the
// rejected netting design, June would still be the chronological MAX
// dues_period touched by a kind='dues' row for this member - reversing it
// by giving the reversal a different kind entirely is what keeps June out
// of that MAX once it no longer counts as paid at all.
func TestReverseDuesPaymentReversedPeriodDoesNotReadAsPaidInAdvance(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()

	tierID := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, tierID, 25_000, "2026-01")
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Jane", tierID: &tierID})

	// Genuinely paid March.
	if _, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-03-10",
		Periods: []PeriodAmount{{DuesPeriod: "2026-03", Amount: 25_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments(March) = %v, want no error", err)
	}

	// Wrongly-entered June payment.
	junePosted, err := l.PostDuesPayments(ctx, PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		MemberID: memberID, OccurredOn: "2026-06-01",
		Periods: []PeriodAmount{{DuesPeriod: "2026-06", Amount: 25_000}},
	})
	if err != nil {
		t.Fatalf("PostDuesPayments(June) = %v, want no error", err)
	}

	// Before reversing June, March genuinely does read as paid in advance -
	// establishing the mechanism the reversal must then undo.
	statusBeforeReversal, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-03")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod(March) before June's reversal = %v, want no error", err)
	}
	gotBeforeReversal, ok := statusFor(t, statusBeforeReversal, memberID)
	if !ok || gotBeforeReversal.Status != DuesStatusPaidInAdvance {
		t.Fatalf("March status before June's reversal = %+v, ok=%v, want PaidInAdvance", gotBeforeReversal, ok)
	}

	// Reverse the wrongly-entered June payment.
	if _, err := l.ReverseDuesPayment(ctx, ReverseDuesPaymentParams{
		FundID: f.fundID, TransactionID: junePosted[0].ID, OccurredOn: "2026-08-01",
	}); err != nil {
		t.Fatalf("ReverseDuesPayment(June) = %v, want no error", err)
	}

	// March must now read as plain Paid - NOT paid in advance through a June
	// that no longer counts as paid at all.
	statusAfterReversal, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-03")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod(March) after June's reversal = %v, want no error", err)
	}
	gotAfterReversal, ok := statusFor(t, statusAfterReversal, memberID)
	if !ok {
		t.Fatalf("member %d missing from March's roster after June's reversal", memberID)
	}
	if gotAfterReversal.Status != DuesStatusPaid {
		t.Errorf("March status after June's reversal = %q, want %q (not PaidInAdvance - June no longer counts as paid)",
			gotAfterReversal.Status, DuesStatusPaid)
	}

	// And June itself must read as Unpaid, not merely "not advance".
	statusJune, err := l.DuesStatusForPeriod(ctx, f.fundID, "2026-06")
	if err != nil {
		t.Fatalf("DuesStatusForPeriod(June) after its own reversal = %v, want no error", err)
	}
	gotJune, ok := statusFor(t, statusJune, memberID)
	if !ok {
		t.Fatalf("member %d missing from June's roster after its own reversal", memberID)
	}
	if gotJune.Status != DuesStatusUnpaid {
		t.Errorf("June status after its own reversal = %q, want %q", gotJune.Status, DuesStatusUnpaid)
	}
}
