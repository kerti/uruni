package ledger

import (
	"context"
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

	// Idempotency, when non-nil, makes the payment safe to repeat: the same key
	// with the same request answers the first result, every period's row, and
	// posts nothing (ADR-043). nil is an unkeyed post.
	Idempotency *Idempotency
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
//
// With p.Idempotency set (ADR-043), a repeat of the same request returns the
// first call's rows as one result and posts none; argument shape is still
// checked first, and the account check inside the transaction does not run on a
// replay.
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

	posted, err := withIdempotency(ctx, l, p.Idempotency, func(q store.Querier) ([]store.Transaction, error) {
		if err := refuseInactiveAccount(ctx, q, p.FundID, p.AccountID); err != nil {
			return nil, err
		}
		mainID, err := mainPurposeID(ctx, q, p.FundID)
		if err != nil {
			return nil, err
		}
		rows := make([]store.Transaction, 0, len(p.Periods))
		for _, period := range p.Periods {
			row, err := l.postDuesPaymentTx(ctx, q, p, mainID, period)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		return rows, nil
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
