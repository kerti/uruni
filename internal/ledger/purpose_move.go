package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// PostPurposeMoveParams is every argument PostPurposeMove needs to move an
// amount from one purpose to another (ADR-036, #383).
type PostPurposeMoveParams struct {
	FundID        int64
	FromPurposeID int64
	ToPurposeID   int64

	// AccountID is the one location both legs post on. Its balance does not
	// move - one 'out' and one 'in' of the same amount net to zero there -
	// but the row names it, and the choice is the treasurer's (ADR-036).
	AccountID  int64
	Amount     money.Amount // must be > 0
	OccurredOn string       // "YYYY-MM-DD", a real calendar date

	// Note is written to both legs, or to neither; see normalizeNote.
	Note *string
}

// PostPurposeMove moves Amount of what the fund holds from one purpose to
// another: a reclass_purpose pair with reason 'allocation', both legs on
// AccountID. Nothing leaves the fund or any account - the fund balance and
// every account balance are unchanged - and the two purpose balances shift
// by exactly Amount, in opposite directions (ADR-036).
//
// It is the third caller of postTransferPairTx, after CloseIncidentalAndRoll
// and PostPurposeCorrection, and adds no second write path (ADR-027).
// postTransferPairTx checks nothing, so every rule lives here, each its own
// named error:
//
//   - Amount <= 0 or a malformed date: ErrInvalidArgument.
//   - FromPurposeID equal to ToPurposeID: ErrPurposeMoveSamePurpose.
//   - A purpose that is not this fund's (including an id that names
//     nothing): ErrPurposeMoveUnknownPurpose.
//   - Titipan (pass-through) on either side: ErrPurposeMovePassThrough. That
//     money belongs to the parent body and leaves only by being forwarded.
//   - A closed envelope on either side: ErrPurposeMoveClosed - reopen it
//     first, the rule ADR-031 applies to postings.
//   - An account that is not this fund's: ErrPurposeMoveUnknownAccount; a
//     retired one: ErrPurposeMoveAccountInactive.
//   - Amount above the source purpose's balance: ErrPurposeMoveInsufficient.
//     The balance is the ledger sum at posting time, read inside the same
//     transaction as the write (ADR-004's single connection makes that
//     read-then-write atomic); it is not the balance as of OccurredOn, so a
//     backdated move can leave a purpose briefly negative on an earlier day,
//     the same tolerance every backdated posting has (ADR-036).
func (l *Ledger) PostPurposeMove(ctx context.Context, p PostPurposeMoveParams) (store.Transfer, error) {
	if p.Amount <= 0 {
		return store.Transfer{}, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidArgument, p.Amount.Int64())
	}
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return store.Transfer{}, err
	}
	if p.FromPurposeID == p.ToPurposeID {
		return store.Transfer{}, ErrPurposeMoveSamePurpose
	}

	var created store.Transfer
	err := l.withTx(ctx, func(q store.Querier) error {
		for _, id := range [...]int64{p.FromPurposeID, p.ToPurposeID} {
			purpose, err := q.GetPurposeForFund(ctx, store.GetPurposeForFundParams{ID: id, FundID: p.FundID})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrPurposeMoveUnknownPurpose
				}
				return fmt.Errorf("fetching purpose %d: %w", id, err)
			}
			if purpose.Kind == "pass_through" {
				return ErrPurposeMovePassThrough
			}
			if err := refuseClosedIncidental(ctx, q, id, ErrPurposeMoveClosed); err != nil {
				return err
			}
		}

		account, err := q.GetAccountForFund(ctx, store.GetAccountForFundParams{ID: p.AccountID, FundID: p.FundID})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPurposeMoveUnknownAccount
			}
			return fmt.Errorf("fetching account %d: %w", p.AccountID, err)
		}
		if account.InactiveOn != nil {
			return ErrPurposeMoveAccountInactive
		}

		balance, err := q.PurposeBalance(ctx, store.PurposeBalanceParams{FundID: p.FundID, PurposeID: p.FromPurposeID})
		if err != nil {
			return fmt.Errorf("source purpose balance: %w", err)
		}
		if p.Amount.Int64() > balance {
			return ErrPurposeMoveInsufficient
		}

		reason := reasonAllocation
		from := leg{AccountID: p.AccountID, PurposeID: p.FromPurposeID}
		to := leg{AccountID: p.AccountID, PurposeID: p.ToPurposeID}
		created, err = l.postTransferPairTx(ctx, q, p.FundID, "reclass_purpose", from, to,
			p.Amount, p.OccurredOn, normalizeNote(p.Note), nil, &reason)
		if err != nil {
			return fmt.Errorf("posting purpose move: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.Transfer{}, err
	}
	return created, nil
}
