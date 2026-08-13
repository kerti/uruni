package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// The property the whole package exists to prove: a transfer moves money, it
// does not create or destroy it. Mirrors internal/db's
// TestTransferKindRequiresATransfer, asserted here at the domain layer.
func TestPostTransferBetweenAccountsLeavesFundBalanceUnchanged(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	before, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() before = %v, want no error", err)
	}

	if _, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID,
		FromAccountID: f.cashID, ToAccountID: f.bankID,
		Amount: 40_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
	}

	after, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after = %v, want no error", err)
	}

	if before != after {
		t.Errorf("FundBalance() before=%d after=%d, want identical - a transfer moves money, it does not create or destroy it", before, after)
	}
	if after != 100_000 {
		t.Errorf("FundBalance() = %d, want 100000", after)
	}
}

// AccountBalance moves by exactly the amount on both sides, in opposite
// directions. PurposeBalance, in contrast, does not move at all - the
// purpose did not change, only where the money sits.
func TestPostTransferBetweenAccountsMovesBothAccountBalancesOppositely(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	purposeBefore, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance() before = %v, want no error", err)
	}

	if _, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID,
		FromAccountID: f.cashID, ToAccountID: f.bankID,
		Amount: 40_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
	}

	cashBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance(cash) = %v, want no error", err)
	}
	if cashBal != 60_000 {
		t.Errorf("AccountBalance(cash) = %d, want 60000", cashBal)
	}

	bankBal, err := l.AccountBalance(ctx, f.fundID, f.bankID)
	if err != nil {
		t.Fatalf("AccountBalance(bank) = %v, want no error", err)
	}
	if bankBal != 40_000 {
		t.Errorf("AccountBalance(bank) = %d, want 40000", bankBal)
	}

	purposeAfter, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance() after = %v, want no error", err)
	}
	if purposeAfter != purposeBefore {
		t.Errorf("PurposeBalance() before=%d after=%d, want identical - between_accounts does not move the purpose", purposeBefore, purposeAfter)
	}
}

// The transfer row exists and both legs reference it; neither leg exists
// without it.
func TestPostTransferBetweenAccountsWritesOneTransferRowAndBothLegsReferenceIt(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	transfer, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID,
		FromAccountID: f.cashID, ToAccountID: f.bankID,
		Amount: 40_000, OccurredOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
	}
	if transfer.ID == 0 {
		t.Fatal("PostTransferBetweenAccounts() returned a zero transfer id")
	}
	if transfer.Kind != "between_accounts" {
		t.Errorf("transfer.Kind = %q, want %q", transfer.Kind, "between_accounts")
	}

	transfers, err := q.ListTransfersByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransfersByFund() = %v, want no error", err)
	}
	if len(transfers) != 1 {
		t.Fatalf("ListTransfersByFund() returned %d rows, want 1", len(transfers))
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListTransactionsByFund() returned %d rows, want 2 legs", len(rows))
	}

	seenDirections := map[string]bool{}
	for _, row := range rows {
		if row.Kind != "transfer" {
			t.Errorf("leg.Kind = %q, want %q", row.Kind, "transfer")
		}
		if row.TransferID == nil || *row.TransferID != transfer.ID {
			t.Errorf("leg.TransferID = %v, want %d", row.TransferID, transfer.ID)
		}
		if row.Amount != 40_000 {
			t.Errorf("leg.Amount = %d, want 40000", row.Amount)
		}
		seenDirections[row.Direction] = true
	}
	if !seenDirections["in"] || !seenDirections["out"] {
		t.Errorf("leg directions = %v, want both \"in\" and \"out\"", seenDirections)
	}
}

