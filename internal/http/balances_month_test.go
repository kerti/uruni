package http

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// Beranda's "Bulan ini" and the report's running month are one ledger
// function's answer (ADR-038): for a month holding a reversal, an opening, an
// adjustment and transfers, GET /api/balances carries exactly the report's
// Walk.In and Walk.Out.
func TestGetBalancesMonthMatchesTheReportsRunningWalk(t *testing.T) {
	ctx := context.Background()
	sqlDB := testStoreDB(t)
	l := ledger.New(sqlDB)
	res, err := l.SetUpFund(ctx, ledger.SetUpFundParams{
		FundName: "Kas RT 05",
		Accounts: []ledger.AccountInput{{Kind: "cash", Name: "Tunai"}, {Kind: "bank", Name: "Bank"}},
	})
	if err != nil {
		t.Fatalf("SetUpFund() = %v", err)
	}
	fundID, cash, bank, main := res.Fund.ID, res.Accounts[0].ID, res.Accounts[1].ID, res.MainPurposeID

	// 2026-10-02 10:00 in Asia/Jakarta.
	now := time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC)
	post := func(direction string, amount int64, on string, adjustment bool) int64 {
		t.Helper()
		tx, err := l.PostTransaction(ctx, ledger.PostTransactionParams{
			FundID: fundID, AccountID: cash, PurposeID: main, Direction: direction, Amount: money.Amount(amount), OccurredOn: on, IsAdjustment: adjustment,
		})
		if err != nil {
			t.Fatalf("PostTransaction(%s %d) = %v", direction, amount, err)
		}
		return tx.ID
	}
	post("in", 50_000, "2026-09-20", false)
	env, err := l.OpenIncidental(ctx, ledger.OpenIncidentalParams{FundID: fundID, Occasion: "Duka", OpenedOn: "2026-09-01"})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v", err)
	}
	budi, err := store.New(sqlDB).CreateMember(ctx, store.CreateMemberParams{FundID: fundID, Name: "Budi", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateMember() = %v", err)
	}
	gift, err := l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: fundID, AccountID: cash, PurposeID: env.PurposeID, MemberID: &budi.ID, Direction: "in", Amount: 20_000, OccurredOn: "2026-09-25",
	})
	if err != nil {
		t.Fatalf("PostTransaction(contribution) = %v", err)
	}
	post("in", 200_000, "2026-10-01", false)
	post("out", 30_000, "2026-10-01", false)
	post("out", 4_000, "2026-10-02", true)
	if _, err := l.ReverseDuesPayment(ctx, ledger.ReverseDuesPaymentParams{FundID: fundID, TransactionID: gift.ID, OccurredOn: "2026-10-02"}); err != nil {
		t.Fatalf("ReverseDuesPayment() = %v", err)
	}
	if _, err := l.CreateAccount(ctx, ledger.CreateAccountParams{
		FundID: fundID, Kind: "cash", Name: "Kotak",
		OpeningBalance: &ledger.OpeningBalance{Amount: 100_000, OccurredOn: "2026-10-01"},
	}); err != nil {
		t.Fatalf("CreateAccount() = %v", err)
	}
	if _, err := l.PostTransferBetweenAccounts(ctx, ledger.PostTransferBetweenAccountsParams{
		FundID: fundID, FromAccountID: cash, ToAccountID: bank, PurposeID: main, Amount: 10_000, OccurredOn: "2026-10-02",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v", err)
	}
	if _, err := l.PostPurposeMove(ctx, ledger.PostPurposeMoveParams{
		FundID: fundID, FromPurposeID: main, ToPurposeID: env.PurposeID, AccountID: cash, Amount: 5_000, OccurredOn: "2026-10-02",
	}); err != nil {
		t.Fatalf("PostPurposeMove() = %v", err)
	}

	report, err := l.MonthlyReport(ctx, ledger.ReportParams{FundID: fundID, Now: now})
	if err != nil {
		t.Fatalf("MonthlyReport() = %v", err)
	}
	if !report.Running {
		t.Fatal("the report's month is not the running one")
	}

	r := authedRouterAt(t, sqlDB, func() time.Time { return now })
	rec := getBalances(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/balances = %d (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeBalances(t, rec).Month
	if got.In != report.Walk.In.Int64() || got.Out != report.Walk.Out.Int64() {
		t.Errorf("month = %+v, want in %d out %d (the report's Walk)", got, report.Walk.In.Int64(), report.Walk.Out.Int64())
	}
	// Pinned so the test cannot pass by both sides being zero: 200.000 in
	// less the reversed 20.000; 30.000 out.
	if got.In != 180_000 || got.Out != 30_000 {
		t.Errorf("month = %+v, want in 180000 out 30000", got)
	}
}
