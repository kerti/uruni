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
// amount between purposes (ADR-036, #383).
type PostPurposeMoveParams struct {
	FundID        int64
	FromPurposeID int64
	ToPurposeID   int64

	// AccountID is the one location both legs post on. Its balance does not move
	// (one 'out' and one 'in' of the same amount net to zero) but the row names it,
	// and the choice is the treasurer's (ADR-036).
	AccountID  int64
	Amount     money.Amount // must be > 0
	OccurredOn string       // "YYYY-MM-DD", a real calendar date

	// Note is written to both legs, or to neither; see normalizeNote.
	Note *string

	// Idempotency, when non-nil, makes the move safe to repeat (ADR-043).
	Idempotency *Idempotency
}

// PostPurposeMove moves Amount of what the fund holds from one purpose to
// another: a reclass_purpose pair with reason 'allocation', both legs on
// AccountID. Nothing leaves the fund or any account, and the two purpose
// balances shift by exactly Amount, oppositely (ADR-036).
//
// It is the third caller of postTransferPairTx, after CloseIncidentalAndRoll
// and PostPurposeCorrection, with no second write path (ADR-027).
// postTransferPairTx checks nothing, so every rule lives here as a named
// error:
//
//   - Amount <= 0 or a malformed date: ErrInvalidArgument. A real date outside
//     the plausible window: ErrDateOutOfRange.
//   - FromPurposeID equal to ToPurposeID: ErrPurposeMoveSamePurpose.
//   - A purpose not this fund's (including an id naming nothing):
//     ErrPurposeMoveUnknownPurpose.
//   - Titipan (pass-through) on either side: ErrPurposeMovePassThrough. That
//     money belongs to the parent body and leaves only by being forwarded.
//   - A closed envelope on either side: ErrPurposeMoveClosed - reopen first,
//     the rule ADR-031 applies to postings.
//   - An account not this fund's: ErrPurposeMoveUnknownAccount; a retired one:
//     ErrPurposeMoveAccountInactive.
//   - Amount above the source purpose's balance: ErrPurposeMoveInsufficient.
//     The balance is the ledger sum at posting time, read in the write's
//     transaction (ADR-004's single connection makes read-then-write atomic),
//     not as of OccurredOn, so a backdated move can leave a purpose briefly
//     negative on an earlier day, as every backdated posting can (ADR-036).
//
// With p.Idempotency set (ADR-043), a repeat returns the first move; the checks
// inside the transaction (the source balance among them) do not run on a replay.
func (l *Ledger) PostPurposeMove(ctx context.Context, p PostPurposeMoveParams) (store.Transfer, error) {
	if p.Amount <= 0 {
		return store.Transfer{}, invalidField("amount", "amount must be positive, got %d", p.Amount.Int64())
	}
	if err := CheckAmountCeiling("amount", p.Amount); err != nil {
		return store.Transfer{}, err
	}
	window, err := l.windowFor(ctx, l.q, p.FundID)
	if err != nil {
		return store.Transfer{}, err
	}
	if err := validateOccurredOn("occurred_on", p.OccurredOn, window.entry()); err != nil {
		return store.Transfer{}, err
	}
	if p.FromPurposeID == p.ToPurposeID {
		return store.Transfer{}, ErrPurposeMoveSamePurpose
	}

	created, err := withIdempotency(ctx, l, p.Idempotency, func(ctx context.Context, q store.Querier) (store.Transfer, error) {
		for _, id := range [...]int64{p.FromPurposeID, p.ToPurposeID} {
			purpose, err := q.GetPurposeForFund(ctx, store.GetPurposeForFundParams{ID: id, FundID: p.FundID})
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return store.Transfer{}, ErrPurposeMoveUnknownPurpose
				}
				return store.Transfer{}, fmt.Errorf("fetching purpose %d: %w", id, err)
			}
			if purpose.Kind == string(PurposeKindPassThrough) {
				return store.Transfer{}, ErrPurposeMovePassThrough
			}
			if err := refuseClosedIncidental(ctx, q, id, ErrPurposeMoveClosed); err != nil {
				return store.Transfer{}, err
			}
		}

		account, err := q.GetAccountForFund(ctx, store.GetAccountForFundParams{ID: p.AccountID, FundID: p.FundID})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.Transfer{}, ErrPurposeMoveUnknownAccount
			}
			return store.Transfer{}, fmt.Errorf("fetching account %d: %w", p.AccountID, err)
		}
		if account.InactiveOn != nil {
			return store.Transfer{}, ErrPurposeMoveAccountInactive
		}

		balance, err := q.PurposeBalance(ctx, store.PurposeBalanceParams{FundID: p.FundID, PurposeID: p.FromPurposeID})
		if err != nil {
			return store.Transfer{}, fmt.Errorf("source purpose balance: %w", err)
		}
		if p.Amount.Int64() > balance {
			return store.Transfer{}, ErrPurposeMoveInsufficient
		}

		reason := reasonAllocation
		from := leg{AccountID: p.AccountID, PurposeID: p.FromPurposeID}
		to := leg{AccountID: p.AccountID, PurposeID: p.ToPurposeID}
		pair, err := l.postTransferPairTx(ctx, q, transferPair{
			FundID: p.FundID, Kind: TransferKindReclassPurpose, From: from, To: to,
			Amount: p.Amount, OccurredOn: p.OccurredOn, Note: normalizeNote(p.Note), Reason: &reason,
		})
		if err != nil {
			return store.Transfer{}, fmt.Errorf("posting purpose move: %w", err)
		}
		return pair, nil
	})
	if err != nil {
		return store.Transfer{}, err
	}
	return created, nil
}
