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
// date; dues are monthly.
const duesPeriodLayout = "2006-01"

// PeriodAmount is one period within a multi-period dues payment: the period
// paid and the amount toward it. The kind='dues' CHECK requires exactly one
// dues_period per row (ADR-024), and paying several months at once is the
// treasurer's real workflow (PRD section 7.3), so Periods is a slice on
// PostDuesPaymentsParams and each entry becomes its own "transaction" row,
// never flattened into a total.
type PeriodAmount struct {
	DuesPeriod string       // "YYYY-MM", a real calendar month
	Amount     money.Amount // must be > 0
}

// PostDuesPaymentsParams is every argument PostDuesPayments needs to post one
// member's payment across one or more periods, in one sitting, on the same
// account, dated and noted alike; Periods is the only part that varies per
// row.
//
// There is no PurposeID (#473): dues are the routine fund's income and always
// land on Kas Utama, as SettleReimbursementParams carries none because the
// claim owns it.
type PostDuesPaymentsParams struct {
	FundID     int64
	AccountID  int64
	MemberID   int64
	OccurredOn string // "YYYY-MM-DD", a real calendar date, shared by every row
	Note       *string
	Periods    []PeriodAmount

	// Now is the clock the date's and the periods' ceilings are read from, in the
	// fund's zone (ADR-041, #632). The zero value means time.Now().
	Now time.Time
}

