package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// PostPurposeCorrectionParams is every argument PostPurposeCorrection needs
// to fix one posted row's peruntukan (ADR-033).
//
// There is no AccountID, Direction, Amount or OccurredOn field: all four are
// read off the row being corrected. The date is not an input because dating it
// today would leave the month the mistake happened wrong in both tags.
type PostPurposeCorrectionParams struct {
	FundID        int64
	TransactionID int64 // the posted row whose peruntukan is wrong
	PurposeID     int64 // the target: what it should have been tagged

	// Idempotency, when non-nil, makes the correction safe to repeat (ADR-043).
	Idempotency *Idempotency
}

// PostPurposeCorrection posts a kind='reclass_purpose' pair that moves one
// posted row's attribution from its effective peruntukan to PurposeID,
// carrying the original row's account_id and occurred_on on both legs and
// corrects_transaction_id naming the row it corrects.
//
// It is built on postTransferPairTx (transfer.go), which CloseIncidentalAndRoll
// also reuses: no second write path.
//
// Direction is the crux (ADR-033): postTransferPairTx posts 'out' at its
// "from" leg and 'in' at its "to" leg, but which purpose plays which role
// flips with the original row's direction, because "undo the original
// attribution and re-apply it to the target" depends on which way the
// original moved money:
//
//   - The original was direction='out' (an expense mis-tagged; ADR-033's
//     example: an out mis-tagged to Titipan). It contributed -amount to the
//     effective purpose's balance and nothing to the target's. To leave the
//     effective purpose "as if never tagged there" the pair adds +amount to
//     it - an 'in', the "to" leg. To leave the target "as if it had always
//     held this row" the pair adds -amount to it - an 'out', the "from" leg.
//   - The original was direction='in' (a contribution mis-tagged). It
//     contributed +amount to the effective purpose and nothing to the target.
//     Undoing that needs -amount at the effective purpose (an 'out', the
//     "from" leg) and +amount at the target (an 'in', the "to" leg): the
//     mirror image.
//
// Either way both legs share the original row's account_id, so the pair nets
// to zero on that account by construction and only the purpose the amount
// counts against moves; the untouched account balance ADR-033 promises falls
// out of postTransferPairTx's shape, with no separate check.
//
// The effective peruntukan, not the stored purpose_id, is what "undo the
// original attribution" undoes: a correction built from the stored tag would
// move money out of a tag that no longer holds it and drive it negative
// (ADR-033).
//
// Eligibility is ADR-033's table, each refusal its own named error
// (ErrPurposeCorrection...), decided by CorrectionRefusal so the list's
// correctable flag cannot disagree. A correction's legs are kind='transfer',
// which is what makes a correction of a correction impossible.
// Correct-then-reverse is allowed (the reversal copies the effective purpose);
// reverse-then-correct is refused, ADR-042. postTransferPairTx checks nothing
// by design, so a closed source or target incidental is refused here, ahead of
// it (ADR-031).
//
// With p.Idempotency set (ADR-043), a repeat returns the first pair and none of
// the checks above runs, so the repeat of a correction that worked is never the
// Noop its own result would make it.
func (l *Ledger) PostPurposeCorrection(ctx context.Context, p PostPurposeCorrectionParams) (store.Transfer, error) {
	created, err := withIdempotency(ctx, l, p.Idempotency, func(ctx context.Context, q store.Querier) (store.Transfer, error) {
		original, err := q.GetTransactionForFund(ctx, store.GetTransactionForFundParams{
			FundID: p.FundID,
			ID:     p.TransactionID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return store.Transfer{}, ErrPurposeCorrectionNotFound
			}
			return store.Transfer{}, fmt.Errorf("fetching transaction to correct: %w", err)
		}

		reversed, err := q.ReversedFlagForTransaction(ctx, store.ReversedFlagForTransactionParams{
			FundID: p.FundID,
			ID:     p.TransactionID,
		})
		if err != nil {
			return store.Transfer{}, fmt.Errorf("checking whether the transaction was reversed: %w", err)
		}
		effectivePurposeID, err := effectivePeruntukan(ctx, q, p.FundID, original)
		if err != nil {
			return store.Transfer{}, err
		}
		sourceClosed, err := closedIncidental(ctx, q, effectivePurposeID)
		if err != nil {
			return store.Transfer{}, err
		}
		if err := CorrectionRefusal(CorrectionFacts{
			Kind:            TransactionKind(original.Kind),
			IsReversal:      original.ReversesTransactionID != nil,
			Named:           original.MemberID != nil,
			Reversed:        reversed != 0,
			EffectiveClosed: sourceClosed,
		}); err != nil {
			return store.Transfer{}, err
		}

		// What is left depends on the target, which only the write has.
		if p.PurposeID == effectivePurposeID {
			return store.Transfer{}, ErrPurposeCorrectionNoop
		}
		targetClosed, err := closedIncidental(ctx, q, p.PurposeID)
		if err != nil {
			return store.Transfer{}, err
		}
		if targetClosed {
			return store.Transfer{}, ErrPurposeCorrectionTargetClosed
		}

		// The direction flip worked out in the doc comment: 'out' needs the
		// effective purpose to receive the pair's 'in' leg and the target the 'out'
		// leg; 'in' is the mirror image.
		var from, to leg
		switch Direction(original.Direction) {
		case DirectionOut:
			from = leg{AccountID: original.AccountID, PurposeID: p.PurposeID}
			to = leg{AccountID: original.AccountID, PurposeID: effectivePurposeID}
		default: // "in" - the schema's own CHECK admits no third value.
			from = leg{AccountID: original.AccountID, PurposeID: effectivePurposeID}
			to = leg{AccountID: original.AccountID, PurposeID: p.PurposeID}
		}

		transactionID := p.TransactionID
		pair, err := l.postTransferPairTx(ctx, q, transferPair{
			FundID: p.FundID, Kind: TransferKindReclassPurpose, From: from, To: to,
			Amount: money.FromDB(original.Amount), OccurredOn: original.OccurredOn, CorrectsTransactionID: &transactionID,
		})
		if err != nil {
			return store.Transfer{}, fmt.Errorf("posting purpose correction: %w", err)
		}
		return pair, nil
	})
	if err != nil {
		return store.Transfer{}, fmt.Errorf("correcting purpose: %w", err)
	}
	return created, nil
}

