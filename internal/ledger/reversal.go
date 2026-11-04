package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
}

// ReversalFacts is what the ledger needs to know about one row to decide
// whether it can be reversed. The write and the list's reversible flag both
// fill it, so they share one rule (ReversalRefusal), not two that agree today.
type ReversalFacts struct {
	Kind       TransactionKind
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
	case TransactionKindDues:
	case TransactionKindNormal:
		if f.IsReconciliationFix {
			return ErrReversalReconciliationFix
		}
	case TransactionKindOpening:
		return ErrReversalOpening
	case TransactionKindReimbursement:
		return ErrReversalReimbursement
	case TransactionKindTransfer:
		return ErrReversalTransfer
	case TransactionKindAdjustment:
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

// DuesPaymentRefusal answers whether a dues payment can be reversed now (ADR-048):
// nil when it can, otherwise the named error ReverseDuesPayment returns. months
// are the facts of every dues row of the payment. A month an earlier reversal
// already took back is skipped, never refused; the first standing month
// ReversalRefusal refuses refuses the whole act; a payment with no month
// standing is ErrAlreadyReversed. The write and the list's reversible flag both
// ask it, so they share one rule.
func DuesPaymentRefusal(months []ReversalFacts, today string) error {
	standing := 0
	for _, m := range months {
		if m.Reversed {
			continue
		}
		if err := ReversalRefusal(m, today); err != nil {
			return err
		}
		standing++
	}
	if standing == 0 {
		return ErrAlreadyReversed
	}
	return nil
}

// reversalTarget is one row as a reversal needs it: the facts ReversalRefusal
// decides on, plus the fields a reversal row copies from it. GetReversalTarget
// and ListDuesPaymentReversalTargets read the same columns into two generated
// row types; both convert to this one, so the reversal is built in one place.
type reversalTarget struct {
	id                 int64
	accountID          int64
	effectivePurposeID int64
	direction          string
	amount             int64
	memberID           *int64
	duesPeriod         *string
	kind               string
	occurredOn         string
	duesPaymentID      *int64
	isReversal         bool
	reversed           bool
	isFix              bool
	effectiveClosed    bool
	counted            bool
}

func (t reversalTarget) facts() ReversalFacts {
	return ReversalFacts{
		Kind:                TransactionKind(t.kind),
		OccurredOn:          t.occurredOn,
		IsReversal:          t.isReversal,
		Reversed:            t.reversed,
		IsReconciliationFix: t.isFix,
		EffectiveClosed:     t.effectiveClosed,
		Counted:             t.counted,
	}
}

func targetOf(r store.GetReversalTargetRow) reversalTarget {
	return reversalTarget{
		id: r.ID, accountID: r.AccountID, effectivePurposeID: r.EffectivePurposeID, direction: r.Direction,
		amount: r.Amount, memberID: r.MemberID, duesPeriod: r.DuesPeriod, kind: r.Kind, occurredOn: r.OccurredOn,
		duesPaymentID: r.DuesPaymentID, isReversal: r.ReversesTransactionID != nil, reversed: r.Reversed != 0,
		isFix: r.IsReconciliationFix != 0, effectiveClosed: r.EffectiveClosedOn != nil, counted: r.Counted != 0,
	}
}

func targetOfPaymentRow(r store.ListDuesPaymentReversalTargetsRow) reversalTarget {
	return reversalTarget{
		id: r.ID, accountID: r.AccountID, effectivePurposeID: r.EffectivePurposeID, direction: r.Direction,
		amount: r.Amount, memberID: r.MemberID, duesPeriod: r.DuesPeriod, kind: r.Kind, occurredOn: r.OccurredOn,
		duesPaymentID: r.DuesPaymentID, isReversal: r.ReversesTransactionID != nil, reversed: r.Reversed != 0,
		isFix: r.IsReconciliationFix != 0, effectiveClosed: r.EffectiveClosedOn != nil, counted: r.Counted != 0,
	}
}

// postReversalTx inserts the reversal row of one target on the caller's open
// querier: the other way from the original, with its amount, location, member
// and period exactly (NULL included) and its EFFECTIVE purpose (ADR-033), dated
// by ReversalDate. It carries no dues_payment_id: a reversal names the row it
// undoes, which names the payment (ADR-048). The caller has already run
// ReversalRefusal.
func (l *Ledger) postReversalTx(ctx context.Context, q store.Querier, fundID int64, o reversalTarget, today string, note *string) (store.Transaction, error) {
	direction := DirectionIn
	if o.direction == string(DirectionIn) {
		direction = DirectionOut
	}
	originalID := o.id
	return q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID:                fundID,
		AccountID:             o.accountID,
		PurposeID:             o.effectivePurposeID,
		Direction:             string(direction),
		Amount:                o.amount,
		OccurredOn:            ReversalDate(o.occurredOn, o.counted, today),
		Kind:                  string(TransactionKindAdjustment),
		MemberID:              o.memberID,
		DuesPeriod:            o.duesPeriod,
		ReversesTransactionID: &originalID,
		Note:                  normalizeNote(note),
		CreatedAt:             l.now().Unix(),
	})
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
// A dues row is never reversed alone (ADR-048): the call reverses its whole
// payment, exactly as ReverseDuesPayment does, and returns the reversal of the
// row asked about, or - when that month was already reversed before this act
// (data from before ADR-048 only) - the first reversal the act posted.
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
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		row, err := q.GetReversalTarget(ctx, store.GetReversalTargetParams{
			FundID: p.FundID,
			ID:     p.TransactionID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrReversalNotFound
			}
			return fmt.Errorf("fetching transaction to reverse: %w", err)
		}
		original := targetOf(row)

		if original.kind == string(TransactionKindDues) && original.duesPaymentID != nil {
			rows, err := l.reverseDuesPaymentTx(ctx, q, p.FundID, *original.duesPaymentID, p.Note)
			if err != nil {
				return err
			}
			reversal = rows[0]
			for _, r := range rows {
				if r.ReversesTransactionID != nil && *r.ReversesTransactionID == p.TransactionID {
					reversal = r
				}
			}
			return nil
		}

		window, err := l.windowFor(ctx, q, p.FundID)
		if err != nil {
			return err
		}
		if err := ReversalRefusal(original.facts(), window.today); err != nil {
			return err
		}
		reversal, err = l.postReversalTx(ctx, q, p.FundID, original, window.today, p.Note)
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("reversing transaction: %w", err)
	}
	return reversal, nil
}

