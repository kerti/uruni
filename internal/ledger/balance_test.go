package ledger

import (
	"context"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// An empty fund's balances are 0, not an error.
func TestEmptyFundBalancesAreZero(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 0 {
		t.Errorf("FundBalance() = %d, want 0", fundBal)
	}

	acctBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if acctBal != 0 {
		t.Errorf("AccountBalance() = %d, want 0", acctBal)
	}

	purposeBal, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance() = %v, want no error", err)
	}
	if purposeBal != 0 {
		t.Errorf("PurposeBalance() = %d, want 0", purposeBal)
	}
}

// pass_through and incidental purposes sum into FundBalance and
// AccountBalance exactly like main - PRD 7.6 as revised, ADR-024's "either
// both leave the headline or neither does". There is no second "available"
// balance anywhere in Uruni.
func TestPassThroughAndIncidentalSumIntoFundAndAccountBalanceLikeMain(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	posts := []struct {
		purposeID int64
		amount    money.Amount
	}{
		{f.mainID, 100_000},
		{f.passID, 30_000},
		{f.incidenID, 20_000},
	}
	for _, p := range posts {
		if _, err := l.PostTransaction(ctx, PostTransactionParams{
			FundID: f.fundID, AccountID: f.cashID, PurposeID: p.purposeID,
			Direction: "in", Amount: p.amount, OccurredOn: "2026-08-12",
		}); err != nil {
			t.Fatalf("PostTransaction(purpose=%d) = %v, want no error", p.purposeID, err)
		}
	}

	const want = money.Amount(150_000)

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != want {
		t.Errorf("FundBalance() = %d, want %d - pass_through and incidental must sum in exactly like main", fundBal, want)
	}

	acctBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if acctBal != want {
		t.Errorf("AccountBalance() = %d, want %d - pass_through and incidental must sum in exactly like main", acctBal, want)
	}
}

// PurposeBalance reports per purpose while AccountBalance and FundBalance
// stay pooled across every purpose.
func TestPurposeBalanceIsPerPurposeWhileAccountAndFundStayPooled(t *testing.T) {
	l := newTestLedger(t)
	f := newFixture(t, l)
	ctx := context.Background()

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 100_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransaction(main) = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.incidenID,
		Direction: "in", Amount: 40_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransaction(incidental) = %v, want no error", err)
	}

	mainBal, err := l.PurposeBalance(ctx, f.fundID, f.mainID)
	if err != nil {
		t.Fatalf("PurposeBalance(main) = %v, want no error", err)
	}
	if mainBal != 100_000 {
		t.Errorf("PurposeBalance(main) = %d, want 100000", mainBal)
	}

	incidentalBal, err := l.PurposeBalance(ctx, f.fundID, f.incidenID)
	if err != nil {
		t.Fatalf("PurposeBalance(incidental) = %v, want no error", err)
	}
	if incidentalBal != 40_000 {
		t.Errorf("PurposeBalance(incidental) = %d, want 40000", incidentalBal)
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 140_000 {
		t.Errorf("FundBalance() = %d, want 140000 - pooled across both purposes", fundBal)
	}
}

// A second fund's rows never appear in the first fund's balances.
func TestASecondFundsRowsNeverAppearInTheFirstFundsBalances(t *testing.T) {
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
	otherCash := createAccount(t, q, other.ID, "cash", "Cash")
	otherMain := createPurpose(t, q, other.ID, "main", "Main")

	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransaction(fund 1) = %v, want no error", err)
	}
	if _, err := l.PostTransaction(ctx, PostTransactionParams{
		FundID: other.ID, AccountID: otherCash, PurposeID: otherMain,
		Direction: "in", Amount: 999_000, OccurredOn: "2026-08-12",
	}); err != nil {
		t.Fatalf("PostTransaction(fund 2) = %v, want no error", err)
	}

	fundBal, err := l.FundBalance(ctx, f.fundID)
	if err != nil {
		t.Fatalf("FundBalance() = %v, want no error", err)
	}
	if fundBal != 50_000 {
		t.Errorf("FundBalance(fund 1) = %d, want 50000 - fund 2's rows leaked in", fundBal)
	}

	acctBal, err := l.AccountBalance(ctx, f.fundID, f.cashID)
	if err != nil {
		t.Fatalf("AccountBalance() = %v, want no error", err)
	}
	if acctBal != 50_000 {
		t.Errorf("AccountBalance(fund 1) = %d, want 50000", acctBal)
	}
}
