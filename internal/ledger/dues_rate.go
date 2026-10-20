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

// Rate writes (ADR-040). A tier's rate is one row per start month, and a mistake
// in it is corrected by a new row - the next `correction` number of the month,
// of which the highest stands - never an overwrite, so a past period that was
// repriced leaves the row that priced it behind. Because each write reads the
// month's latest correction before it inserts, rate writes live here and not in
// direct CRUD (ADR-027's amendment).
//
// The three verbs differ only in what they append:
//
//   - CreateDuesRate: a rate into a month that has none (correction 0), or into
//     a month whose standing row is a deletion (the next correction).
//   - CorrectDuesRate: a new amount for the row that stands (the next
//     correction, amount only; an equal amount writes nothing).
//   - DeleteDuesRate: a deleting correction (deleted = 1) that copies the amount
//     it withdraws, so the month holds no rate and its periods fall back to the
//     earlier month's rate, or to "not decided".
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

	// Now is the clock the start month's ceiling is read from, in the fund's zone
	// (ADR-041, #632). The zero value means time.Now().
	Now time.Time
}

// CreateDuesRate adds a rate for a tier from a month. It is a "change" in
// ADR-040's sense: it touches no period before the month and, backdated,
// reprices the months from there up to the next row. It answers
// ErrDuesRateMonthTaken when the month already has a rate; a month whose
// standing row is a deletion takes the next correction, so a deleted rate can
// be re-added.
//
// The start month is held to the plausible window (#632): from 2000-01 to the
// fund's current month plus rateMonthsAhead, inclusive. CorrectDuesRate and
// DeleteDuesRate restate a month that already stands, so they choose no date
// and check none.
func (l *Ledger) CreateDuesRate(ctx context.Context, p CreateDuesRateParams) (store.DuesRate, error) {
	if err := CheckAmountCeiling("amount", p.Amount); err != nil {
		return store.DuesRate{}, err
	}
	window, err := l.windowFor(ctx, l.q, p.FundID, p.Now)
	if err != nil {
		return store.DuesRate{}, err
	}
	if err := validateDuesPeriod("effective_from", p.EffectiveFrom, window.rate()); err != nil {
		return store.DuesRate{}, err
	}
	var out store.DuesRate
	err = l.withTx(ctx, func(q store.Querier) error {
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
			Correction: correction, Deleted: 0, CreatedAt: time.Now().Unix(),
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
// reprices every period the row covers (from its month to the next month that
// still has a rate), deliberately: a mistyped amount was never the truth, and
// the superseded row stays behind. There is no age limit and no payment guard.
//
// An amount equal to the standing one writes nothing and returns the standing
// row, so re-saving the form leaves no stray rows. The ceiling (#499) and the
// schema's amount CHECKs apply to a correction as to any rate.
//
// rateID must be the row that stands: a superseded row, or one that is itself a
// deleting correction, is refused with ErrDuesRateSuperseded (a stale page).
func (l *Ledger) CorrectDuesRate(ctx context.Context, fundID, rateID int64, amount money.Amount) (store.DuesRate, error) {
	if err := CheckAmountCeiling("amount", amount); err != nil {
		return store.DuesRate{}, err
	}
	var out store.DuesRate
	err := l.withTx(ctx, func(q store.Querier) error {
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
			Correction: standing.Correction + 1, Deleted: 0, CreatedAt: time.Now().Unix(),
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
// correction (deleted = 1) that copies its amount. The month then holds no rate:
// the periods it covered fall back to the earlier month's rate, or to "not
// decided" when there is none, and a later POST into the month appends the next
// correction. This is what makes a rate filed against the wrong month, or an
// erroneous first rate, removable; nothing is destroyed.
//
// rateID must be the row that stands, as for CorrectDuesRate.
func (l *Ledger) DeleteDuesRate(ctx context.Context, fundID, rateID int64) error {
	return l.withTx(ctx, func(q store.Querier) error {
		standing, err := standingRate(ctx, q, fundID, rateID)
		if err != nil {
			return err
		}
		if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
			TierID: standing.TierID, Amount: standing.Amount, EffectiveFrom: standing.EffectiveFrom,
			Correction: standing.Correction + 1, Deleted: 1, CreatedAt: time.Now().Unix(),
		}); err != nil {
			return fmt.Errorf("withdrawing the dues rate: %w", err)
		}
		return nil
	})
}

// standingRate fetches the rate rateID names within the fund and checks it is the
// row that stands for its month, as a rate: not superseded by a later correction
// and not itself a deletion. An id naming nothing in the fund answers
// sql.ErrNoRows (wrapped), a stale one ErrDuesRateSuperseded.
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
