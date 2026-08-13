package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
		claim, err := q.GetReimbursement(ctx, p.ReimbursementID)
		if err != nil {
			return fmt.Errorf("fetching reimbursement: %w", err)
		}

		if claim.WaivedOn != nil {
			return ErrReimbursementWaived
		}

		_, err = q.GetReimbursementSettlement(ctx, store.GetReimbursementSettlementParams{
			FundID:          p.FundID,
			ReimbursementID: &p.ReimbursementID,
		})
		if err == nil {
			return ErrReimbursementAlreadySettled
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("checking for an existing settlement: %w", err)
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
