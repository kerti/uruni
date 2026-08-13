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

// occurredOnLayout is the schema's business-date shape (ADR-024): a calendar
// day the treasurer's week runs on, never an instant.
const occurredOnLayout = "2006-01-02"

// PostTransactionParams is every argument PostTransaction needs to post one
// ordinary or adjusting entry.
//
// There is deliberately no Kind field. PostTransaction (#39) covers exactly
// kind='normal' and kind='adjustment' - dues, reimbursement and transfer each
// carry their own extra schema invariant (member_id+dues_period,
// reimbursement_id, transfer_id) that this method does not check, and get
// their own method in a later slice (#40-#42). A string Kind field would let a
// caller reach those kinds through this method without the invariant that
// makes them safe - CreateTransactionParams would accept it and the schema's
// CHECKs would reject most such calls, but not all of them (kind='opening' has
// no extra CHECK at all). IsAdjustment, a plain bool with exactly two
// outcomes, is the widest surface this method can expose without that risk.
type PostTransactionParams struct {
	FundID     int64
	AccountID  int64
	PurposeID  int64
	Direction  string       // "in" or "out"
	Amount     money.Amount // must be > 0
	OccurredOn string       // "YYYY-MM-DD", a real calendar date
	Note       *string

	// IsAdjustment selects kind='adjustment' over kind='normal'. A correction
	// may be posted on any Tuesday, not only during a reconciliation
	// (ADR-024), so this needs no other input to distinguish it.
	IsAdjustment bool
}

// PostTransaction writes one kind='normal' or kind='adjustment' entry and
// returns the created row.
//
// Argument-shape failures - amount <= 0, a malformed or calendar-invalid
// occurred_on, an empty or unrecognized direction - are rejected before the
// write reaches the schema's CHECK constraints, wrapping ErrInvalidArgument
// and naming the field (ADR-027). Everything else the write can fail on - an
// account belonging to another fund, an id nothing created - is a domain bug,
// not a caller mistake, and is wrapped generically for M4 to map to a 500.
func (l *Ledger) PostTransaction(ctx context.Context, p PostTransactionParams) (store.Transaction, error) {
	if p.Amount <= 0 {
		return store.Transaction{}, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidArgument, p.Amount.Int64())
	}
	if p.Direction != "in" && p.Direction != "out" {
		return store.Transaction{}, fmt.Errorf("%w: direction must be \"in\" or \"out\", got %q", ErrInvalidArgument, p.Direction)
	}
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return store.Transaction{}, err
	}

	kind := "normal"
	if p.IsAdjustment {
		kind = "adjustment"
	}

	var posted store.Transaction
	err := l.withTx(ctx, func(q store.Querier) error {
		var err error
		posted, err = q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID:     p.FundID,
			AccountID:  p.AccountID,
			PurposeID:  p.PurposeID,
			Direction:  p.Direction,
			Amount:     p.Amount.Int64(),
			OccurredOn: p.OccurredOn,
			Kind:       kind,
			Note:       p.Note,
			CreatedAt:  time.Now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("posting transaction: %w", err)
	}
	return posted, nil
}

// PostOpeningBalanceParams is every argument PostOpeningBalance needs to
// record one account's starting figure.
//
// There is no Direction field: an opening balance is always kind='opening',
// direction='in' - a starting figure is money the ledger begins with, never
// money it begins owing.
type PostOpeningBalanceParams struct {
	FundID     int64
	AccountID  int64
	PurposeID  int64
	Amount     money.Amount // must be >= 0; a zero amount posts no row (see below)
	OccurredOn string       // "YYYY-MM-DD", a real calendar date
	Note       *string
}

// PostOpeningBalance writes one kind='opening' row, direction='in', carrying
// an account's starting figure, and returns the created row.
//
// A zero amount posts no row and returns no error, rather than the naive
// reading "zero is legal, so write it": "transaction" carries CHECK (amount
// > 0), binding every kind, so a zero-amount row cannot exist without
// weakening the constraint that protects every other kind too. It also
// carries no information the ledger lacks - an account with no opening entry
// already derives to 0 by summing an empty set (FundBalance, AccountBalance),
// which is exactly CLAUDE.md rule 2's "balances are derived by summing the
// ledger". A negative amount is ErrInvalidArgument.
//
// On that zero path the returned store.Transaction is the zero value, so a
// caller needing to tell "posted nothing" from "posted a row" tests
// posted.ID != 0. Nothing in M3 needs to; M4's setup flow is the first caller
// that might.
//
// A second call for an account that already has an opening entry returns
// ErrOpeningBalanceExists and writes nothing. The pre-check inside withTx
// exists to name that error cleanly; the schema's
// opening_balance_once_per_account partial unique index is the actual
// guarantee, and under ADR-004's SetMaxOpenConns(1) a race between the check
// and the insert cannot happen, so this is not a lock (ADR-027, mirroring
// SettleReimbursement's settled-once check).
func (l *Ledger) PostOpeningBalance(ctx context.Context, p PostOpeningBalanceParams) (store.Transaction, error) {
	if p.Amount < 0 {
		return store.Transaction{}, fmt.Errorf("%w: amount must not be negative, got %d", ErrInvalidArgument, p.Amount.Int64())
	}
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return store.Transaction{}, err
	}
	if p.Amount == 0 {
		return store.Transaction{}, nil
	}

	var posted store.Transaction
	err := l.withTx(ctx, func(q store.Querier) error {
		_, err := q.GetOpeningBalance(ctx, store.GetOpeningBalanceParams{FundID: p.FundID, AccountID: p.AccountID})
		if err == nil {
			return ErrOpeningBalanceExists
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking for an existing opening balance: %w", err)
		}

		posted, err = q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID:     p.FundID,
			AccountID:  p.AccountID,
			PurposeID:  p.PurposeID,
			Direction:  "in",
			Amount:     p.Amount.Int64(),
			OccurredOn: p.OccurredOn,
			Kind:       "opening",
			Note:       p.Note,
			CreatedAt:  time.Now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("posting opening balance: %w", err)
	}
	return posted, nil
}

// validateOccurredOn rejects anything that is not a real YYYY-MM-DD calendar
// date.
//
// time.Parse with this exact, strict layout already refuses an out-of-range
// month or day - "2026-02-30" fails to parse rather than normalizing to
// "2026-03-02" - which is why no separate calendar-range check is needed. The
// round-trip format comparison is a second, cheap line of defense: it is what
// this function leans on to stay correct even if that parsing behavior ever
// changed, rather than a check load-bearing today.
func validateOccurredOn(s string) error {
	t, err := time.Parse(occurredOnLayout, s)
	if err != nil {
		return fmt.Errorf("%w: occurred_on %q is not a valid calendar date: %v", ErrInvalidArgument, s, err)
	}
	if t.Format(occurredOnLayout) != s {
		return fmt.Errorf("%w: occurred_on %q is not a valid calendar date", ErrInvalidArgument, s)
	}
	return nil
}