// PostDuesPayments writes one kind='dues' entry per element of Periods -
// direction='in', with member_id and dues_period set, as the schema's CHECK
// requires - all inside a single database transaction.
//
// Every period is validated before anything is written, then every row is
// inserted inside one withTx: a failure on period N leaves zero rows, not
// N-1. (A singular PostDuesPayment called once per period by the handler used
// to leave posted rows standing when a later period failed, #96; two ways to
// post one dues row is also the footgun ADR-027 argues against, so a
// single-period payment is a one-element Periods slice.)
//
// Argument-shape failures - an empty Periods, a non-positive amount, a
// malformed or calendar-invalid occurred_on, a malformed or calendar-invalid
// dues_period on any period - are rejected before any row is written
// (ADR-027), and so is a real date outside the plausible window (#632): an
// occurred_on after today in the fund's zone, a dues_period past the current
// month plus duesMonthsAhead, either before the floor (ErrDateOutOfRange). An empty Periods is ErrInvalidArgument here as the handler's 400:
// "post a payment with nothing to post" is a caller mistake, not a silent
// no-op answering 201.
//
// A retired AccountID (inactive_on set) is ErrAccountInactive, checked once
// for the whole batch in the transaction before any row is written (#444).
// Every row's purpose is Kas Utama, looked up once in the same transaction:
// never a closed envelope, never a titipan (#473).
func (l *Ledger) PostDuesPayments(ctx context.Context, p PostDuesPaymentsParams) ([]store.Transaction, error) {
	if len(p.Periods) == 0 {
		return nil, fmt.Errorf("%w: periods must not be empty", ErrInvalidArgument)
	}
	window, err := l.windowFor(ctx, l.q, p.FundID, p.Now)
	if err != nil {
		return nil, err
	}
	if err := validateOccurredOn("occurred_on", p.OccurredOn, window.entry()); err != nil {
		return nil, err
	}
	for _, period := range p.Periods {
		if period.Amount <= 0 {
			return nil, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidArgument, period.Amount.Int64())
		}
		if err := CheckAmountCeiling("amount", period.Amount); err != nil {
			return nil, err
		}
		if err := validateDuesPeriod("dues_period", period.DuesPeriod, window.dues()); err != nil {
			return nil, err
		}
	}

	posted := make([]store.Transaction, 0, len(p.Periods))
	err = l.withTx(ctx, func(q store.Querier) error {
		if err := refuseInactiveAccount(ctx, q, p.FundID, p.AccountID); err != nil {
			return err
		}
		mainID, err := mainPurposeID(ctx, q, p.FundID)
		if err != nil {
			return err
		}
		for _, period := range p.Periods {
			row, err := l.postDuesPaymentTx(ctx, q, p, mainID, period)
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

// postDuesPaymentTx inserts one period's "transaction" row using the caller's
// open store.Querier (ADR-027's ...Tx composition rule), with no transaction
// management or validation: PostDuesPayments does both once per batch.
func (l *Ledger) postDuesPaymentTx(ctx context.Context, q store.Querier, p PostDuesPaymentsParams, purposeID int64, period PeriodAmount) (store.Transaction, error) {
	return q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: p.FundID, AccountID: p.AccountID, PurposeID: purposeID,
		Direction: "in", Amount: period.Amount.Int64(), OccurredOn: p.OccurredOn,
		Kind:       "dues",
		MemberID:   &p.MemberID,
		DuesPeriod: &period.DuesPeriod,
		Note:       normalizeNote(p.Note),
		CreatedAt:  time.Now().Unix(),
	})
}

// ReverseDuesPaymentParams is every argument ReverseDuesPayment needs to
// reverse one previously-posted dues payment.
//
// There is no AccountID, PurposeID, Amount, MemberID or DuesPeriod field: all
// five are copied from the original row, never re-typed, as ADR-027 does for
// SettleReimbursementParams. A reversal naming a different amount or member
// would not reverse that payment (ADR-029).
type ReverseDuesPaymentParams struct {
	FundID        int64
	TransactionID int64  // the kind='dues' row, or a named contribution, being reversed (ADR-034)
	OccurredOn    string // "YYYY-MM-DD", the reversal's own date - not the original payment's
	Note          *string

	// Now is the clock the date's ceiling is read from, in the fund's zone
	// (ADR-041, #632). The zero value means time.Now().
	Now time.Time
}

// ReverseDuesPayment posts one kind='adjustment', direction='out' row that
// reverses a previously-posted kind='dues' payment or a named contribution
// (ADR-029, widened by ADR-034), carrying a new reverses_transaction_id
// naming the row it reverses. It returns the posted reversal row.
//
// account_id, purpose_id, amount, member_id and dues_period are copied from
// the original row inside the same withTx (see ReverseDuesPaymentParams).
// Only occurred_on and note are the caller's: the date the correction is
// made, and why. Copying dues_period unchanged makes a dues reversal carry
// its period and a contribution reversal carry none (a contribution's
// dues_period is NULL), which the schema's BEFORE INSERT trigger verifies
// independently.
//
// The original row is fetched fund-scoped (GetTransactionForFund: WHERE
// fund_id = ? AND id = ?), so ErrDuesPaymentNotFound answers for another
// fund's transaction id as for a missing one. The composite FK on
// reverses_transaction_id backs this at the schema level, but the fund-scoped
// fetch stops the method constructing the cross-fund insert at all.
//
// Four named errors, each a pre-check ahead of a guarantee the schema already
// enforces, giving a clean error instead of a raw constraint string
// (ADR-027's ErrReimbursementAlreadySettled shape):
//
//   - ErrDuesPaymentNotFound: no row with this id exists in this fund.
//   - ErrNotADuesPayment: the row is neither kind='dues' nor a named
//     contribution (kind='normal' with member_id set). That also rules out
//     reversing a reversal, which is kind='adjustment'.
//   - ErrDuesPaymentAlreadyReversed: GetDuesPaymentReversal finds a reversal
//     for this payment; the dues_payment_reversed_once partial unique index
//     is the guarantee, at most once ever.
//   - ErrIncidentalClosed: the original row's purpose is a closed incidental
//     (ADR-031). The reversal posts to that purpose, so the envelope must be
//     reopened first, as PostTransaction's guard requires. A dues payment is
//     always on 'main' (#473), so this applies only to a named contribution.
func (l *Ledger) ReverseDuesPayment(ctx context.Context, p ReverseDuesPaymentParams) (store.Transaction, error) {
	window, err := l.windowFor(ctx, l.q, p.FundID, p.Now)
	if err != nil {
		return store.Transaction{}, err
	}
	if err := validateOccurredOn("occurred_on", p.OccurredOn, window.entry()); err != nil {
		return store.Transaction{}, err
	}

	var reversal store.Transaction
	err = l.withTx(ctx, func(q store.Querier) error {
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

		isNamedContribution := original.Kind == "normal" && original.MemberID != nil
		if original.Kind != "dues" && !isNamedContribution {
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

		closedOn, err := q.IncidentalClosedOnForPurpose(ctx, original.PurposeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking incidental closed state: %w", err)
		}
		if err == nil && closedOn != nil {
			return ErrIncidentalClosed
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
			Note:                  normalizeNote(p.Note),
			CreatedAt:             time.Now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("reversing dues payment: %w", err)
	}
	return reversal, nil
}

// DeleteDuesTier removes a tier and the rates that priced it, in one
// transaction (#232). Only a tier no member was EVER in can go (ADR-040).
//
// Not money, but it is the only place with a transaction, and the two
// statements must share one. A tier's rates have an FK onto it, so the tier
// cannot be deleted first; and if a member_tier row then refuses the tier
// through its composite (fund_id, tier_id) FK, the rollback takes the rate
// deletion with it. Split across two calls, a refused tier would leave its
// rates gone.
//
// "Ever": a member_tier row is never deleted while its member exists, so a tier
// any row names - a superseded one included - is refused, and a tier a member
// was put into by a slip, since corrected, can no longer be deleted (rename it).
//
// No pre-check for members: the foreign key is the check, and a COUNT(*)
// first would only race it (as DeleteAccount's handler documents). The
// caller maps the constraint error to 409.
//
// fundID scopes the delete (GetDuesTierForFund, #188): an id names a row, it
// does not prove the caller may touch it.
func (l *Ledger) DeleteDuesTier(ctx context.Context, fundID, tierID int64) error {
	return l.withTx(ctx, func(q store.Querier) error {
		// Resolve the tier within the fund first: DeleteDuesRatesByTier is keyed by
		// tier id alone, so without this a foreign fund's tier id would delete that
		// fund's rate trail and commit before the fund-scoped tier delete ran. An id
		// naming no tier of this fund answers sql.ErrNoRows (wrapped): not found.
		if _, err := q.GetDuesTierForFund(ctx, store.GetDuesTierForFundParams{ID: tierID, FundID: fundID}); err != nil {
			return fmt.Errorf("fetching dues tier: %w", err)
		}
		if err := q.DeleteDuesRatesByTier(ctx, tierID); err != nil {
			return fmt.Errorf("deleting the tier's rates: %w", err)
		}
		if err := q.DeleteDuesTier(ctx, store.DeleteDuesTierParams{ID: tierID, FundID: fundID}); err != nil {
			return fmt.Errorf("deleting the tier: %w", err)
		}
		return nil
	})
}
