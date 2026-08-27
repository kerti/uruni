package ledger

import (
	"context"
	"database/sql"
	"errors"
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

// ReverseDuesPaymentParams is every argument ReverseDuesPayment needs to
// reverse one previously-posted dues payment.
//
// There is deliberately no AccountID, PurposeID, Amount, MemberID or
// DuesPeriod field: all five are copied from the original row by
// ReverseDuesPayment itself, never re-typed by the caller - the same
// discipline ADR-027 already applies to SettleReimbursementParams, which
// carries no Amount or PurposeID for exactly this reason. A reversal that
// could name a different amount or member than the payment it reverses
// would not be a reversal of that payment (ADR-029).
type ReverseDuesPaymentParams struct {
	FundID        int64
	TransactionID int64  // the kind='dues' row being reversed
	OccurredOn    string // "YYYY-MM-DD", the reversal's own date - not the original payment's
	Note          *string
}

// ReverseDuesPayment posts one kind='adjustment', direction='out' row that
// reverses a previously-posted kind='dues' payment, carrying a new
// reverses_transaction_id naming the row it reverses (ADR-029). It returns
// the posted reversal row.
//
// account_id, purpose_id, amount, member_id and dues_period are copied from
// the original payment, inside the same withTx that posts the reversal -
// never re-typed by the caller (see ReverseDuesPaymentParams). Only
// occurred_on and note are the caller's to choose: the date the correction
// is actually made, and why.
//
// The original row is fetched fund-scoped (GetTransactionForFund: WHERE
// fund_id = ? AND id = ?), not by id alone. That is what makes
// ErrDuesPaymentNotFound the answer for a transaction id belonging to
// another fund, exactly as it is for one that does not exist at all - the
// composite FK on reverses_transaction_id backs the same guarantee at the
// schema level, but the fund-scoped fetch is what stops this method from
// ever constructing the cross-fund insert in the first place.
//
// Three named errors, each a pre-check ahead of a guarantee the schema
// already enforces on its own - here purely to give the caller a clean,
// named error instead of a raw constraint string (ADR-027's
// ErrReimbursementAlreadySettled shape):
//
//   - ErrDuesPaymentNotFound: no row with this id exists in this fund.
//   - ErrNotADuesPayment: the row exists but its Kind is not "dues" - which
//     also rules out reversing a reversal, since a reversal is itself
//     posted as kind='adjustment', never kind='dues'.
//   - ErrDuesPaymentAlreadyReversed: GetDuesPaymentReversal already finds a
//     reversal row for this payment - the dues_payment_reversed_once
//     partial unique index is the actual guarantee, at most once ever.
func (l *Ledger) ReverseDuesPayment(ctx context.Context, p ReverseDuesPaymentParams) (store.Transaction, error) {
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return store.Transaction{}, err
	}

	var reversal store.Transaction
	err := l.withTx(ctx, func(q store.Querier) error {
		original, err := q.GetTransactionForFund(ctx, store.GetTransactionForFundParams{
			FundID: p.FundID,
			ID:     p.TransactionID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrDuesPaymentNotFound
			}
			return fmt.Errorf("fetching transaction to reverse: %w", err)
		}

		if original.Kind != "dues" {
			return ErrNotADuesPayment
		}

		_, err = q.GetDuesPaymentReversal(ctx, store.GetDuesPaymentReversalParams{
			FundID:                p.FundID,
			ReversesTransactionID: &p.TransactionID,
		})
		if err == nil {
			return ErrDuesPaymentAlreadyReversed
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking for an existing reversal: %w", err)
		}

		reversal, err = q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID:                p.FundID,
			AccountID:             original.AccountID,
			PurposeID:             original.PurposeID,
			Direction:             "out",
			Amount:                original.Amount,
			OccurredOn:            p.OccurredOn,
			Kind:                  "adjustment",
			MemberID:              original.MemberID,
			DuesPeriod:            original.DuesPeriod,
			ReversesTransactionID: &p.TransactionID,
			Note:                  p.Note,
			CreatedAt:             time.Now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("reversing dues payment: %w", err)
	}
	return reversal, nil
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
