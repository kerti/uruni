package ledger

import (
	"context"
	"errors"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// #474: a location still holding money cannot be retired. Retired, it is
// never counted in Cek kas (PRD 7.8) and refuses every posting (#444), yet
// FundBalance still sums it - so its money would sit where nothing proves
// it and nothing can move it. The treasurer moves the balance out first.
func TestRetireAccountRefusesALocationThatHoldsMoney(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	postOpeningBalance(t, l, f.fundID, f.bankID, f.mainID, 100_000, "2026-08-01")

	retire := func() error {
		on := "2026-08-31"
		_, err := l.UpdateAccount(ctx, f.fundID, store.UpdateAccountParams{ID: f.bankID, SetInactiveOn: 1, InactiveOn: &on})
		return err
	}
	if err := retire(); !errors.Is(err, ErrAccountHoldsMoney) {
		t.Fatalf("UpdateAccount(retire, holding 100000) = %v, want an error wrapping ErrAccountHoldsMoney", err)
	}
	if err := refuseInactiveAccount(ctx, l.q, f.fundID, f.bankID); err != nil {
		t.Fatalf("location retired despite the refusal: %v", err)
	}

	// Pindah lokasi empties it; then it retires.
	if _, err := l.PostTransferBetweenAccounts(ctx, PostTransferBetweenAccountsParams{
		FundID: f.fundID, PurposeID: f.mainID, FromAccountID: f.bankID, ToAccountID: f.cashID,
		Amount: 100_000, OccurredOn: "2026-08-30",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v, want no error", err)
	}
	if err := retire(); err != nil {
		t.Fatalf("UpdateAccount(retire, empty) = %v, want no error", err)
	}
	if err := refuseInactiveAccount(ctx, l.q, f.fundID, f.bankID); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("location after retiring: refuseInactiveAccount = %v, want ErrAccountInactive", err)
	}
}

// A negative recorded balance is not empty either, but it is not money to
// move: the ledger says more left than came in, and only a count finds that
// gap. It gets its own error so the treasurer is told the true next step.
func TestRetireAccountRefusesANegativeBalance(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "out", Amount: 5_000, OccurredOn: "2026-08-10",
	}); err != nil {
		t.Fatalf("PostTransaction() = %v, want no error", err)
	}
	on := "2026-08-31"
	if _, err := l.UpdateAccount(ctx, f.fundID, store.UpdateAccountParams{ID: f.cashID, SetInactiveOn: 1, InactiveOn: &on}); !errors.Is(err, ErrAccountBalanceNegative) {
		t.Fatalf("UpdateAccount(retire, at -5000) = %v, want an error wrapping ErrAccountBalanceNegative", err)
	}
}

// A location retired while holding money - before #474, or restored from a
// backup taken then - must still have a way out: reinstating is never
// refused, so the treasurer can move its money and retire it again.
func TestReinstatingALocationThatHoldsMoneyIsAllowed(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()
	postOpeningBalance(t, l, f.fundID, f.bankID, f.mainID, 100_000, "2026-08-01")
	on := "2026-08-31"
	if _, err := l.q.UpdateAccount(ctx, store.UpdateAccountParams{ID: f.bankID, SetInactiveOn: 1, InactiveOn: &on}); err != nil {
		t.Fatalf("retiring through the store (pre-#474 data) = %v, want no error", err)
	}

	reinstated, err := l.UpdateAccount(ctx, f.fundID, store.UpdateAccountParams{ID: f.bankID, SetInactiveOn: 1})
	if err != nil {
		t.Fatalf("UpdateAccount(reinstate, holding 100000) = %v, want no error", err)
	}
	if reinstated.InactiveOn != nil {
		t.Errorf("inactive_on = %q after reinstating, want nil", *reinstated.InactiveOn)
	}
}
