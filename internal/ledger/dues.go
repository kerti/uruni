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
// There is no PurposeID: dues are the routine fund's income and always land on
// Kas Utama.
type PostDuesPaymentsParams struct {
	FundID     int64
	AccountID  int64
	MemberID   int64
	OccurredOn string // "YYYY-MM-DD", a real calendar date, shared by every row
	Note       *string
	Periods    []PeriodAmount

	// Now is the clock the date's and the periods' ceilings are read from (ADR-041).
	// Zero means time.Now().
	Now time.Time

	// Idempotency, when non-nil, makes the payment safe to repeat (ADR-043).
	Idempotency *Idempotency
}

// PostDuesPayments writes one kind='dues' entry per element of Periods -
// direction='in', with member_id and dues_period set, as the schema's CHECK
// requires - all inside a single database transaction.
//
// Every period is validated before anything is written, then every row is
// inserted inside one withTx: a failure on period N leaves zero rows, not N-1.
// A single-period payment is a one-element Periods slice (ADR-027).
//
// Argument-shape failures - an empty Periods (a caller mistake, not a silent
// no-op), a non-positive amount, a malformed or calendar-invalid occurred_on or
// dues_period - are rejected before any row is written (ADR-027), and so is a
// date outside the plausible window: an occurred_on after today in the fund's
// zone, a dues_period past the current month plus duesMonthsAhead, either
// before the floor (ErrDateOutOfRange).
//
// A retired AccountID is ErrAccountInactive, checked once for the batch inside
// the transaction. Every row's purpose is Kas Utama, looked up once there:
// never a closed envelope, never a titipan.
//
// With p.Idempotency set (ADR-043), a repeat returns the first call's rows and
// posts none; argument shape is still checked first, the account check is not
// re-run on a replay.
func (l *Ledger) PostDuesPayments(ctx context.Context, p PostDuesPaymentsParams) ([]store.Transaction, error) {
	if len(p.Periods) == 0 {
		return nil, invalidField("periods", "periods must not be empty")
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
			return nil, invalidField("periods.amount", "amount must be positive, got %d", period.Amount.Int64())
		}
		if err := CheckAmountCeiling("amount", period.Amount); err != nil {
			return nil, err
		}
		if err := validateDuesPeriod("dues_period", period.DuesPeriod, window.dues()); err != nil {
			return nil, err
		}
	}

	posted, err := withIdempotency(ctx, l, p.Idempotency, func(ctx context.Context, q store.Querier) ([]store.Transaction, error) {
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

// postDuesPaymentTx inserts one period's row on the caller's open querier
// (ADR-027's ...Tx rule); PostDuesPayments validates once per batch.
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
// transaction. Only a tier no member was EVER in can go (ADR-040).
//
// The two statements must share one transaction: the rates' FK means they go
// first, and if a member_tier row then refuses the tier, the rollback takes the
// rate deletion with it.
//
// "Ever": a member_tier row is never deleted while its member exists, so a tier
// any row names - a superseded one included - is refused, and a tier a member
// was put into by a slip, since corrected, can no longer be deleted (rename it).
//
// No pre-check for members: the foreign key is the check. The caller maps the
// constraint error to 409.
func (l *Ledger) DeleteDuesTier(ctx context.Context, fundID, tierID int64) error {
	return l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		// Resolve the tier within the fund first: DeleteDuesRatesByTier is keyed by
		// tier id alone, so without this a foreign fund's tier id would delete that
		// fund's rate trail before the fund-scoped tier delete ran.
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
