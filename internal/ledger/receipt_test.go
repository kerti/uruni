package ledger

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

func receiptCount(t *testing.T, l *Ledger) int {
	t.Helper()
	var n int
	if err := l.db.QueryRowContext(context.Background(), `SELECT count(*) FROM receipt`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateReceiptAttachesToExactlyOneParent(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t).at(augustNow)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	txn := postNormalOn(t, l, f, f.cashID)
	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)

	onTxn, err := l.CreateReceipt(ctx, CreateReceiptParams{FundID: f.fundID, TransactionID: &txn.ID, Path: "a.jpg"})
	if err != nil {
		t.Fatalf("CreateReceipt(transaction) = %v, want no error", err)
	}
	if onTxn.TransactionID == nil || *onTxn.TransactionID != txn.ID || onTxn.Path != "a.jpg" || onTxn.UploadedAt != augustNow.Unix() {
		t.Errorf("CreateReceipt(transaction) = %+v, want it on transaction %d, a.jpg, at %d", onTxn, txn.ID, augustNow.Unix())
	}
	onClaim, err := l.CreateReceipt(ctx, CreateReceiptParams{FundID: f.fundID, ReimbursementID: &claim.ID, Path: "b.jpg"})
	if err != nil {
		t.Fatalf("CreateReceipt(claim) = %v, want no error", err)
	}
	if onClaim.ReimbursementID == nil || *onClaim.ReimbursementID != claim.ID {
		t.Errorf("CreateReceipt(claim) = %+v, want it on claim %d", onClaim, claim.ID)
	}
	if got, ok := ledgerEntryCount(t, l, f.fundID); !ok || got != 1 {
		t.Errorf("ledger entries = %d, want 1: a receipt is not a ledger fact", got)
	}
}

func TestCreateReceiptRefusalsWriteNothing(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	txn := postNormalOn(t, l, f, f.cashID)
	claim := createReimbursement(t, q, f, 75_000, "2026-08-01", nil)
	missing := txn.ID + 999
	other := otherFund(t, q)

	tests := []struct {
		name   string
		params CreateReceiptParams
		want   *Error
	}{
		{"no parent", CreateReceiptParams{FundID: f.fundID, Path: "a.jpg"}, ErrDataConstraint},
		{"both parents", CreateReceiptParams{FundID: f.fundID, TransactionID: &txn.ID, ReimbursementID: &claim.ID, Path: "a.jpg"}, ErrDataConstraint},
		{"a blank path", CreateReceiptParams{FundID: f.fundID, TransactionID: &txn.ID, Path: " "}, ErrDataConstraint},
		{"a transaction that does not exist", CreateReceiptParams{FundID: f.fundID, TransactionID: &missing, Path: "a.jpg"}, ErrReferenceMissing},
		{"a claim that does not exist", CreateReceiptParams{FundID: f.fundID, ReimbursementID: &missing, Path: "a.jpg"}, ErrReferenceMissing},
		{"another fund's transaction", CreateReceiptParams{FundID: other.ID, TransactionID: &txn.ID, Path: "a.jpg"}, ErrReferenceMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := l.CreateReceipt(ctx, tc.params); !errors.Is(err, tc.want) {
				t.Errorf("CreateReceipt() = %v, want %v", err, tc.want)
			}
			if n := receiptCount(t, l); n != 0 {
				t.Errorf("receipts = %d, want 0: a refused create writes nothing", n)
			}
		})
	}
}

func TestDeleteReceipt(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	ctx := context.Background()
	txn := postNormalOn(t, l, f, f.cashID)
	receipt, err := l.CreateReceipt(ctx, CreateReceiptParams{FundID: f.fundID, TransactionID: &txn.ID, Path: "a.jpg"})
	if err != nil {
		t.Fatal(err)
	}

	other := otherFund(t, q)
	if err := l.DeleteReceipt(ctx, DeleteReceiptParams{FundID: other.ID, ReceiptID: receipt.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeleteReceipt(another fund's receipt) = %v, want sql.ErrNoRows", err)
	}
	if n := receiptCount(t, l); n != 1 {
		t.Fatalf("receipts = %d, want 1: another fund's delete removes nothing", n)
	}

	if err := l.DeleteReceipt(ctx, DeleteReceiptParams{FundID: f.fundID, ReceiptID: receipt.ID}); err != nil {
		t.Fatalf("DeleteReceipt() = %v, want no error", err)
	}
	if n := receiptCount(t, l); n != 0 {
		t.Errorf("receipts = %d, want 0", n)
	}
	// The ledger row it described is untouched.
	if _, err := q.GetTransactionForFund(ctx, store.GetTransactionForFundParams{FundID: f.fundID, ID: txn.ID}); err != nil {
		t.Errorf("the transaction after its receipt was deleted = %v, want it still there", err)
	}
	if err := l.DeleteReceipt(ctx, DeleteReceiptParams{FundID: f.fundID, ReceiptID: receipt.ID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeleteReceipt(already gone) = %v, want sql.ErrNoRows", err)
	}
}

// ledgerEntryCount counts the fund's transaction rows.
func ledgerEntryCount(t *testing.T, l *Ledger, fundID int64) (int, bool) {
	t.Helper()
	var n int
	if err := l.db.QueryRowContext(context.Background(), `SELECT count(*) FROM "transaction" WHERE fund_id = ?`, fundID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n, true
}
