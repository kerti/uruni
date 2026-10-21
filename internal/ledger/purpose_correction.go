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
// There is no AccountID, Direction, Amount or OccurredOn field: all four are
// read off the row being corrected, as ReverseDuesPaymentParams does. A
// correction naming a different account or date would not correct that row,
// and ADR-033 is explicit that the date is not an input: dating it today
// would leave the month the mistake happened wrong in both tags.
type PostPurposeCorrectionParams struct {
	FundID        int64
	TransactionID int64 // the posted row whose peruntukan is wrong
	PurposeID     int64 // the target: what it should have been tagged
}

// PostPurposeCorrection posts a kind='reclass_purpose' pair that moves one
// posted row's attribution from its effective peruntukan to PurposeID,
// carrying the original row's account_id and occurred_on on both legs and
// corrects_transaction_id naming the row it corrects.
//
// It is built on postTransferPairTx (transfer.go), which
// CloseIncidentalAndRoll also reuses: no second write path (ADR-027's narrow
// PostTransaction, ADR-033).
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
// original attribution" undoes. effectivePeruntukan follows every prior
// correction pointing at TransactionID to where they end; with none, the
// stored purpose_id is accurate. A correction built from the stored tag would
// move money out of a tag that no longer holds it and drive it negative: the
// defect #266 reports, recreated by the tool meant to fix it (ADR-033).
//
// Eligibility, each a named error (ADR-033):
//
//   - kind='opening': ErrPurposeCorrectionOpening - Kas Utama by
//     construction, nothing to fix.
//   - kind='dues': ErrPurposeCorrectionDues - a different entry, not a
//     mis-tag.
//   - kind='reimbursement': ErrPurposeCorrectionReimbursement - its payout
//     inherits the claim's peruntukan; edit the claim.
//   - kind='transfer': ErrPurposeCorrectionTransfer - a leg is half a
//     movement; correcting one breaks the pair. Since a correction's legs are
//     kind='transfer', a correction of a correction is impossible, with no
//     depth limit or cycle rule.
//   - kind='adjustment' with reverses_transaction_id set (a dues reversal,
//     ADR-029): ErrPurposeCorrectionDuesReversal - use ReverseDuesPayment.
//   - kind='normal' with member_id set (a named contribution, ADR-034):
//     ErrPurposeCorrectionNamedContribution - moving it would leave
//     participation counting the member against an envelope the money has
//     left; reverse and post again, as for a dues payment.
//   - kind='normal' with no member_id, or kind='adjustment' with
//     reverses_transaction_id NULL (an ordinary correction or an ADR-024
//     reconciliation fix): eligible.
//
// Two more refusals follow eligibility, against the closed-incidental state
// ADR-031 guarantees (ADR-033):
//
//   - The target (PurposeID) names a closed incidental:
//     ErrPurposeCorrectionTargetClosed. postTransferPairTx checks nothing by
//     design, so this route would otherwise bypass PostTransaction's
//     ErrIncidentalClosed guard.
//   - The effective peruntukan names a closed incidental:
//     ErrPurposeCorrectionSourceClosed - leaving it non-zero would break the
//     zero-balance invariant #270's rollover derivation reads.
//
// Last, PurposeID equal to the effective peruntukan: ErrPurposeCorrectionNoop,
// since nothing would move.
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
		case "normal":
			// A named contribution (ADR-034): moving it would leave participation
			// counting the member against an envelope the money has left. Refused like a
			// dues payment; an unnamed contribution (member_id NULL) is eligible.
			if original.MemberID != nil {
				return ErrPurposeCorrectionNamedContribution
			}
		}
		// "normal" with no member_id, and "adjustment" with no
		// reverses_transaction_id, are eligible: the cases the switch leaves
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

		// The direction flip worked out in the doc comment: 'out' needs the
		// effective purpose to receive the pair's 'in' leg and the target the 'out'
		// leg; 'in' is the mirror image.
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
			money.FromDB(original.Amount), original.OccurredOn, nil, &transactionID, nil)
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

// effectivePeruntukan is the row's current tag (ADR-033): its stored
// purpose_id until a correction moves it, then the end of the path its
// corrections walk. EffectivePurposeForTransaction reads it from
// ledger_entry.effective_purpose_id, which derives that end without ordering
// the corrections (#411, #628; the definition is in 00001_schema.sql). See
// PostPurposeCorrection for why the stored tag alone is unsafe once a
// correction exists.
func effectivePeruntukan(ctx context.Context, q store.Querier, fundID int64, original store.Transaction) (int64, error) {
	id, err := q.EffectivePurposeForTransaction(ctx, store.EffectivePurposeForTransactionParams{
		FundID: fundID, ID: original.ID,
	})
	if err != nil {
		return 0, fmt.Errorf("finding the effective purpose: %w", err)
	}
	return id, nil
}

// refuseClosedIncidental returns refusal when purposeID names a closed
// incidental, and nil otherwise, including when purposeID is not an
// incidental (main, pass_through): "no row, no guard", as PostTransaction's
// IncidentalClosedOnForPurpose check. postTransferPairTx checks nothing
// (ADR-033), so this stands in front of it for the target and the effective
// peruntukan.
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
