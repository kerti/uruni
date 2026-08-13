package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// leg identifies one side of a transfer pair: the account and purpose a
// transaction posts against. It deliberately carries no direction -
// postTransferPair derives that itself (out at from, in at to), so a caller
// cannot construct a pair with both legs pointing the same way, which would
// not net to zero.
type leg struct {
	AccountID int64
	PurposeID int64
}

// legsIdentical reports whether from and to name the same account and the
// same purpose - an account-to-itself (or purpose-to-itself) transfer that
// would satisfy every schema CHECK while moving nothing and meaning nothing.
func legsIdentical(from, to leg) bool {
	return from.AccountID == to.AccountID && from.PurposeID == to.PurposeID
}

// postTransferPair is the one primitive behind every value-neutral movement
// (ADR-024, ADR-027): insert one transfer row, then two "transaction" rows
// referencing it, equal amount, opposite direction, all inside one withTx.
// The fund's total is unchanged by construction - one amount, two directions
// - because nothing moved, only where the money sits (between_accounts) or
// what it is for (reclass_purpose).
//
// kind selects which of the two the transfer row records; the two rows this
// function posts are identical either way, since the "same purpose" or "same
// account" constraint each caller upholds lives in the leg values it passes,
// not in this function's logic. This is deliberate: postTransferPair does
// not know or check which fields its two callers hold equal, so
// RollIncidentalLeftover (#42) needs no second code path, only different
// legs and kind='reclass_purpose'.
//
// No argument-shape validation happens here (ADR-027): that is each exported
// caller's job, because the message a caller wants ("from_account_id and
// to_account_id must differ") reads differently depending on which two
// fields the caller holds fixed. This function trusts its caller completely.
func (l *Ledger) postTransferPair(ctx context.Context, fundID int64, kind string, from, to leg, amount money.Amount, occurredOn string) (store.Transfer, error) {
	var transfer store.Transfer
	err := l.withTx(ctx, func(q store.Querier) error {
		now := time.Now().Unix()

		var err error
		transfer, err = q.CreateTransfer(ctx, store.CreateTransferParams{
			FundID: fundID, Kind: kind, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating transfer: %w", err)
		}

		for _, p := range [...]struct {
			leg       leg
			direction string
		}{{from, "out"}, {to, "in"}} {
			if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
				FundID: fundID, AccountID: p.leg.AccountID, PurposeID: p.leg.PurposeID,
				Direction: p.direction, Amount: amount.Int64(), OccurredOn: occurredOn,
				Kind: "transfer", TransferID: &transfer.ID, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("posting transfer leg (%s): %w", p.direction, err)
			}
		}
		return nil
	})
	if err != nil {
		return store.Transfer{}, err
	}
	return transfer, nil
}

// PostTransferBetweenAccountsParams is every argument
// PostTransferBetweenAccounts needs: the same purpose, moving between two
// different accounts - depositing the wallet's cash at the bank is the
// PRD's example.
type PostTransferBetweenAccountsParams struct {
	FundID        int64
	PurposeID     int64
	FromAccountID int64
	ToAccountID   int64
	Amount        money.Amount // must be > 0
	OccurredOn    string       // "YYYY-MM-DD", a real calendar date
}

// PostTransferBetweenAccounts moves money from one account to another within
// the same purpose - cash into the bank, or back out. The fund's total is
// unchanged by construction; only AccountBalance moves, on both sides, by
// exactly the amount, in opposite directions.
func (l *Ledger) PostTransferBetweenAccounts(ctx context.Context, p PostTransferBetweenAccountsParams) (store.Transfer, error) {
	if p.Amount <= 0 {
		return store.Transfer{}, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidArgument, p.Amount.Int64())
	}
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return store.Transfer{}, err
	}

	from := leg{AccountID: p.FromAccountID, PurposeID: p.PurposeID}
	to := leg{AccountID: p.ToAccountID, PurposeID: p.PurposeID}
	if legsIdentical(from, to) {
		return store.Transfer{}, fmt.Errorf("%w: from_account_id and to_account_id must differ, got %d for both", ErrInvalidArgument, p.FromAccountID)
	}

	transfer, err := l.postTransferPair(ctx, p.FundID, "between_accounts", from, to, p.Amount, p.OccurredOn)
	if err != nil {
		return store.Transfer{}, fmt.Errorf("posting transfer between accounts: %w", err)
	}
	return transfer, nil
}
