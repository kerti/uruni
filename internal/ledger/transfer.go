package ledger

import (
	"context"
	"fmt"
	"strings"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// leg identifies one side of a transfer pair: the account and purpose a
// transaction posts against. It carries no direction: postTransferPairTx
// derives it (out at from, in at to), so a caller cannot build a pair with
// both legs pointing the same way, which would not net to zero.
type leg struct {
	AccountID int64
	PurposeID int64
}

// legsIdentical reports whether from and to name the same account and purpose:
// a transfer to itself would satisfy every schema CHECK while moving nothing.
func legsIdentical(from, to leg) bool {
	return from.AccountID == to.AccountID && from.PurposeID == to.PurposeID
}

// normalizeNote collapses "no note" to one representation. Absence can arrive
// as nil or an empty or whitespace-only string (an untouched form field), and
// storing "" would render an empty note line in the transaction list.
func normalizeNote(note *string) *string {
	if note == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*note)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// postTransferPairTx is the one primitive behind every value-neutral movement
// (ADR-024, ADR-027): insert one transfer row, then two "transaction" rows
// referencing it, equal amount, opposite direction. The fund's total is
// unchanged by construction: only where the money sits (between_accounts) or
// what it is for (reclass_purpose) moves.
//
// kind selects which the transfer row records; the two rows posted are the
// same either way, since the "same purpose" or "same account" constraint each
// caller upholds lives in the legs it passes. This function does not check
// which fields its callers hold equal, so CloseIncidentalAndRoll (#42) needs
// no second code path, only different legs and kind='reclass_purpose'.
//
// correctsTransactionID is nil except for a purpose correction (ADR-033,
// #267): PostPurposeCorrection reuses this primitive rather than opening a
// second write path (ADR-027); the schema's CHECK stops a between_accounts
// pair or a roll from setting it.
//
// reason is nil except for the two that say why a purpose pair moved
// (ADR-036): reasonRoll from CloseIncidentalAndRoll, reasonAllocation from
// PostPurposeMove. Corrections and between_accounts pairs pass nil; the
// schema's CHECK refuses a reason elsewhere.
//
// No argument-shape validation happens here (ADR-027): each exported caller
// validates, because the message ("from_account_id and to_account_id must
// differ") depends on which fields that caller holds fixed. This function
// trusts its caller.
//
// It takes an open store.Querier rather than opening its own transaction:
// CloseIncidentalAndRoll needs the pair and the incidental UPDATE in one
// atomic write, and a nested l.withTx is refused with txguard.ErrNested:
// under ADR-004's SetMaxOpenConns(1) the outer one holds the only connection. Every caller opens its withTx and passes its Querier in.
func (l *Ledger) postTransferPairTx(ctx context.Context, q store.Querier, fundID int64, kind string, from, to leg, amount money.Amount, occurredOn string, note *string, correctsTransactionID *int64, reason *string) (store.Transfer, error) {
	now := l.now().Unix()

	transfer, err := q.CreateTransfer(ctx, store.CreateTransferParams{
		FundID: fundID, Kind: kind, CorrectsTransactionID: correctsTransactionID, Reason: reason, CreatedAt: now,
	})
	if err != nil {
		return store.Transfer{}, fmt.Errorf("creating transfer: %w", err)
	}

	for _, p := range [...]struct {
		leg       leg
		direction string
	}{{from, "out"}, {to, "in"}} {
		if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID: fundID, AccountID: p.leg.AccountID, PurposeID: p.leg.PurposeID,
			Direction: p.direction, Amount: amount.Int64(), OccurredOn: occurredOn,
			Kind: "transfer", TransferID: &transfer.ID, Note: note, CreatedAt: now,
		}); err != nil {
			return store.Transfer{}, fmt.Errorf("posting transfer leg (%s): %w", p.direction, err)
		}
	}
	return transfer, nil
}

// PostTransferBetweenAccountsParams is every argument
// PostTransferBetweenAccounts needs: the same purpose, between two different
// accounts (the PRD's example: depositing the wallet's cash at the bank).
type PostTransferBetweenAccountsParams struct {
	FundID        int64
	PurposeID     int64
	FromAccountID int64
	ToAccountID   int64
	Amount        money.Amount // must be > 0
	OccurredOn    string       // "YYYY-MM-DD", a real calendar date

	// Note is written to both legs, or neither. One movement is one thing that
	// happened, so its explanation belongs on both halves; a note on the "out"
	// leg alone reads in the list as an unexplained arrival elsewhere. nil and an
	// empty string both mean no note; see normalizeNote.
	Note *string

	// Idempotency, when non-nil, makes the transfer safe to repeat (ADR-043).
	Idempotency *Idempotency
}

// PostTransferBetweenAccounts moves money from one account to another within
// the same purpose. The fund's total is unchanged by construction; only
// AccountBalance moves, on both sides, by exactly the amount, oppositely.
//
// Either location being retired (inactive_on set) is ErrAccountInactive and
// nothing is written (#444).
//
// With p.Idempotency set (ADR-043), a repeat returns the first transfer.
func (l *Ledger) PostTransferBetweenAccounts(ctx context.Context, p PostTransferBetweenAccountsParams) (store.Transfer, error) {
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

	from := leg{AccountID: p.FromAccountID, PurposeID: p.PurposeID}
	to := leg{AccountID: p.ToAccountID, PurposeID: p.PurposeID}
	if legsIdentical(from, to) {
		return store.Transfer{}, invalidField("to_account_id", "from_account_id and to_account_id must differ, got %d for both", p.FromAccountID)
	}

	// Both locations are checked inside the write's transaction: money neither
	// leaves a retired location nor arrives in one (#444). To empty a retired
	// location, reinstate it, move, retire again.
	transfer, err := withIdempotency(ctx, l, p.Idempotency, func(ctx context.Context, q store.Querier) (store.Transfer, error) {
		for _, accountID := range [...]int64{p.FromAccountID, p.ToAccountID} {
			if err := refuseInactiveAccount(ctx, q, p.FundID, accountID); err != nil {
				return store.Transfer{}, err
			}
		}
		return l.postTransferPairTx(ctx, q, p.FundID, "between_accounts", from, to, p.Amount, p.OccurredOn, normalizeNote(p.Note), nil, nil)
	})
	if err != nil {
		return store.Transfer{}, fmt.Errorf("posting transfer between accounts: %w", err)
	}
	return transfer, nil
}

// The two reasons a purpose pair that corrects nothing can carry (ADR-036):
// the schema's CHECK values, named once for the callers and tests.
const (
	reasonRoll       = "roll"
	reasonAllocation = "allocation"
)