// ReverseDuesPaymentParams is every argument ReverseDuesPayment needs: the
// payment and the caller's note. As with ReverseTransactionParams, everything
// else is copied from the rows being reversed.
type ReverseDuesPaymentParams struct {
	FundID        int64
	DuesPaymentID int64
	Note          *string
}

// ReverseDuesPayment reverses a dues payment whole (ADR-048): one reversal row
// for each of its months still standing, all in one database transaction, so
// either every month is taken back or none is. It returns the posted rows in
// the order of the months they reverse.
//
// Each row keeps ADR-042's rules exactly as ReverseTransaction applies them to
// one: ReversalRefusal decides it, ReversalDate dates it, and the first
// refusal (a month under a closed envelope, a counted month dated after today)
// refuses the whole act with its named error. A month an earlier reversal
// already took back is skipped, not refused - only data from before ADR-048
// holds a payment reversed in part, and *batalkan* takes the rest. A payment
// with no month standing is ErrAlreadyReversed; another fund's payment, or
// none, is ErrReversalNotFound. The reversal rows carry no dues_payment_id.
func (l *Ledger) ReverseDuesPayment(ctx context.Context, p ReverseDuesPaymentParams) ([]store.Transaction, error) {
	var posted []store.Transaction
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		posted, err = l.reverseDuesPaymentTx(ctx, q, p.FundID, p.DuesPaymentID, p.Note)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("reversing dues payment: %w", err)
	}
	return posted, nil
}

// reverseDuesPaymentTx is ReverseDuesPayment on the caller's open querier
// (ADR-027's ...Tx rule). Every standing month is checked before any row is
// written, so a refusal leaves nothing behind even without the transaction.
func (l *Ledger) reverseDuesPaymentTx(ctx context.Context, q store.Querier, fundID, paymentID int64, note *string) ([]store.Transaction, error) {
	rows, err := q.ListDuesPaymentReversalTargets(ctx, store.ListDuesPaymentReversalTargetsParams{FundID: fundID, DuesPaymentID: &paymentID})
	if err != nil {
		return nil, fmt.Errorf("fetching dues payment to reverse: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrReversalNotFound
	}
	window, err := l.windowFor(ctx, q, fundID)
	if err != nil {
		return nil, err
	}

	targets := make([]reversalTarget, 0, len(rows))
	facts := make([]ReversalFacts, 0, len(rows))
	for _, row := range rows {
		t := targetOfPaymentRow(row)
		targets = append(targets, t)
		facts = append(facts, t.facts())
	}
	if err := DuesPaymentRefusal(facts, window.today); err != nil {
		return nil, err
	}
	var standing []reversalTarget
	for _, t := range targets {
		if !t.reversed {
			standing = append(standing, t)
		}
	}

	posted := make([]store.Transaction, 0, len(standing))
	for _, t := range standing {
		reversal, err := l.postReversalTx(ctx, q, fundID, t, window.today, note)
		if err != nil {
			return nil, err
		}
		posted = append(posted, reversal)
	}
	return posted, nil
}
