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
// day, never an instant.
const occurredOnLayout = "2006-01-02"

// PostTransactionParams is every argument PostTransaction needs to post one
// ordinary or adjusting entry.
//
// There is no Kind field. PostTransaction (#39) covers kind='normal' and
// kind='adjustment' only; dues, reimbursement and transfer each carry extra
// schema invariants (member_id+dues_period, reimbursement_id, transfer_id)
// this method does not check, and have their own methods (PostDuesPayments,
// PostTransferBetweenAccounts, the reimbursement ones). A string Kind would
// let a caller reach those kinds without the invariant that makes them safe;
// the schema's CHECKs would reject most such calls but not all
// (kind='opening' has no extra CHECK). IsAdjustment, a bool with two
// outcomes, is the widest surface this method can expose safely.
type PostTransactionParams struct {
	FundID     int64
	AccountID  int64
	PurposeID  int64
	Direction  string       // "in" or "out"
	Amount     money.Amount // must be > 0
	OccurredOn string       // "YYYY-MM-DD", a real calendar date
	Note       *string

	// IsAdjustment selects kind='adjustment' over kind='normal'. A correction may
	// be posted any day, not only during a reconciliation (ADR-024).
	IsAdjustment bool

	// MemberID optionally names the member a kind='normal', direction='in' row is
	// a contribution from (ADR-034, #211); a guest or anonymous giver leaves it
	// nil. It is valid only on that shape: naming a member on an adjustment or an
	// outgoing row is refused before the write, and on a purpose that is not an
	// incidental inside the write's withTx, once PurposeID's kind is known
	// (ErrContributionRequiresIncidentalPurpose).
	MemberID *int64
}

