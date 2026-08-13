package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// OpenIncidentalParams is every argument OpenIncidental needs to open one
// envelope.
//
// There is deliberately no separate purpose-name field: Occasion becomes
// both the purpose row's name and the incidental row's occasion, because
// PRD 7.5's own language treats them as the same label ("create an
// incidental for an occasion").
type OpenIncidentalParams struct {
	FundID       int64
	Occasion     string        // non-empty; becomes purpose.name and incidental.occasion
	TargetAmount *money.Amount // nil = no target (the schema allows NULL); must be > 0 if set
	OpenedOn     string        // "YYYY-MM-DD", a real calendar date
}

// OpenIncidental writes one purpose row (kind='incidental') and the
// incidental row that is 1:1 with it, inside one withTx, and returns the
// created incidental.
//
// The two inserts happen together on purpose. Opening an envelope is a
// purpose row and an incidental row in two different tables; nothing in the
// schema ties their creation together, so two separate store.Queries calls
// - the shape M4 would otherwise have reached for - leave a window where a
// crash between them strands an orphan purpose: a tag that exists, appears
// in purpose lists, accepts transactions, and has no occasion, target or
// opened_on behind it (see #42's comment). That is the same atomicity
// argument ADR-027 already makes for CloseIncidentalAndRoll, applied to the
// other end of the same lifecycle.
func (l *Ledger) OpenIncidental(ctx context.Context, p OpenIncidentalParams) (store.Incidental, error) {
	if strings.TrimSpace(p.Occasion) == "" {
		return store.Incidental{}, fmt.Errorf("%w: occasion must not be empty", ErrInvalidArgument)
	}
	if p.TargetAmount != nil && *p.TargetAmount <= 0 {
		return store.Incidental{}, fmt.Errorf("%w: target_amount must be positive when set, got %d", ErrInvalidArgument, p.TargetAmount.Int64())
	}
	if err := validateOccurredOn(p.OpenedOn); err != nil {
		return store.Incidental{}, err
	}

	var created store.Incidental
	err := l.withTx(ctx, func(q store.Querier) error {
		now := time.Now().Unix()

		purpose, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
			FundID: p.FundID, Kind: "incidental", Name: p.Occasion, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating purpose: %w", err)
		}

		var targetAmount *int64
		if p.TargetAmount != nil {
			v := p.TargetAmount.Int64()
			targetAmount = &v
		}

		created, err = q.CreateIncidental(ctx, store.CreateIncidentalParams{
			PurposeID: purpose.ID, Occasion: p.Occasion, TargetAmount: targetAmount,
			OpenedOn: p.OpenedOn, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating incidental: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.Incidental{}, fmt.Errorf("opening incidental: %w", err)
	}
	return created, nil
}

// CloseIncidentalAndRollParams is every argument CloseIncidentalAndRoll
// needs to close one envelope and roll its leftover into the fund's main
// purpose.
type CloseIncidentalAndRollParams struct {
	FundID    int64
	PurposeID int64 // the incidental purpose closing

	// AccountID is the account the reclass_purpose pair posts through on
	// both legs. Its choice is immaterial to correctness - a same-account
	// pair always nets to zero on that account regardless of which
	// account the original contributions actually came in through
	// (ADR-027) - so it is a plain parameter with no derivation behind
	// it.
	AccountID int64

	ClosedOn string // "YYYY-MM-DD", a real calendar date
}

// CloseIncidentalAndRoll closes one envelope and, if it collected more than
// it disbursed, rolls the leftover into the fund's main purpose - one call,
// one withTx, per ADR-027.
//
// The leftover is IncidentalTotals' collected minus disbursed
// (money.Amount.Sub, checked for overflow). Three outcomes:
//
//   - Leftover > 0: postTransferPairTx posts a reclass_purpose pair - same
//     AccountID on both legs, from the incidental purpose to the fund's
//     main purpose - and closed_on is set in the same transaction. Returns
//     the rolled amount.
//   - Leftover == 0, or negative (the envelope disbursed more than it
//     collected): closes the envelope and posts nothing. Neither is an
//     error - an error inside withTx would roll the close back, and
//     nothing asks for an over-disbursed envelope to stay open. Returns 0,
//     nil.
//   - Already closed (closed_on already set): returns
//     ErrIncidentalAlreadyClosed and posts nothing. A second roll would
//     move money that already moved.
func (l *Ledger) CloseIncidentalAndRoll(ctx context.Context, p CloseIncidentalAndRollParams) (money.Amount, error) {
	if err := validateOccurredOn(p.ClosedOn); err != nil {
		return 0, err
	}

	var rolled money.Amount
	err := l.withTx(ctx, func(q store.Querier) error {
		envelope, err := q.GetIncidental(ctx, p.PurposeID)
		if err != nil {
			return fmt.Errorf("fetching incidental: %w", err)
		}
		if envelope.ClosedOn != nil {
			return ErrIncidentalAlreadyClosed
		}

		totals, err := q.IncidentalTotals(ctx, store.IncidentalTotalsParams{
			FundID: p.FundID, PurposeID: p.PurposeID,
		})
		if err != nil {
			return fmt.Errorf("computing incidental totals: %w", err)
		}

		leftover, err := money.FromDB(totals.CollectedAmount).Sub(money.FromDB(totals.DisbursedAmount))
		if err != nil {
			return fmt.Errorf("computing incidental leftover: %w", err)
		}

		if leftover > 0 {
			mainID, err := mainPurposeID(ctx, q, p.FundID)
			if err != nil {
				return err
			}

			from := leg{AccountID: p.AccountID, PurposeID: p.PurposeID}
			to := leg{AccountID: p.AccountID, PurposeID: mainID}
			if _, err := l.postTransferPairTx(ctx, q, p.FundID, "reclass_purpose", from, to, leftover, p.ClosedOn); err != nil {
				return fmt.Errorf("rolling incidental leftover: %w", err)
			}
			rolled = leftover
		}

		closedOn := p.ClosedOn
		if _, err := q.CloseIncidental(ctx, store.CloseIncidentalParams{
			ClosedOn: &closedOn, PurposeID: p.PurposeID,
		}); err != nil {
			return fmt.Errorf("closing incidental: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return rolled, nil
}

// mainPurposeID finds the fund's one kind='main' purpose. purpose_single_main
// guarantees exactly one exists per fund, so ListPurposesByFund - already
// scoped by fund_id - is enough; no dedicated query is added for a lookup
// this small.
func mainPurposeID(ctx context.Context, q store.Querier, fundID int64) (int64, error) {
	purposes, err := q.ListPurposesByFund(ctx, fundID)
	if err != nil {
		return 0, fmt.Errorf("listing purposes: %w", err)
	}
	for _, p := range purposes {
		if p.Kind == "main" {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("fund %d has no main purpose", fundID)
}