// effectivePeruntukan is the row's current tag (ADR-033): its stored
// purpose_id until a correction moves it, then the end of the path its
// corrections walk. EffectivePurposeForTransaction reads it from
// ledger_entry.effective_purpose_id, which derives that end without ordering
// the corrections. The stored tag alone is unsafe once a correction exists.
func effectivePeruntukan(ctx context.Context, q store.Querier, fundID int64, original store.Transaction) (int64, error) {
	id, err := q.EffectivePurposeForTransaction(ctx, store.EffectivePurposeForTransactionParams{
		FundID: fundID, ID: original.ID,
	})
	if err != nil {
		return 0, fmt.Errorf("finding the effective purpose: %w", err)
	}
	return id, nil
}

// CorrectionFacts is what CorrectionRefusal decides on: one row's facts, none
// of them about the target pos. The write reads them off the row; GET
// /api/transactions reads the same facts off its page query.
type CorrectionFacts struct {
	Kind TransactionKind
	// IsReversal: the row carries reverses_transaction_id (a kind='adjustment' one).
	IsReversal bool
	// Named: a kind='normal' row with member_id set, a named contribution.
	Named bool
	// Reversed: ledger_entry.reversed, something already reverses the row.
	Reversed bool
	// EffectiveClosed: the row's EFFECTIVE purpose is a closed envelope.
	EffectiveClosed bool
}

// CorrectionRefusal answers whether a row of the given facts may have its pos
// corrected at all (ADR-033's eligibility table): nil when it may, otherwise
// the named error PostPurposeCorrection returns. The write and the list's
// correctable flag both ask it, so the door and the refusal share one rule
// (#821). What depends on the target (a no-op, a closed target) is the
// write's alone.
//
// A named contribution is refused (ADR-034): moving it would leave
// participation counting the member against an envelope the money has left.
// Reverse-then-correct is refused (ADR-042): the reversal already took the
// money out of the row's effective purpose. A closed effective envelope is
// refused because correcting out of it would leave its balance non-zero,
// breaking the rollover invariant (ADR-031).
func CorrectionRefusal(f CorrectionFacts) error {
	switch f.Kind {
	case TransactionKindOpening:
		return ErrPurposeCorrectionOpening
	case TransactionKindDues:
		return ErrPurposeCorrectionDues
	case TransactionKindReimbursement:
		return ErrPurposeCorrectionReimbursement
	case TransactionKindTransfer:
		return ErrPurposeCorrectionTransfer
	case TransactionKindAdjustment:
		if f.IsReversal {
			return ErrPurposeCorrectionReversal
		}
	case TransactionKindNormal:
		if f.Named {
			return ErrPurposeCorrectionNamedContribution
		}
	default:
		return fmt.Errorf("transaction kind %q has no correction rule", f.Kind)
	}
	if f.Reversed {
		return ErrPurposeCorrectionAlreadyReversed
	}
	if f.EffectiveClosed {
		return ErrPurposeCorrectionSourceClosed
	}
	return nil
}

// closedIncidental reports whether purposeID names a closed incidental: false
// when it is not an incidental at all (main, pass_through), "no row, no
// guard", as PostTransaction's IncidentalClosedOnForPurpose check.
// postTransferPairTx checks nothing (ADR-033), so PostPurposeCorrection asks
// this for the effective peruntukan and the target ahead of it.
func closedIncidental(ctx context.Context, q store.Querier, purposeID int64) (bool, error) {
	closedOn, err := q.IncidentalClosedOnForPurpose(ctx, purposeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("checking incidental closed state: %w", err)
	}
	return closedOn != nil, nil
}

// refuseClosedIncidental returns refusal when purposeID names a closed
// incidental, and nil otherwise (closedIncidental), for the writes whose
// postTransferPairTx or insert checks nothing itself.
func refuseClosedIncidental(ctx context.Context, q store.Querier, purposeID int64, refusal error) error {
	closed, err := closedIncidental(ctx, q, purposeID)
	if err != nil {
		return err
	}
	if closed {
		return refusal
	}
	return nil
}