// Atomicity: a pair whose second leg violates the schema leaves zero rows
// behind, not one. A second, identical call to the same accounts with the
// same purpose is legal on its own, so this forces the violation with a
// destination account that belongs to another fund entirely - the composite
// FK the schema enforces.
func TestPostTransferBetweenAccountsLeavesNoRowsWhenTheSecondLegFails(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	other, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	otherAccount := createAccount(t, q, other.ID, "bank", "Other Fund's Bank")

	_, err = l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID,
		FromAccountID: f.cashID, ToAccountID: otherAccount,
		Amount: 40_000, OccurredOn: "2026-08-12",
	})
	if err == nil {
		t.Fatal("PostTransferBetweenAccounts() across funds = nil error, want a foreign key violation")
	}

	transfers, err := q.ListTransfersByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransfersByFund() = %v, want no error", err)
	}
	if len(transfers) != 0 {
		t.Errorf("ledger holds %d transfer rows after a failed pair, want 0", len(transfers))
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d transaction rows after a failed pair, want 0 - the first leg must have rolled back too", len(rows))
	}
}

func TestPostTransferBetweenAccountsRejectsNonPositiveAmountBeforeTheWrite(t *testing.T) {
	tests := []struct {
		name   string
		amount money.Amount
	}{
		{"zero", 0},
		{"negative", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			_, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
				FundID: f.fundID, PurposeID: f.mainID,
				FromAccountID: f.cashID, ToAccountID: f.bankID,
				Amount: tt.amount, OccurredOn: "2026-08-12",
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("PostTransferBetweenAccounts() = %v, want an error wrapping ErrInvalidArgument", err)
			}

			rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
			}
			if len(rows) != 0 {
				t.Errorf("ledger holds %d rows after a rejected transfer, want 0", len(rows))
			}
		})
	}
}

func TestPostTransferBetweenAccountsRejectsInvalidOccurredOn(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID,
		FromAccountID: f.cashID, ToAccountID: f.bankID,
		Amount: 40_000, OccurredOn: "2026-02-30",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want an error wrapping ErrInvalidArgument", err)
	}
}

// postTransferPair is the shared primitive #42's incidental roll will also
// call, with the legs' shape flipped: same account, different purpose,
// kind='reclass_purpose'. This calls it directly - there is no exported
// entry point for reclass_purpose yet - to prove the primitive already fits
// that caller without a second code path, per ADR-027.
func TestPostTransferPairSupportsTheReclassPurposeShape(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.incidenID,
		Direction: "in", Amount: 60_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}

	before, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() before = %v, want no error", err)
	}

	from := leg{AccountID: f.cashID, PurposeID: f.incidenID}
	to := leg{AccountID: f.cashID, PurposeID: f.mainID}
	transfer, err := l.postTransferPair(ctx, f.fundID, "reclass_purpose", from, to, 60_000, "2026-08-12")
	if err != nil {
		t.Fatalf("postTransferPair() = %v, want no error", err)
	}
	if transfer.Kind != "reclass_purpose" {
		t.Errorf("transfer.Kind = %q, want %q", transfer.Kind, "reclass_purpose")
	}

	after, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() after = %v, want no error", err)
	}
	if before != after {
		t.Errorf("FundBalance() before=%d after=%d, want identical", before, after)
	}

	// The account did not change, so AccountBalance is untouched...
	acctBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if acctBal != 60_000 {
		t.Errorf("AccountBalance() = %d, want 60000 - reclass_purpose does not move the account", acctBal)
	}

	// ...but the leftover moved from the incidental purpose to main.
	incidenBal, err := l.PurposeBalance(ctx, f.fundID, f.incidenID)
	if err != nil {
		t.Fatalf("PurposeBalance(incidental) = %v, want no error", err)
	}
	if incidenBal != 0 {
		t.Errorf("PurposeBalance(incidental) = %d, want 0 - the leftover rolled out", incidenBal)
	}
	mainBal, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) = %v, want no error", err)
	}
	if mainBal != 60_000 {
		t.Errorf("PurposeBalance(main) = %d, want 60000 - the leftover rolled into the main purpose", mainBal)
	}
}

// Two identical legs - here, the same account on both sides of the same
// purpose - would satisfy every schema CHECK while meaning nothing: a
// same-account, same-purpose pair that moves no money and nets to zero
// twice over. It is rejected before the write.
func TestPostTransferBetweenAccountsRejectsIdenticalLegs(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID,
		FromAccountID: f.cashID, ToAccountID: f.cashID,
		Amount: 40_000, OccurredOn: "2026-08-12",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a rejected no-op transfer, want 0", len(rows))
	}
}
