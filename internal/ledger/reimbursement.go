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

// SettleReimbursementParams is every argument SettleReimbursement needs to pay
// out one outstanding claim.
//
// There is deliberately no Amount and no PurposeID field: both come from the
// reimbursement row itself once fetched, never from the caller. A settlement
// that paid a different figure than the claim would not be a settlement of
// that claim, so the amount is not this struct's to carry.
type SettleReimbursementParams struct {
	FundID          int64
	ReimbursementID int64
	AccountID       int64  // which account pays the claim out
	OccurredOn      string // "YYYY-MM-DD", a real calendar date - the settle date, not incurred_on
}

// SettleReimbursement writes one kind='reimbursement', direction='out' row
// paying out a claim that has been sitting off the ledger, and returns the
// created row.
//
// A member fronting their own money does not move the kas, so there is no
// ledger row until this call - that is why reimbursement is its own table and
// why the recorded balance still matches the wallet in between (ADR-024).
// This method posts exactly one row: the payout, dated on the settle date
// (OccurredOn), not on incurred_on, which stays on the reimbursement row as
// the truth about when the member actually spent the money.
//
// Two pre-checks run inside the transaction before the write, each producing
// a named error rather than closing a race that cannot happen: under
// ADR-004's SetMaxOpenConns(1), no second connection can interleave a write
// between either check and this method's own insert, so neither check is a
// lock and neither closes a window the schema does not already close on its
// own (mirroring PostOpeningBalance's identical comment for the
// opening-balance-once check).
//
//   - If the claim has WaivedOn set, ErrReimbursementWaived: a claim that will
//     never be repaid cannot be settled.
//   - If GetReimbursementSettlement already finds a kind='reimbursement' row
//     for this claim, ErrReimbursementAlreadySettled. The
//     reimbursement_settled_once partial unique index is the actual
//     guarantee - it refuses a second settling row even for a caller that
//     bypasses this method entirely and inserts through raw store.Queries.
//     This pre-check exists only to turn that into a clean, named error
//     instead of a raw "UNIQUE constraint failed" string.
func (l *Ledger) SettleReimbursement(ctx context.Context, p SettleReimbursementParams) (store.Transaction, error) {
	if err := validateOccurredOn(p.OccurredOn); err != nil {
		return store.Transaction{}, err
	}

	var posted store.Transaction
	err := l.withTx(ctx, func(q store.Querier) error {
		claim, err := unsettledClaim(ctx, q, p.FundID, p.ReimbursementID)
		if err != nil {
			return err
		}

		if claim.WaivedOn != nil {
			return ErrReimbursementWaived
		}

		posted, err = q.CreateTransaction(ctx, store.CreateTransactionParams{
			FundID:          p.FundID,
			AccountID:       p.AccountID,
			PurposeID:       claim.PurposeID,
			Direction:       "out",
			Amount:          claim.Amount,
			OccurredOn:      p.OccurredOn,
			Kind:            "reimbursement",
			ReimbursementID: &p.ReimbursementID,
			CreatedAt:       time.Now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Transaction{}, fmt.Errorf("settling reimbursement: %w", err)
	}
	return posted, nil
}

// UpdateReimbursementParams is every argument UpdateReimbursement needs.
// Each optional field is a pointer meaning "leave alone" when nil, except
// the two nullable columns, where nil is ambiguous between "leave alone"
// and "clear it" - so those carry a Set flag, the same split
// store.UpdateReimbursementParams uses and PATCH /api/members already
// speaks.
type UpdateReimbursementParams struct {
	FundID          int64
	ReimbursementID int64

	MemberID   *int64
	PurposeID  *int64
	Amount     *money.Amount // must be > 0 when set
	IncurredOn *string       // "YYYY-MM-DD", a real calendar date, when set

	Note    *string
	SetNote bool

	WaivedOn    *string // "YYYY-MM-DD" to waive, nil with SetWaivedOn to un-waive
	SetWaivedOn bool
}

// UpdateReimbursement corrects a claim that has not been settled yet, and is
// also how a claim is waived - waiving sets one column, so it rides on the
// ordinary correction rather than a method of its own, which is what makes
// un-waiving (SetWaivedOn with a nil WaivedOn) free.
//
// This is an UPDATE rather than an adjusting entry, and that is not a hole
// in CLAUDE.md rule 3: an unsettled claim is off the ledger, which is the
// whole point of the reimbursement table (ADR-024). The schema agrees -
// "transaction", transfer, reconciliation and reconciliation_line carry
// immutability triggers and reimbursement deliberately does not.
//
// Settlement is the boundary. Settling copies the claim's amount and
// purpose_id onto a transaction that is immutable forever, so a correction
// afterwards would let the claim silently disagree with the row that paid
// it - two records that both look authoritative. Once settled, the only
// correction is an ordinary adjusting entry on the ledger, and this method
// returns ErrReimbursementAlreadySettled.
//
// The check and the write share one transaction for the same reason
// SettleReimbursement's do: it is not a lock (ADR-004's SetMaxOpenConns(1)
// makes an interleaved write structurally impossible) but a single,
// coherent read-then-decide.
func (l *Ledger) UpdateReimbursement(ctx context.Context, p UpdateReimbursementParams) (store.Reimbursement, error) {
	if p.Amount != nil && *p.Amount <= 0 {
		return store.Reimbursement{}, fmt.Errorf("%w: amount must be positive, got %d", ErrInvalidArgument, p.Amount.Int64())
	}
	if p.IncurredOn != nil {
		if err := validateOccurredOn(*p.IncurredOn); err != nil {
			return store.Reimbursement{}, err
		}
	}
	if p.WaivedOn != nil {
		if err := validateOccurredOn(*p.WaivedOn); err != nil {
			return store.Reimbursement{}, err
		}
	}

	var updated store.Reimbursement
	err := l.withTx(ctx, func(q store.Querier) error {
		if _, err := unsettledClaim(ctx, q, p.FundID, p.ReimbursementID); err != nil {
			return err
		}

		args := store.UpdateReimbursementParams{
			ID:         p.ReimbursementID,
			MemberID:   p.MemberID,
			PurposeID:  p.PurposeID,
			IncurredOn: p.IncurredOn,
		}
		if p.Amount != nil {
			amount := p.Amount.Int64()
			args.Amount = &amount
		}
		if p.SetNote {
			args.SetNote = 1
			args.Note = p.Note
		}
		if p.SetWaivedOn {
			args.SetWaivedOn = 1
			args.WaivedOn = p.WaivedOn
		}

		var err error
		updated, err = q.UpdateReimbursement(ctx, args)
		return err
	})
	if err != nil {
		return store.Reimbursement{}, fmt.Errorf("updating reimbursement: %w", err)
	}
	return updated, nil
}

// DeleteReimbursement removes a claim that should never have existed - the
// treasurer's own typo, not a debt anyone forgave. A claim the member
// actually waived is waived, not deleted: that one happened, and the record
// should keep saying so.
//
// Settled claims are refused for the same reason UpdateReimbursement
// refuses them, and harder: the payout row references this claim's id and
// can never be deleted itself, so removing the claim would leave the ledger
// pointing at nothing.
func (l *Ledger) DeleteReimbursement(ctx context.Context, fundID, reimbursementID int64) error {
	err := l.withTx(ctx, func(q store.Querier) error {
		if _, err := unsettledClaim(ctx, q, fundID, reimbursementID); err != nil {
			return err
		}
		return q.DeleteReimbursement(ctx, reimbursementID)
	})
	if err != nil {
		return fmt.Errorf("deleting reimbursement: %w", err)
	}
	return nil
}

// unsettledClaim fetches the claim and returns it only if no payout row
// references it yet, and ErrReimbursementAlreadySettled otherwise. It is the
// one place the "mutable only until settled" rule is written - settle,
// update and delete all go through it, so the three cannot drift apart.
//
// A claim that does not exist is sql.ErrNoRows from GetReimbursement,
// wrapped and left to the caller's mapper, which is the shape
// SettleReimbursement has always produced for an unknown id.
//
// The settled check is not a lock. Under ADR-004's SetMaxOpenConns(1) no
// second connection can interleave a write between this read and the
// caller's own, and the reimbursement_settled_once partial index remains
// the actual guarantee for settling; this exists to name the case.
func unsettledClaim(ctx context.Context, q store.Querier, fundID, reimbursementID int64) (store.Reimbursement, error) {
	claim, err := q.GetReimbursement(ctx, store.GetReimbursementParams{
		ID: reimbursementID, FundID: fundID,
	})
	if err != nil {
		return store.Reimbursement{}, fmt.Errorf("fetching reimbursement: %w", err)
	}

	_, err = q.GetReimbursementSettlement(ctx, store.GetReimbursementSettlementParams{
		FundID:          fundID,
		ReimbursementID: &reimbursementID,
	})
	if err == nil {
		return store.Reimbursement{}, ErrReimbursementAlreadySettled
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return store.Reimbursement{}, fmt.Errorf("checking for an existing settlement: %w", err)
	}
	return claim, nil
}
