package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// #230's uniform rule: a location and its opening balance are born together,
// in one database transaction, or not at all - everywhere in the app. There
// is no longer any standalone way to post an opening balance, so this file
// tests the two paths that now carry one: Ledger.CreateAccount (a location
// added after setup) and SetUpFund (the accounts a fund starts with).
// TestOpeningBalanceIndexRefusesASecondRowInsertedDirectly is the one
// survivor from before #230: it proves the schema's own guarantee, which
// neither entry point's pre-check logic touches.

// CreateAccount, given an opening balance, posts one kind='opening',
// direction='in' row tagged to the fund's main purpose, against the account
// it just created - and FundBalance/AccountBalance include it exactly like
// any other entry.
func TestCreateAccountPostsOpeningBalanceRowIncludedInBalances(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	account, err := l.CreateAccount(ctx, CreateAccountParams{
		FundID: f.fundID, Kind: "cash", Name: "Kas RT 05",
		OpeningBalance: &OpeningBalance{Amount: 100_000, OccurredOn: "2026-08-12"},
	})
	if err != nil {
		t.Fatalf("CreateAccount() = %v, want no error", err)
	}
	if account.ID == 0 {
		t.Fatal("CreateAccount() returned a zero account id")
	}
	if account.Kind != "cash" || account.Name != "Kas RT 05" {
		t.Errorf("account = %+v, want kind=cash name=Kas RT 05", account)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ledger holds %d rows, want exactly 1", len(rows))
	}
	row := rows[0]
	if row.Kind != "opening" {
		t.Errorf("Kind = %q, want %q", row.Kind, "opening")
	}
	if row.Direction != "in" {
		t.Errorf("Direction = %q, want %q", row.Direction, "in")
	}
	if row.AccountID != account.ID {
		t.Errorf("AccountID = %d, want the account just created (%d)", row.AccountID, account.ID)
	}
	if row.PurposeID != f.mainID {
		t.Errorf("PurposeID = %d, want the fund's main purpose (%d)", row.PurposeID, f.mainID)
	}
	if row.Amount != 100_000 {
		t.Errorf("Amount = %d, want %d", row.Amount, 100_000)
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 100_000 {
		t.Errorf("FundBalance() = %d, want %d", fundBal, 100_000)
	}

	acctBal, err := l.AccountBalance(ctx, f.fundID, account.ID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if acctBal != 100_000 {
		t.Errorf("AccountBalance() = %d, want %d", acctBal, 100_000)
	}
}

// A zero-amount opening balance still creates the account, but posts no
// row - the same no-op rule PostOpeningBalance used to carry, now checked at
// CreateAccount's own boundary.
func TestCreateAccountZeroOpeningBalancePostsAccountOnlyNoRow(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	account, err := l.CreateAccount(ctx, CreateAccountParams{
		FundID: f.fundID, Kind: "bank", Name: "BCA",
		OpeningBalance: &OpeningBalance{Amount: 0, OccurredOn: "2026-08-12"},
	})
	if err != nil {
		t.Fatalf("CreateAccount() = %v, want no error", err)
	}
	if account.ID == 0 {
		t.Fatal("CreateAccount() returned a zero account id")
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a zero opening balance, want 0", len(rows))
	}

	acctBal, err := l.AccountBalance(ctx, f.fundID, account.ID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if acctBal != 0 {
		t.Errorf("AccountBalance() = %d, want 0", acctBal)
	}
}

// No OpeningBalance at all behaves exactly like an explicit zero one -
// absent and zero are the same thing (setup.go's own AccountInput doc
// comment states this; CreateAccountParams shares it).
func TestCreateAccountNilOpeningBalancePostsAccountOnlyNoRow(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	account, err := l.CreateAccount(ctx, CreateAccountParams{FundID: f.fundID, Kind: "cash", Name: "Tunai Dua"})
	if err != nil {
		t.Fatalf("CreateAccount() = %v, want no error", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows with no OpeningBalance given, want 0", len(rows))
	}
	if account.ID == 0 {
		t.Error("CreateAccount() returned a zero account id")
	}
}

// A negative opening balance is refused before withTx opens, and leaves no
// account behind either - the whole point of folding the two writes into one
// transaction: a refused balance must not strand a location with nothing to
// say about what's in it.
func TestCreateAccountRejectsNegativeOpeningBalanceLeavesNoAccountBehind(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	q := store.New(l.db)

	before, err := q.ListAccountsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListAccountsByFund() = %v, want no error", err)
	}

	_, err = l.CreateAccount(ctx, CreateAccountParams{
		FundID: f.fundID, Kind: "cash", Name: "Tunai Baru",
		OpeningBalance: &OpeningBalance{Amount: -1, OccurredOn: "2026-08-12"},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CreateAccount() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	after, err := q.ListAccountsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListAccountsByFund() = %v, want no error", err)
	}
	if len(after) != len(before) {
		t.Errorf("ListAccountsByFund() = %d accounts after a refused create, want %d (unchanged)", len(after), len(before))
	}

	rows, err := q.ListTransactionsByFund(ctx, f.fundID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 0 {
		t.Errorf("ledger holds %d rows after a refused create, want 0", len(rows))
	}
}

// A malformed or calendar-invalid occurred_on is refused the same way, and
// leaves no account behind either.
func TestCreateAccountRejectsInvalidOccurredOnLeavesNoAccountBehind(t *testing.T) {
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
			q := store.New(l.db)

			before, err := q.ListAccountsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListAccountsByFund() = %v, want no error", err)
			}

			_, err = l.CreateAccount(ctx, CreateAccountParams{
				FundID: f.fundID, Kind: "cash", Name: "Tunai Baru",
				OpeningBalance: &OpeningBalance{Amount: 100_000, OccurredOn: tt.occurredOn},
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("CreateAccount() = %v, want an error wrapping ErrInvalidArgument", err)
			}

			after, err := q.ListAccountsByFund(ctx, f.fundID)
			if err != nil {
				t.Fatalf("ListAccountsByFund() = %v, want no error", err)
			}
			if len(after) != len(before) {
				t.Errorf("ListAccountsByFund() = %d accounts after a refused create, want %d (unchanged)", len(after), len(before))
			}
		})
	}
}

// SetUpFund posts exactly the opening balances the accounts asked for -
// some, none, or all - in one batch, mixed freely in the same call.
func TestSetUpFundPostsOpeningBalancesForOnlyTheAccountsThatHaveThem(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()

	result, err := l.SetUpFund(ctx, SetUpFundParams{
		FundName: "Test Fund",
		Accounts: []AccountInput{
			{Kind: "cash", Name: "Tunai"},
			{Kind: "bank", Name: "Bank", OpeningBalance: &OpeningBalance{Amount: 500_000, OccurredOn: "2026-08-01"}},
		},
	})
	if err != nil {
		t.Fatalf("SetUpFund() = %v, want no error", err)
	}

	rows, err := store.New(l.db).ListTransactionsByFund(ctx, result.Fund.ID)
	if err != nil {
		t.Fatalf("ListTransactionsByFund() = %v, want no error", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ledger holds %d rows, want exactly 1 (only the bank account had a balance)", len(rows))
	}
	bankID := result.BankAccountID(t)
	row := rows[0]
	if row.AccountID != bankID {
		t.Errorf("AccountID = %d, want the bank account (%d)", row.AccountID, bankID)
	}
	if row.PurposeID != result.MainPurposeID {
		t.Errorf("PurposeID = %d, want the main purpose (%d)", row.PurposeID, result.MainPurposeID)
	}
	if row.Kind != "opening" || row.Direction != "in" || row.Amount != 500_000 {
		t.Errorf("row = %+v, want kind=opening direction=in amount=500000", row)
	}

	cashID := result.CashAccountID(t)
	cashBal, err := l.AccountBalance(ctx, result.Fund.ID, cashID)
	if err != nil {
		t.Fatalf("AccountBalance(cash) = %v, want no error", err)
	}
	if cashBal != 0 {
		t.Errorf("AccountBalance(cash) = %d, want 0 - it had no opening balance", cashBal)
	}

	fundBal, err := l.FundBalance(ctx, result.Fund.ID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 500_000 {
		t.Errorf("FundBalance() = %d, want %d", fundBal, 500_000)
	}
}

// A negative opening balance anywhere in the batch is refused before withTx
// opens, and leaves no fund behind at all - a retry with a corrected amount
// must not collide with ErrFundAlreadyExists.
func TestSetUpFundRejectsNegativeOpeningBalanceLeavesNoFundBehind(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()
	q := store.New(l.db)

	_, err := l.SetUpFund(ctx, SetUpFundParams{
		FundName: "Test Fund",
		Accounts: []AccountInput{
			{Kind: "cash", Name: "Tunai", OpeningBalance: &OpeningBalance{Amount: -1, OccurredOn: "2026-08-01"}},
		},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetUpFund() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	funds, err := q.ListFunds(ctx)
	if err != nil {
		t.Fatalf("ListFunds() = %v, want no error", err)
	}
	if len(funds) != 0 {
		t.Fatalf("ListFunds() = %d funds after a refused setup, want 0", len(funds))
	}

	// The retry succeeds - the refused call did not consume ErrFundAlreadyExists's
	// one-fund slot.
	if _, err := l.SetUpFund(ctx, SetUpFundParams{
		FundName: "Test Fund",
		Accounts: []AccountInput{{Kind: "cash", Name: "Tunai"}},
	}); err != nil {
		t.Fatalf("SetUpFund() retry after a rejected negative opening balance = %v, want no error", err)
	}
}

// A malformed occurred_on anywhere in the batch is refused the same way,
// leaving no fund and no account behind either.
func TestSetUpFundRejectsInvalidOccurredOnLeavesNoFundBehind(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()
	q := store.New(l.db)

	_, err := l.SetUpFund(ctx, SetUpFundParams{
		FundName: "Test Fund",
		Accounts: []AccountInput{
			{Kind: "cash", Name: "Tunai"},
			{Kind: "bank", Name: "Bank", OpeningBalance: &OpeningBalance{Amount: 100_000, OccurredOn: "not-a-date"}},
		},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetUpFund() = %v, want an error wrapping ErrInvalidArgument", err)
	}

	funds, err := q.ListFunds(ctx)
	if err != nil {
		t.Fatalf("ListFunds() = %v, want no error", err)
	}
	if len(funds) != 0 {
		t.Fatalf("ListFunds() = %d funds after a refused setup, want 0", len(funds))
	}
}

// The schema's opening_balance_once_per_account index is the actual
// guarantee, independent of either entry point's own logic: inserting a
// second kind='opening' row directly through raw store.Queries, bypassing
// both CreateAccount and SetUpFund entirely, must still be refused. This is
// the test that would catch the index itself being dropped.
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
