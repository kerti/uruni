package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// One opening balance posts one kind='opening' row, and FundBalance and
// AccountBalance include it exactly like any other entry.
func TestPostOpeningBalancePostsOneRowIncludedInBalances(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 100_000, OccurredOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("PostOpeningBalance() = %v, want no error", err)
	}
	if posted.ID == 0 {
		t.Error("PostOpeningBalance() returned a zero id")
	}
	if posted.Kind != "opening" {
		t.Errorf("Kind = %q, want %q", posted.Kind, "opening")
	}
	if posted.Direction != "in" {
		t.Errorf("Direction = %q, want %q", posted.Direction, "in")
	}
	if posted.Amount != 100_000 {
		t.Errorf("Amount = %d, want %d", posted.Amount, 100_000)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ledger holds %d rows, want exactly 1", len(rows))
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 100_000 {
		t.Errorf("FundBalance() = %d, want %d", fundBal, 100_000)
	}

	acctBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if acctBal != 100_000 {
		t.Errorf("AccountBalance() = %d, want %d", acctBal, 100_000)
	}
}

// A second call for the same account is refused with the named sentinel and
// posts nothing - proven by asserting the row count, not only the error.
func TestPostOpeningBalanceSecondCallForSameAccountIsRefused(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 100_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("first PostOpeningBalance() = %v, want no error", err)
	}

	_, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 50_000, OccurredOn: "2026-08-13",
	})
	if !errors.Is(err, ErrOpeningBalanceExists) {
		t.Fatalf("second PostOpeningBalance() = %v, want an error wrapping ErrOpeningBalanceExists", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Errorf("ledger holds %d rows after a refused second post, want exactly 1", len(rows))
	}
}

// The schema's opening_balance_once_per_account index is the actual
// guarantee, independent of the domain's pre-check: inserting a second
// kind='opening' row directly through raw store.Queries, bypassing
// PostOpeningBalance entirely, must still be refused. This is the test that
// would catch the pre-check being removed.
func TestOpeningBalanceIndexRefusesASecondRowInsertedDirectly(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-12",
		Kind: "opening", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("first CreateTransaction() = %v, want no error", err)
	}

	_, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-13",
		Kind: "opening", CreatedAt: 2,
	})
	if err == nil {
		t.Fatal("second CreateTransaction(kind='opening') for the same account = nil error, want a unique constraint violation")
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Errorf("ledger holds %d rows after a refused direct insert, want exactly 1", len(rows))
	}
}

// Two different accounts in the same fund may each have their own opening
// balance.
func TestPostOpeningBalanceTwoAccountsInSameFund(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 100_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostOpeningBalance(cash) = %v, want no error", err)
	}

	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.bankID, PurposeID: f.mainID,
		Amount: 250_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostOpeningBalance(bank) = %v, want no error", err)
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 350_000 {
		t.Errorf("FundBalance() = %d, want %d", fundBal, 350_000)
	}

	cashBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance(cash) = %v, want no error", err)
	}
	if cashBal != 100_000 {
		t.Errorf("AccountBalance(cash) = %d, want %d", cashBal, 100_000)
	}

	bankBal, err := l.AccountBalance(ctx, f.fundID, f.bankID)
	if err != nil {
		t.Fatalf("AccountBalance(bank) = %v, want no error", err)
	}
	if bankBal != 250_000 {
		t.Errorf("AccountBalance(bank) = %d, want %d", bankBal, 250_000)
	}
}

// A zero opening balance posts no row and returns no error - the account
// already derives to 0 by summing an empty set, so a zero row would carry no
// information the ledger lacks (see the issue #51 comment correcting the
// original acceptance criterion).
func TestPostOpeningBalanceZeroPostsNoRowNoError(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posted, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 0, OccurredOn: "2026-08-12",
	})
	if err != nil {
		t.Fatalf("PostOpeningBalance(0) = %v, want no error", err)
	}
	if posted.ID != 0 {
		t.Errorf("PostOpeningBalance(0) = %+v, want a zero-value Transaction (no row posted)", posted)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a zero opening balance, want 0", len(rows))
	}

	acctBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if acctBal != 0 {
		t.Errorf("AccountBalance() = %d, want 0", acctBal)
	}

	// A zero opening balance does not consume the account's one-opening slot:
	// a genuine later opening balance may still be posted.
	if _, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: 10_000, OccurredOn: "2026-08-13",
	}); err != nil {
		t.Fatalf("PostOpeningBalance() after a zero call = %v, want no error", err)
	}
}

// A negative amount is rejected before the write, exactly like
// PostTransaction's non-positive check - proven by asserting nothing was
// inserted, not only that an error came back.
func TestPostOpeningBalanceRejectsNegativeAmountBeforeTheWrite(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	_, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Amount: -1, OccurredOn: "2026-08-12",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PostOpeningBalance(-1) = %v, want an error wrapping ErrInvalidArgument", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a rejected post, want 0 - the CHECK should never have been reached", len(rows))
	}
}

// A malformed occurred_on is rejected before the write, reusing
// validateOccurredOn rather than a second date validator.
func TestPostOpeningBalanceRejectsInvalidOccurredOn(t *testing.T) {
	tests := []struct {
		name       string
		occurredOn string
	}{
		{"malformed", "12 August 2026"},
		{"calendar-invalid", "2026-02-30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLedger(t)
			f := newFixture(t, l)
			ctx := context.Background()

			_, err := l.PostOpeningBalance(ctx, PostOpeningBalanceParams{
				FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
				Amount: 100_000, OccurredOn: tt.occurredOn,
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("PostOpeningBalance() = %v, want an error wrapping ErrInvalidArgument", err)
			}

			rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
			}
			if len(rows) != 0 {
				t.Errorf("ledger holds %d rows after a rejected post, want 0", len(rows))
			}
		})
	}
}
