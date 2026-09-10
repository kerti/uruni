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
//
// One business-state check runs first, inside the same transaction as the
// write (ADR-031): if PurposeID names an incidental whose closed_on is set,
// the post is refused with ErrIncidentalClosed, in both directions - a late
// bill deserves attribution to the occasion exactly as much as a late
// contribution does. IncidentalClosedOnForPurpose returns zero rows for a
// 'main' or 'pass_through' PurposeID, so no purpose.kind branch is needed;
// sql.ErrNoRows there just means the guard does not apply. This is a
// read-before-write refusal in the same shape as ErrReimbursementAlreadySettled
// and ErrDuesPaymentAlreadyReversed, not a second write path - PostTransaction
// still inserts exactly one row either way.
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
		closedOn, err := q.IncidentalClosedOnForPurpose(ctx, p.PurposeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking incidental closed state: %w", err)
		}
		if err == nil && closedOn != nil {
			return ErrIncidentalClosed
		}

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

// OpeningBalance is one account's starting figure (PRD section 7.1), carried
// as a value rather than posted through its own method: #230's uniform rule
// is that a location and its opening balance are born together, in one
// database transaction, everywhere in the app - there is no longer any
// standalone way to post one. SetUpFund and Ledger.CreateAccount are the only
// two places that ever turn one into a row, each inside the same withTx that
// creates the account it belongs to.
//
// There is no Direction field: an opening balance is always kind='opening',
// direction='in' - a starting figure is money the ledger begins with, never
// money it begins owing.
type OpeningBalance struct {
	Amount     money.Amount // must be >= 0; a zero amount posts no row (see validateOpeningBalance)
	OccurredOn string       // "YYYY-MM-DD", a real calendar date; only checked when Amount > 0
	Note       *string
}

// validateOpeningBalance is the one shape check both SetUpFund and
// CreateAccount run before their own withTx opens, so a refused opening
// balance never leaves a partially-created location behind it (validate
// first, write once, ADR-027).
//
// OccurredOn is only checked when Amount > 0: a zero amount never reaches a
// write (see postOpeningBalanceRow's caller contract below), so a bogus date
// alongside a zero amount describes no row that will ever exist and is not
// this function's business to refuse.
func validateOpeningBalance(ob OpeningBalance) error {
	if ob.Amount < 0 {
		return fmt.Errorf("%w: opening balance amount must not be negative, got %d", ErrInvalidArgument, ob.Amount.Int64())
	}
	if ob.Amount > 0 {
		if err := validateOccurredOn(ob.OccurredOn); err != nil {
			return err
		}
	}
	return nil
}

// postOpeningBalanceRow inserts one kind='opening', direction='in' row for
// accountID, tagged to purposeID, against q inside a transaction the caller
// already has open. The caller (SetUpFund, CreateAccount) must have already
// run validateOpeningBalance and confirmed ob.Amount > 0 - a zero amount
// posts no row: "transaction" carries CHECK (amount > 0), binding every
// kind, and a zero opening balance carries no information the ledger lacks -
// an account with no opening entry already derives to 0 by summing an empty
// set (FundBalance, AccountBalance), which is exactly CLAUDE.md rule 2's
// "balances are derived by summing the ledger".
//
// There is no pre-check for an existing opening balance here: both callers
// create accountID in this same transaction, a few lines above, so it cannot
// already carry one. The schema's opening_balance_once_per_account partial
// unique index remains the actual guarantee against any other path.
func postOpeningBalanceRow(ctx context.Context, q store.Querier, fundID, accountID, purposeID int64, ob OpeningBalance, now int64) (store.Transaction, error) {
	return q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID:     fundID,
		AccountID:  accountID,
		PurposeID:  purposeID,
		Direction:  "in",
		Amount:     ob.Amount.Int64(),
		OccurredOn: ob.OccurredOn,
		Kind:       "opening",
		Note:       ob.Note,
		CreatedAt:  now,
	})
}

// CreateAccountParams is every argument Ledger.CreateAccount needs to bring
// one more location into existence, optionally with the starting figure it
// carries in.
type CreateAccountParams struct {
	FundID         int64
	Kind           string // "cash" or "bank" - the schema's own CHECK is the single source of truth (ADR-027)
	Name           string // non-empty after trimming - the schema's own CHECK refuses it, not pre-validated here (ADR-027)
	OpeningBalance *OpeningBalance
}

// CreateAccount writes one account row and, when OpeningBalance is present
// and its Amount is positive, its opening balance row, inside one withTx -
// #230's uniform rule applied to "a location added after setup." A refused
// opening balance means no account is created either: validateOpeningBalance
// runs before withTx opens, so a bad amount or date never reaches the write
// at all, and a schema violation on the account insert itself (a malformed
// kind, a blank name) aborts the same transaction before any opening balance
// is even considered.
func (l *Ledger) CreateAccount(ctx context.Context, p CreateAccountParams) (store.Account, error) {
	if p.OpeningBalance != nil {
		if err := validateOpeningBalance(*p.OpeningBalance); err != nil {
			return store.Account{}, err
		}
	}

	var account store.Account
	err := l.withTx(ctx, func(q store.Querier) error {
		now := time.Now().Unix()

		created, err := q.CreateAccount(ctx, store.CreateAccountParams{
			FundID: p.FundID, Kind: p.Kind, Name: p.Name, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		account = created

		if p.OpeningBalance != nil && p.OpeningBalance.Amount > 0 {
			purposeID, err := mainPurposeID(ctx, q, p.FundID)
			if err != nil {
				return err
			}
			if _, err := postOpeningBalanceRow(ctx, q, p.FundID, account.ID, purposeID, *p.OpeningBalance, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return store.Account{}, fmt.Errorf("creating account: %w", err)
	}
	return account, nil
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