// PostTransaction writes one kind='normal' or kind='adjustment' entry and
// returns the created row.
//
// Argument-shape failures - amount <= 0, a malformed or calendar-invalid
// occurred_on, an empty or unrecognized direction - are rejected before the
// write reaches the schema's CHECKs, wrapping ErrInvalidArgument and naming
// the field (ADR-027). Anything else the write can fail on, such as another
// fund's account or an id nothing created, is a domain bug wrapped generically
// for a 500.
//
// Two business-state checks run first, in the write's transaction. A retired
// AccountID (inactive_on set) is ErrAccountInactive, the rule PostPurposeMove
// applies and the record forms apply by not offering it (#444). Then
// (ADR-031): if PurposeID names an incidental whose closed_on is set, the
// post is ErrIncidentalClosed in both directions (a late bill deserves
// attribution to the occasion as much as a late contribution).
// IncidentalClosedOnForPurpose returns no rows for a 'main' or 'pass_through'
// PurposeID, so no purpose.kind branch is needed; sql.ErrNoRows means the
// guard does not apply. It is a read-before-write refusal, like
// ErrReimbursementAlreadySettled, not a second write path.
func (l *Ledger) PostTransaction(ctx context.Context, p PostTransactionParams) (store.Transaction, error) {
	if p.Amount <= 0 {
		return store.Transaction{}, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidArgument, p.Amount.Int64())
	}
	if err := CheckAmountCeiling("amount", p.Amount); err != nil {
		return store.Transaction{}, err
	}
	if p.Direction != "in" && p.Direction != "out" {
		return store.Transaction{}, fmt.Errorf("%w: direction must be \"in\" or \"out\", got %q", ErrInvalidArgument, p.Direction)
	}
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return store.Transaction{}, err
	}
	if p.MemberID != nil {
		if p.IsAdjustment {
			return store.Transaction{}, fmt.Errorf("%w: member_id is only valid on a contribution, not an adjustment", ErrInvalidArgument)
		}
		if p.Direction != "in" {
			return store.Transaction{}, fmt.Errorf("%w: member_id is only valid on an incoming contribution", ErrInvalidArgument)
		}
	}

	kind := "normal"
	if p.IsAdjustment {
		kind = "adjustment"
	}

	var posted store.Transaction
	err := l.withTx(ctx, func(q store.Querier) error {
		if err := refuseInactiveAccount(ctx, q, p.FundID, p.AccountID); err != nil {
			return err
		}

		closedOn, err := q.IncidentalClosedOnForPurpose(ctx, p.PurposeID)
		switch {
		case err == nil:
			if closedOn != nil {
				return ErrIncidentalClosed
			}
		case errors.Is(err, sql.ErrNoRows):
			// PurposeID does not name an incidental, so no closed-envelope guard applies.
			// A contribution's member_id needs the opposite reading (ADR-034): naming a
			// member on any other purpose is refused with a clean error now, not the
			// BEFORE INSERT trigger's raw message.
			if p.MemberID != nil {
				return ErrContributionRequiresIncidentalPurpose
			}
		default:
			return fmt.Errorf("checking incidental closed state: %w", err)
		}

		posted, err = q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID:     p.FundID,
			AccountID:  p.AccountID,
			PurposeID:  p.PurposeID,
			Direction:  p.Direction,
			Amount:     p.Amount.Int64(),
			OccurredOn: p.OccurredOn,
			Kind:       kind,
			MemberID:   p.MemberID,
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
// as a value, not posted through its own method: #230's rule is that a
// location and its opening balance are born together in one database
// transaction, with no standalone way to post one. SetUpFund and
// Ledger.CreateAccount are the only places that turn one into a row, each in
// the withTx that creates the account.
//
// There is no Direction field: an opening balance is always kind='opening',
// direction='in', money the ledger begins with, never money it begins owing.
type OpeningBalance struct {
	Amount     money.Amount // must be >= 0; a zero amount posts no row (see validateOpeningBalance)
	OccurredOn string       // "YYYY-MM-DD", a real calendar date; only checked when Amount > 0
	Note       *string
}

// validateOpeningBalance is the one shape check SetUpFund and CreateAccount
// run before their withTx opens, so a refused opening balance never leaves a
// partially-created location (validate first, write once, ADR-027).
//
// OccurredOn is checked only when Amount > 0: a zero amount never reaches a
// write (see postOpeningBalanceRow), so a bogus date beside it describes no
// row.
func validateOpeningBalance(ob OpeningBalance) error {
	if ob.Amount < 0 {
		return fmt.Errorf("%w: opening balance amount must not be negative, got %d", ErrInvalidArgument, ob.Amount.Int64())
	}
	if err := CheckAmountCeiling("opening balance amount", ob.Amount); err != nil {
		return err
	}
	if ob.Amount > 0 {
		if err := validateOccurredOn(ob.OccurredOn); err != nil {
			return err
		}
	}
	return nil
}

// postOpeningBalanceRow inserts one kind='opening', direction='in' row for
// accountID, tagged to purposeID, against q in a transaction the caller has
// open. The caller (SetUpFund, CreateAccount) must have run
// validateOpeningBalance and confirmed ob.Amount > 0: a zero amount posts no
// row ("transaction" has CHECK (amount > 0) for every kind, and an account
// with no opening entry derives to 0 by summing an empty set, CLAUDE.md
// rule 2).
//
// No pre-check for an existing opening balance: both callers create accountID
// in this transaction, so it cannot carry one. The
// opening_balance_once_per_account partial unique index guards any other path.
func postOpeningBalanceRow(ctx context.Context, q store.Querier, fundID, accountID, purposeID int64, ob OpeningBalance, now int64) (store.Transaction, error) {
	return q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID:     fundID,
		AccountID:  accountID,
		PurposeID:  purposeID,
		Direction:  "in",
		Amount:     ob.Amount.Int64(),
		OccurredOn: ob.OccurredOn,
		Kind:       "opening",
		Note:       normalizeNote(ob.Note),
		CreatedAt:  now,
	})
}

// CreateAccountParams is every argument Ledger.CreateAccount needs to add one
// location, optionally with its starting figure.
type CreateAccountParams struct {
	FundID         int64
	Kind           string // "cash" or "bank" - the schema's own CHECK is the single source of truth (ADR-027)
	Name           string // non-empty after trimming - the schema's own CHECK refuses it, not pre-validated here (ADR-027)
	OpeningBalance *OpeningBalance
}

// CreateAccount writes one account row and, when OpeningBalance is present
// with a positive Amount, its opening balance row, in one withTx (#230's rule
// for a location added after setup). A refused opening balance creates no
// account: validateOpeningBalance runs before withTx opens, and a schema
// violation on the account insert (malformed kind, blank name) aborts the
// transaction before any opening balance is considered.
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
// time.Parse with this strict layout already refuses an out-of-range month or
// day ("2026-02-30" fails rather than normalizing to "2026-03-02"), so no
// separate range check is needed. The round-trip format comparison is a
// cheap second defense should that parsing behavior change, not load-bearing
// today.
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
