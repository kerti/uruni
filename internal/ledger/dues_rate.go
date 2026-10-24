package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// Rate writes (ADR-040). A tier's rate is one row per start month, and a mistake
// in it is corrected by a new row - the next `correction` number of the month,
// of which the highest stands - never an overwrite, so a past period that was
// repriced leaves the row that priced it behind. Because each write reads the
// month's latest correction before it inserts, rate writes live here and not in
// direct CRUD (ADR-027's amendment).
//
// Nothing posted moves. A correction changes what is OWED for the periods the
// month's row covers, never what was paid, so no balance and no reconciliation
// changes.

// CreateDuesRateParams is every argument CreateDuesRate needs.
type CreateDuesRateParams struct {
	FundID        int64
	TierID        int64
	Amount        money.Amount
	EffectiveFrom string // "YYYY-MM"; a malformed month is ErrInvalidArgument, one outside the window ErrDateOutOfRange
}

// CreateDuesRate adds a rate for a tier from a month: correction 0 into a month
// with none, the next correction into a month whose standing row is a deletion,
// otherwise ErrDuesRateMonthTaken. Backdated, it reprices the months from there
// up to the next row (ADR-040).
//
// The start month is held to the plausible window, up to the current month plus
// rateMonthsAhead. CorrectDuesRate and DeleteDuesRate restate a month that
// already stands, so they check no date.
func (l *Ledger) CreateDuesRate(ctx context.Context, p CreateDuesRateParams) (store.DuesRate, error) {
	if err := CheckAmountCeiling("amount", p.Amount); err != nil {
		return store.DuesRate{}, err
	}
	window, err := l.windowFor(ctx, l.q, p.FundID)
	if err != nil {
		return store.DuesRate{}, err
	}
	if err := validateDuesPeriod("effective_from", p.EffectiveFrom, window.rate()); err != nil {
		return store.DuesRate{}, err
	}
	var out store.DuesRate
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		// Fund scope: an id names a row, it does not prove the caller may touch it (#188).
		if _, err := q.GetDuesTierForFund(ctx, store.GetDuesTierForFundParams{ID: p.TierID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching dues tier: %w", err)
		}
		correction := int64(0)
		latest, err := q.LatestDuesRateOfMonth(ctx, store.LatestDuesRateOfMonthParams{TierID: p.TierID, EffectiveFrom: p.EffectiveFrom})
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("reading the month's latest correction: %w", err)
		case latest.Deleted == 1:
			correction = latest.Correction + 1
		default:
			return fmt.Errorf("%w: %s", ErrDuesRateMonthTaken, p.EffectiveFrom)
		}
		out, err = q.CreateDuesRate(ctx, store.CreateDuesRateParams{
			TierID: p.TierID, Amount: p.Amount.Int64(), EffectiveFrom: p.EffectiveFrom,
			Correction: correction, Deleted: 0, CreatedAt: l.now().Unix(),
		})
		if err != nil {
			return fmt.Errorf("writing the dues rate: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.DuesRate{}, err
	}
	return out, nil
}

// CorrectDuesRate states a new amount for the rate rateID names and returns the
// row that now stands. It appends the next correction of that rate's month and
// reprices every period the row covers, deliberately: a mistyped amount was never
// the truth, and the superseded row stays behind. There is no age limit and no
// payment guard.
//
// An amount equal to the standing one writes nothing, so re-saving the form
// leaves no stray rows. rateID must be the row that stands, else
// ErrDuesRateSuperseded (a stale page).
func (l *Ledger) CorrectDuesRate(ctx context.Context, fundID, rateID int64, amount money.Amount) (store.DuesRate, error) {
	if err := CheckAmountCeiling("amount", amount); err != nil {
		return store.DuesRate{}, err
	}
	var out store.DuesRate
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		standing, err := standingRate(ctx, q, fundID, rateID)
		if err != nil {
			return err
		}
		if money.FromDB(standing.Amount) == amount {
			out = standing
			return nil
		}
		out, err = q.CreateDuesRate(ctx, store.CreateDuesRateParams{
			TierID: standing.TierID, Amount: amount.Int64(), EffectiveFrom: standing.EffectiveFrom,
			Correction: standing.Correction + 1, Deleted: 0, CreatedAt: l.now().Unix(),
		})
		if err != nil {
			return fmt.Errorf("writing the corrected dues rate: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.DuesRate{}, err
	}
	return out, nil
}

// DeleteDuesRate withdraws the rate rateID names by appending a deleting
// correction (deleted = 1) that copies its amount. The periods it covered fall
// back to the earlier month's rate, or to "not decided"; nothing is destroyed.
// rateID must be the row that stands, as for CorrectDuesRate.
func (l *Ledger) DeleteDuesRate(ctx context.Context, fundID, rateID int64) error {
	return l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		standing, err := standingRate(ctx, q, fundID, rateID)
		if err != nil {
			return err
		}
		if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
			TierID: standing.TierID, Amount: standing.Amount, EffectiveFrom: standing.EffectiveFrom,
			Correction: standing.Correction + 1, Deleted: 1, CreatedAt: l.now().Unix(),
		}); err != nil {
			return fmt.Errorf("withdrawing the dues rate: %w", err)
		}
		return nil
	})
}

// standingRate fetches the rate rateID names within the fund and checks it is the
// row that stands for its month: not superseded and not a deletion. A stale id
// answers ErrDuesRateSuperseded.
func standingRate(ctx context.Context, q store.Querier, fundID, rateID int64) (store.DuesRate, error) {
	row, err := q.GetDuesRateForFund(ctx, store.GetDuesRateForFundParams{ID: rateID, FundID: fundID})
	if err != nil {
		return store.DuesRate{}, fmt.Errorf("fetching dues rate: %w", err)
	}
	latest, err := q.LatestDuesRateOfMonth(ctx, store.LatestDuesRateOfMonthParams{TierID: row.TierID, EffectiveFrom: row.EffectiveFrom})
	if err != nil {
		return store.DuesRate{}, fmt.Errorf("reading the month's latest correction: %w", err)
	}
	if latest.ID != row.ID || latest.Deleted == 1 {
		return store.DuesRate{}, fmt.Errorf("%w: rate %d", ErrDuesRateSuperseded, rateID)
	}
	return latest, nil
}
