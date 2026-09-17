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
// to fix one posted row's peruntukan (ADR-033, #267).
//
// There is deliberately no AccountID, Direction, Amount or OccurredOn field:
// all four are read off the row being corrected, never re-typed by the
// caller - the same discipline ReverseDuesPaymentParams already holds to for
// the payment it reverses. A correction that could name a different account
// or date than the row it corrects would not be a correction of that row,
// and ADR-033 is explicit that the date is not an input at all: dating it
// today would leave the month the mistake happened wrong in both tags.
type PostPurposeCorrectionParams struct {
	FundID        int64
	TransactionID int64 // the posted row whose peruntukan is wrong
	PurposeID     int64 // the target: what it should have been tagged
}

// PostPurposeCorrection posts a kind='reclass_purpose' pair that moves one
// posted row's attribution from its effective peruntukan to PurposeID,
// carrying the original row's own account_id and occurred_on on both legs
// and corrects_transaction_id naming the row it corrects.
//
// It is built entirely on postTransferPairTx (internal/ledger/transfer.go),
// the same primitive CloseIncidentalAndRoll already reuses for its own
// reclass_purpose rolls - there is no second write path, per ADR-027's
// narrow PostTransaction and ADR-033's own decision.
//
// Direction is the crux (ADR-033): postTransferPairTx always posts 'out' at
// its "from" leg and 'in' at its "to" leg, but which purpose plays which
// role flips with the original row's own direction, because "undo the
// original attribution and re-apply it to the target" means something
// different depending on which way the original row already moved money:
//
//   - The original was direction='out' (an expense mis-tagged, ADR-033's
//     own worked example: an out mis-tagged to Titipan). The original row
//     already contributed -amount to the effective purpose's balance and
//     nothing to the target's. To leave the effective purpose "as if this
//     row had never been tagged there" the pair must add +amount to it -
//     an 'in', so it is the "to" leg. To leave the target "as if it had
//     always held this row" the pair must add -amount to it - an 'out', so
//     it is the "from" leg.
//   - The original was direction='in' (a contribution mis-tagged). The
//     original row already contributed +amount to the effective purpose
//     and nothing to the target. Undoing that needs -amount at the
//     effective purpose (an 'out', the "from" leg) and +amount at the
//     target (an 'in', the "to" leg) - the mirror image of the 'out' case.
//
// Either way both legs share the original row's account_id, so the pair's
// own net effect on that account is zero by construction (one 'out', one
// 'in', same account) and the only thing that moves is which purpose the
// amount counts against - the account balance ADR-033 promises stays
// untouched falls out of postTransferPairTx's own shape, not a separate
// check here.
//
// The effective peruntukan - not the row's stored purpose_id - is what
// "undo the original attribution" undoes. GetLatestPurposeCorrectionForTransaction's
// caller-facing wrapper below follows every prior correction pointing at
// TransactionID and takes the latest one's destination; with none, the
// stored purpose_id is still accurate, since nothing has moved the tag yet.
// A second correction built from the stored tag instead would move money
// out of a tag that no longer holds it and drive it negative - the exact
// defect #266 reports, re-created by the tool meant to fix it (ADR-033).
//
// Eligibility, each its own named error rather than one blanket rule
// (ADR-033):
//
//   - kind='opening': ErrPurposeCorrectionOpening - Kas Utama by
//     construction, nothing to fix.
//   - kind='dues': ErrPurposeCorrectionDues - a different entry, not a
//     mis-tag.
//   - kind='reimbursement': ErrPurposeCorrectionReimbursement - its payout
//     inherits the claim's peruntukan; the claim's own edit is where to fix
//     it.
//   - kind='transfer': ErrPurposeCorrectionTransfer - a leg is half a
//     movement; correcting one alone breaks the pair. This is also what
//     makes a correction of a correction impossible, since a correction's
//     own two legs are themselves kind='transfer' - no depth limit or cycle
//     rule needed.
//   - kind='adjustment' with reverses_transaction_id set (a dues reversal,
//     ADR-029): ErrPurposeCorrectionDuesReversal - its own correction path
//     is ReverseDuesPayment, not this one.
//   - kind='normal', or kind='adjustment' with reverses_transaction_id NULL
//     (an ordinary correction or a ADR-024 reconciliation fix): eligible.
//
// Two more refusals run after eligibility, against the closed-incidental
// state ADR-031 guarantees (ADR-033):
//
//   - The target (PurposeID) names a closed incidental:
//     ErrPurposeCorrectionTargetClosed - postTransferPairTx checks nothing
//     by design, so this route would otherwise post straight past
//     PostTransaction's own ErrIncidentalClosed guard.
//   - The effective peruntukan names a closed incidental:
//     ErrPurposeCorrectionSourceClosed - leaving it non-zero would break
//     the zero-balance invariant #270's rollover derivation reads.
//
// And last, PurposeID equal to the effective peruntukan already:
// ErrPurposeCorrectionNoop - nothing would move.
func (l *Ledger) PostPurposeCorrection(ctx context.Context, p PostPurposeCorrectionParams) (store.Transfer, error) {
	var created store.Transfer
	err := l.withTx(ctx, func(q store.Querier) error {
		original, err := q.GetTransactionForFund(ctx, store.GetTransactionForFundParams{
			FundID: p.FundID,
			ID:     p.TransactionID,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrPurposeCorrectionNotFound
			}
			return fmt.Errorf("fetching transaction to correct: %w", err)
		}

		switch original.Kind {
		case "opening":
			return ErrPurposeCorrectionOpening
		case "dues":
			return ErrPurposeCorrectionDues
		case "reimbursement":
			return ErrPurposeCorrectionReimbursement
		case "transfer":
			return ErrPurposeCorrectionTransfer
		case "adjustment":
			if original.ReversesTransactionID != nil {
				return ErrPurposeCorrectionDuesReversal
			}
		}
		// "normal", and "adjustment" with no reverses_transaction_id, fall
		// through eligible - the last case the switch above leaves
		// unhandled on purpose.

		effectivePurposeID, err := effectivePeruntukan(ctx, q, p.FundID, original)
		if err != nil {
			return err
		}

		if p.PurposeID == effectivePurposeID {
			return ErrPurposeCorrectionNoop
		}

		if err := refuseClosedIncidental(ctx, q, p.PurposeID, ErrPurposeCorrectionTargetClosed); err != nil {
			return err
		}
		if err := refuseClosedIncidental(ctx, q, effectivePurposeID, ErrPurposeCorrectionSourceClosed); err != nil {
			return err
		}

		// The direction flip this method's own doc comment works out in
		// full: 'out' needs the effective purpose to receive the pair's
		// 'in' leg (undoing what it lost) and the target to receive the
		// 'out' leg (taking on what it should have carried); 'in' is the
		// mirror image.
		var from, to leg
		switch original.Direction {
		case "out":
			from = leg{AccountID: original.AccountID, PurposeID: p.PurposeID}
			to = leg{AccountID: original.AccountID, PurposeID: effectivePurposeID}
		default: // "in" - the schema's own CHECK admits no third value.
			from = leg{AccountID: original.AccountID, PurposeID: effectivePurposeID}
			to = leg{AccountID: original.AccountID, PurposeID: p.PurposeID}
		}

		transactionID := p.TransactionID
		created, err = l.postTransferPairTx(ctx, q, p.FundID, "reclass_purpose", from, to,
			money.FromDB(original.Amount), original.OccurredOn, nil, &transactionID)
		if err != nil {
			return fmt.Errorf("posting purpose correction: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.Transfer{}, fmt.Errorf("correcting purpose: %w", err)
	}
	return created, nil
}

// effectivePeruntukan is the row's current tag: the destination of the
// latest correction already pointing at it, or its own stored purpose_id
// when nothing has corrected it yet (ADR-033). See PostPurposeCorrection's
// own doc comment for why the stored tag alone is unsafe once a correction
// exists.
//
// Which of the latest correction's two legs IS that destination depends on
// original's own Direction, not on which leg is 'out' vs 'in':
// PostPurposeCorrection posted that correction with the target at the 'out'
// leg when original.Direction is "out" (undoing an expense's mis-tag needs
// the target to take on the -amount) and at the 'in' leg when it is "in" -
// the exact mapping this function's caller used to build the pair in the
// first place, applied again here to read it back.
func effectivePeruntukan(ctx context.Context, q store.Querier, fundID int64, original store.Transaction) (int64, error) {
	transactionID := original.ID
	latest, err := q.LatestPurposeCorrectionForTransaction(ctx, store.LatestPurposeCorrectionForTransactionParams{
		FundID:                fundID,
		CorrectsTransactionID: &transactionID,
	})
	if err == nil {
		if original.Direction == "out" {
			return latest.OutPurposeID, nil
		}
		return latest.InPurposeID, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return original.PurposeID, nil
	}
	return 0, fmt.Errorf("finding the latest purpose correction: %w", err)
}

// refuseClosedIncidental returns refusal when purposeID names a closed
// incidental, and nil otherwise - including when purposeID is not an
// incidental at all (main, pass_through), the same "no row, no guard" shape
// PostTransaction's own IncidentalClosedOnForPurpose check already uses.
// postTransferPairTx checks nothing by design (ADR-033), so this is what
// stands in front of it for both the target and the effective peruntukan.
func refuseClosedIncidental(ctx context.Context, q store.Querier, purposeID int64, refusal error) error {
	closedOn, err := q.IncidentalClosedOnForPurpose(ctx, purposeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("checking incidental closed state: %w", err)
	}
	if closedOn != nil {
		return refusal
	}
	return nil
}
