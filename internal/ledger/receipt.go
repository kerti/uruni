package ledger

import (
	"context"
	"fmt"

	"github.com/kerti/uruni/internal/store"
)

// A receipt row names a photo already on disk (ADR-011). The ledger owns the
// row and never the file: writing, and removing, the file under the uploads
// volume is the caller's, and the two are not one transaction.

// CreateReceiptParams is every argument CreateReceipt needs.
type CreateReceiptParams struct {
	FundID int64
	// TransactionID or ReimbursementID names the parent: exactly one is non-nil.
	// Neither, or both, is ErrDataConstraint (the schema's CHECK); a parent that
	// does not exist in this fund is ErrReferenceMissing.
	TransactionID   *int64
	ReimbursementID *int64
	// Path is the file's server-generated name, relative to the uploads volume.
	Path string
}

// CreateReceipt records that a photo is attached to a posted transaction or a
// claim. The parent row does not change: a receipt lives in its own table so a
// photo can be added after the fact without touching an immutable ledger row.
func (l *Ledger) CreateReceipt(ctx context.Context, p CreateReceiptParams) (store.Receipt, error) {
	var receipt store.Receipt
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		receipt, err = q.CreateReceipt(ctx, store.CreateReceiptParams{
			FundID: p.FundID, TransactionID: p.TransactionID, ReimbursementID: p.ReimbursementID,
			Path: p.Path, UploadedAt: l.now().Unix(),
		})
		return err
	})
	if err != nil {
		return store.Receipt{}, fmt.Errorf("creating receipt: %w", refuseWrite(err))
	}
	return receipt, nil
}

// DeleteReceiptParams is every argument DeleteReceipt needs.
type DeleteReceiptParams struct {
	FundID    int64
	ReceiptID int64
}

// DeleteReceipt removes a wrong or duplicate photo's row; a receipt row is
// deletable, unlike a ledger row (ADR-011). The file stays on disk: the caller
// removes it after this returns, best effort. A receipt of another fund is
// sql.ErrNoRows.
func (l *Ledger) DeleteReceipt(ctx context.Context, p DeleteReceiptParams) error {
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		if _, err := q.GetReceiptForFund(ctx, store.GetReceiptForFundParams{ID: p.ReceiptID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching receipt: %w", err)
		}
		return q.DeleteReceipt(ctx, p.ReceiptID)
	})
	if err != nil {
		return fmt.Errorf("deleting receipt: %w", refuseDelete(err))
	}
	return nil
}
