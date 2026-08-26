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

// PeriodAmount is one period within a multi-period dues payment: the period
// paid and the amount paid toward it. The schema's kind='dues' CHECK requires
// exactly one dues_period per row (ADR-024), and paying several months at
// once is the treasurer's real workflow (PRD §7.3) - not one row that means
// three things - so Periods is a slice on PostDuesPaymentsParams and every
// entry becomes its own "transaction" row, never flattened into a total.
type PeriodAmount struct {
	DuesPeriod string       // "YYYY-MM", a real calendar month
	Amount     money.Amount // must be > 0
}

// PostDuesPaymentsParams is every argument PostDuesPayments needs to post one
// member's payment across one or more periods, in one sitting, on the same
// account and purpose, dated and noted the same way across all of them -
// Periods is the only part that varies per row.
type PostDuesPaymentsParams struct {
	FundID     int64
	AccountID  int64
	PurposeID  int64
	MemberID   int64
	OccurredOn string // "YYYY-MM-DD", a real calendar date, shared by every row
	Note       *string
	Periods    []PeriodAmount
}

// PostDuesPayments writes one kind='dues' entry per element of Periods -
// direction='in', with member_id and dues_period set, exactly as the
// schema's CHECK requires - all inside a single database transaction.
//
// Every period is validated before anything is written, and then every row
// is inserted inside one withTx: a failure on period N leaves zero rows
// written, not N-1. This is the fix for the defect this type replaces
// (#96) - the previous shape, a singular PostDuesPayment called once per
// period by the HTTP handler, left every already-posted row standing when a
// later period failed, because each call owned and committed its own
// transaction independently. Two ways to post one dues row was also the
// footgun ADR-027 argues against, so there is no singular form left beside
// this one; a single-period payment is a one-element Periods slice.
//
// Argument-shape failures - an empty Periods, a non-positive amount, a
// malformed or calendar-invalid occurred_on, a malformed or calendar-invalid
// dues_period on any period - are rejected before anything reaches the
// write, and before any row in the batch is written (ADR-027). An empty
// Periods is ErrInvalidArgument here, the same way the HTTP handler treats
// it as a 400 before ever calling this method: both layers agree that
// "post a payment with nothing to post" is a caller mistake, not a silent
// no-op that would answer 201 having written nothing.
func (l *Ledger) PostDuesPayments(ctx context.Context, p PostDuesPaymentsParams) ([]store.Transaction, error) {
	if len(p.Periods) == 0 {
		return nil, fmt.Errorf("%w: periods must not be empty", ErrInvalidArgument)
	}
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return nil, err
	}
	for _, period := range p.Periods {
		if period.Amount <= 0 {
			return nil, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidArgument, period.Amount.Int64())
		}
		if err := validateDuesPeriod(period.DuesPeriod); err != nil {
			return nil, err
		}
	}

	posted := make([]store.Transaction, 0, len(p.Periods))
	err := l.withTx(ctx, func(q store.Querier) error {
		for _, period := range p.Periods {
			row, err := l.postDuesPaymentTx(ctx, q, p, period)
			if err != nil {
				return err
			}
			posted = append(posted, row)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("posting dues payments: %w", err)
	}
	return posted, nil
}

// postDuesPaymentTx inserts one period's "transaction" row using the
// caller's already-open store.Querier (ADR-027's …Tx composition rule),
// doing no transaction management and no validation of its own - both are
// PostDuesPayments' job, done once for the whole batch before this is ever
// called.
func (l *Ledger) postDuesPaymentTx(ctx context.Context, q store.Querier, p PostDuesPaymentsParams, period PeriodAmount) (store.Transaction, error) {
	return q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: p.FundID, AccountID: p.AccountID, PurposeID: p.PurposeID,
		Direction: "in", Amount: period.Amount.Int64(), OccurredOn: p.OccurredOn,
		Kind:       "dues",
		MemberID:   &p.MemberID,
		DuesPeriod: &period.DuesPeriod,
		Note:       p.Note,
		CreatedAt:  time.Now().Unix(),
	})
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
