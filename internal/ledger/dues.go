package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// duesPeriodLayout is the schema's period shape (ADR-024): "YYYY-MM", not a
// calendar date - dues are monthly, and a period is not a date.
const duesPeriodLayout = "2006-01"

// PostDuesPaymentParams is every argument PostDuesPayment needs to post one
// member's payment for one period.
//
// There is deliberately no way to pay several periods in one call. The
// schema's kind='dues' CHECK requires exactly one dues_period per row
// (ADR-024), and that is also the treasurer's real workflow - paying three
// months at once is three payments, not one row that means three things.
// Callers who need to record several months at once call this once per
// period.
type PostDuesPaymentParams struct {
	FundID     int64
	AccountID  int64
	PurposeID  int64
	MemberID   int64
	DuesPeriod string       // "YYYY-MM", a real calendar month
	Amount     money.Amount // must be > 0
	OccurredOn string       // "YYYY-MM-DD", a real calendar date
	Note       *string
}

// PostDuesPayment writes one kind='dues' entry: direction='in', with
// member_id and dues_period set, exactly as the schema's CHECK requires.
//
// Argument-shape failures - amount <= 0, a malformed or calendar-invalid
// occurred_on, a malformed or calendar-invalid dues_period - are rejected
// before the write reaches the schema's CHECK constraints (ADR-027).
func (l *Ledger) PostDuesPayment(ctx context.Context, p PostDuesPaymentParams) (store.Transaction, error) {
	if p.Amount <= 0 {
		return store.Transaction{}, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidArgument, p.Amount.Int64())
	}
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return store.Transaction{}, err
	}
	if err := validateDuesPeriod(p.DuesPeriod); err != nil {
		return store.Transaction{}, err
	}

	var posted store.Transaction
	err := l.withTx(ctx, func(q store.Querier) error {
		var err error
		posted, err = q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: p.FundID, AccountID: p.AccountID, PurposeID: p.PurposeID,
			Direction: "in", Amount: p.Amount.Int64(), OccurredOn: p.OccurredOn,
			Kind:       "dues",
			MemberID:   &p.MemberID,
			DuesPeriod: &p.DuesPeriod,
			Note:       p.Note,
			CreatedAt:  time.Now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("posting dues payment: %w", err)
	}
	return posted, nil
}

// validateDuesPeriod rejects anything that is not a real "YYYY-MM" calendar
// month, mirroring validateOccurredOn's approach: time.Parse with this
// exact, strict layout already refuses an out-of-range month - "2026-13"
// fails to parse - and the round-trip format comparison catches a
// short-width value like "2026-1" that Parse would otherwise accept loosely.
func validateDuesPeriod(s string) error {
	t, err := time.Parse(duesPeriodLayout, s)
	if err != nil {
		return fmt.Errorf("%w: dues_period %q is not a valid \"YYYY-MM\" period: %v", ErrInvalidArgument, s, err)
	}
	if t.Format(duesPeriodLayout) != s {
		return fmt.Errorf("%w: dues_period %q is not a valid \"YYYY-MM\" period", ErrInvalidArgument, s)
	}
	return nil
}
