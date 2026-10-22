package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// ReverseTransactionParams is every argument ReverseTransaction needs to
// reverse one previously-posted dues payment or normal transaction (ADR-042).
//
// There is no account, purpose, amount, direction, member, period or date
// field: all but the date are copied from the original row (the purpose being
// its EFFECTIVE one, ADR-033) and the date is the ledger's pick, so a reversal
// can never disagree with what it reverses. Only the note is the caller's.
type ReverseTransactionParams struct {
	FundID        int64
	TransactionID int64 // the kind='dues' or kind='normal' row being reversed
	Note          *string

	// Now is the clock "today" is read from (ADR-041). It only matters once a
	// cek kas has counted the row, which is dated today.
	// Zero means time.Now().
	Now time.Time
}

// ReversalFacts is what the ledger needs to know about one row to decide
// whether it can be reversed. The write and the list's reversible flag both
// fill it, so they share one rule (ReversalRefusal), not two that agree today.
type ReversalFacts struct {
	Kind       string // transaction.kind
	OccurredOn string // the row's own date, "YYYY-MM-DD"

	// IsReversal: the row carries reverses_transaction_id (a kind='adjustment' one).
	IsReversal bool
	// Reversed: ledger_entry.reversed, something already reverses the row.
	Reversed bool
	// IsReconciliationFix: a cek kas line names the row as its fix.
	IsReconciliationFix bool
	// EffectiveClosed: the row's EFFECTIVE purpose is a closed envelope.
	EffectiveClosed bool
	// Counted: some cek kas of the fund has counted the row (its cutoff is at or
	// past the row's id).
	Counted bool
}

// ReversalRefusal answers whether a row of the given facts can be reversed on a
// day whose fund-zone date is today: nil when it can, otherwise the named error
// ReverseTransaction returns. The order is the ADR-042 table's.
//
// Opening, reimbursement and transfer rows each carry a status a reversal would
// leave lying. A closed effective envelope refuses because the reversal posts
// there (ADR-031). Counted and dated after today refuses because the reversal
// would take today, ahead of what it reverses.
func ReversalRefusal(f ReversalFacts, today string) error {
	switch f.Kind {
	case "dues":
	case "normal":
		if f.IsReconciliationFix {
			return ErrReversalReconciliationFix
		}
	case "opening":
		return ErrReversalOpening
	case "reimbursement":
		return ErrReversalReimbursement
	case "transfer":
		return ErrReversalTransfer
	case "adjustment":
		if f.IsReversal {
			return ErrReversalOfReversal
		}
		return ErrReversalAdjustment
	default:
		return fmt.Errorf("transaction kind %q has no reversal rule", f.Kind)
	}
	if f.Reversed {
		return ErrAlreadyReversed
	}
	if f.EffectiveClosed {
		return ErrIncidentalClosed
	}
	if f.Counted && f.OccurredOn > today {
		return ErrReversalBeforeOriginal
	}
	return nil
}

// ReversalDate is the day a reversal of a row is dated (ADR-042). The row's own
// day, unless a cek kas has counted it since: a count freezes the balance of
// every location it counted, and a reversal dated back before it would change a
// past location balance under a frozen snapshot. Then it is today in the fund's
// zone. Neither precedes the row (ReversalRefusal refuses the one case where
// today would), so no floor or ceiling is checked: the caller supplies no date,
// and a date the ledger picks or inherits is not ceiling-checked (ADR-041).
func ReversalDate(occurredOn string, counted bool, today string) string {
	if counted {
		return today
	}
	return occurredOn
}

// ReverseTransaction posts one kind='adjustment' row that reverses a
// previously-posted kind='dues' or kind='normal' row (ADR-029, ADR-034,
// ADR-042), carrying reverses_transaction_id naming it. It returns the posted
// reversal row.
//
// The reversal goes the OTHER way from its original, with the original's
// amount, account, member_id and dues_period exactly as they are (NULL
// included), and the original's EFFECTIVE purpose (ADR-033), not the stored
// one: the stored purpose of a purpose-corrected row would debit the
// corrected-to purpose and credit the wrong one. It is full-amount and once
// only. The date is ReversalDate's; the note is the caller's own and never
// copied from the original.
//
// The original is fetched fund-scoped, so ErrReversalNotFound answers for
// another fund's id as for a missing one. The transaction_named_row_shape
// trigger re-verifies direction, amount, location, member and period
// independently of this method.
//
// A reversal inherits its location from the original even if the location has
// since been retired, as PostPurposeCorrection does: refusing would make a
// mistake on a retired location uncorrectable (CLAUDE.md rule 3), so
// ErrAccountInactive does not apply.
func (l *Ledger) ReverseTransaction(ctx context.Context, p ReverseTransactionParams) (store.Transaction, error) {
	var reversal store.Transaction
	err := l.withTx(ctx, func(q store.Querier) error {
		original, err := q.GetReversalTarget(ctx, store.GetReversalTargetParams{
			FundID: p.FundID,
			ID:     p.TransactionID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrReversalNotFound
			}
			return fmt.Errorf("fetching transaction to reverse: %w", err)
		}

		window, err := l.windowFor(ctx, q, p.FundID, p.Now)
		if err != nil {
			return err
		}
		counted := original.Counted != 0
		if err := ReversalRefusal(ReversalFacts{
			Kind:                original.Kind,
			OccurredOn:          original.OccurredOn,
			IsReversal:          original.ReversesTransactionID != nil,
			Reversed:            original.Reversed != 0,
			IsReconciliationFix: original.IsReconciliationFix != 0,
			EffectiveClosed:     original.EffectiveClosedOn != nil,
			Counted:             counted,
		}, window.today); err != nil {
			return err
		}

		direction := "in"
		if original.Direction == "in" {
			direction = "out"
		}
		reversal, err = q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID:                p.FundID,
			AccountID:             original.AccountID,
			PurposeID:             original.EffectivePurposeID,
			Direction:             direction,
			Amount:                original.Amount,
			OccurredOn:            ReversalDate(original.OccurredOn, counted, window.today),
			Kind:                  "adjustment",
			MemberID:              original.MemberID,
			DuesPeriod:            original.DuesPeriod,
			ReversesTransactionID: &p.TransactionID,
			Note:                  normalizeNote(p.Note),
			CreatedAt:             time.Now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("reversing transaction: %w", err)
	}
	return reversal, nil
}
