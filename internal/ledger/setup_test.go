package ledger

import (
	"context"
	"errors"
	"io"
	"math/big"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// SetUpFund writes exactly the fund, its main purpose, and its cash and bank
// accounts, and returns every id the caller needs right away.
func TestSetUpFundCreatesFundMainPurposeAndAccounts(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()
	q := store.New(l.db)

	result, err := l.SetUpFund(ctx, SetUpFundParams{FundName: "Test Fund"})
	if err != nil {
		t.Fatalf("SetUpFund() = %v, want no error", err)
	}

	if result.Fund.ID == 0 {
		t.Error("Fund.ID is zero")
	}
	if result.Fund.Name != "Test Fund" {
		t.Errorf("Fund.Name = %q, want %q", result.Fund.Name, "Test Fund")
	}
	if result.Fund.Currency != "IDR" {
		t.Errorf("Fund.Currency = %q, want %q", result.Fund.Currency, "IDR")
	}
	if len(result.Fund.ReportSlug) < 22 {
		t.Errorf("len(Fund.ReportSlug) = %d, want >= 22 (the schema's floor)", len(result.Fund.ReportSlug))
	}

	if result.MainPurposeID == 0 {
		t.Error("MainPurposeID is zero")
	}
	mainPurpose, err := q.GetPurpose(ctx, result.MainPurposeID)
	if err != nil {
		t.Fatalf("GetPurpose(main) = %v, want no error", err)
	}
	if mainPurpose.Kind != "main" {
		t.Errorf("main purpose Kind = %q, want %q", mainPurpose.Kind, "main")
	}
	if mainPurpose.FundID != result.Fund.ID {
		t.Errorf("main purpose FundID = %d, want %d", mainPurpose.FundID, result.Fund.ID)
	}

	if result.CashAccountID == 0 {
		t.Error("CashAccountID is zero")
	}
	cash, err := q.GetAccount(ctx, result.CashAccountID)
	if err != nil {
		t.Fatalf("GetAccount(cash) = %v, want no error", err)
	}
	if cash.Kind != "cash" {
		t.Errorf("cash account Kind = %q, want %q", cash.Kind, "cash")
	}
	if cash.FundID != result.Fund.ID {
		t.Errorf("cash account FundID = %d, want %d", cash.FundID, result.Fund.ID)
	}

	if result.BankAccountID == 0 {
		t.Error("BankAccountID is zero")
	}
	bank, err := q.GetAccount(ctx, result.BankAccountID)
	if err != nil {
		t.Fatalf("GetAccount(bank) = %v, want no error", err)
	}
	if bank.Kind != "bank" {
		t.Errorf("bank account Kind = %q, want %q", bank.Kind, "bank")
	}
	if bank.FundID != result.Fund.ID {
		t.Errorf("bank account FundID = %d, want %d", bank.FundID, result.Fund.ID)
	}

	// Nothing else landed in the atomic core: no members, dues tiers, dues
	// rates or opening balances - those are composed afterward by later
	// slices, as ordinary retriable calls.
	members, err := q.ListMembersByFund(ctx, result.Fund.ID)
	if err != nil {
		t.Fatalf("ListMembersByFund() = %v, want no error", err)
	}
	if len(members) != 0 {
		t.Errorf("ListMembersByFund() = %d rows, want 0 - members are not part of the atomic core", len(members))
	}
	accounts, err := q.ListAccountsByFund(ctx, result.Fund.ID)
	if err != nil {
		t.Fatalf("ListAccountsByFund() = %v, want no error", err)
	}
	if len(accounts) != 2 {
		t.Errorf("ListAccountsByFund() = %d rows, want exactly 2 (cash, bank)", len(accounts))
	}
	purposes, err := q.ListPurposesByFund(ctx, result.Fund.ID)
	if err != nil {
		t.Fatalf("ListPurposesByFund() = %v, want no error", err)
	}
	if len(purposes) != 1 {
		t.Errorf("ListPurposesByFund() = %d rows, want exactly 1 (main)", len(purposes))
	}
}

// Two different funds - necessarily two different databases, since a second
// SetUpFund call against the same one is refused (see the test below) - get
// two different slugs. This is the whole entropy argument made observable,
// not a proof of uniqueness (no test can prove that), but a check that the
// generator is not, say, returning a constant.
func TestSetUpFundGeneratesDistinctReportSlugs(t *testing.T) {
	first, err := newTestLedger(t).SetUpFund(context.Background(), SetUpFundParams{FundName: "Fund One"})
	if err != nil {
		t.Fatalf("first SetUpFund() = %v, want no error", err)
	}
	second, err := newTestLedger(t).SetUpFund(context.Background(), SetUpFundParams{FundName: "Fund Two"})
	if err != nil {
		t.Fatalf("second SetUpFund() = %v, want no error", err)
	}

	if first.Fund.ReportSlug == second.Fund.ReportSlug {
		t.Errorf("both funds got report_slug %q, want two distinct slugs", first.Fund.ReportSlug)
	}
}

func TestSetUpFundRejectsEmptyFundName(t *testing.T) {
	l := newTestLedger(t)

	_, err := l.SetUpFund(context.Background(), SetUpFundParams{FundName: "   "})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetUpFund(blank name) = %v, want an error wrapping ErrInvalidArgument", err)
	}

	funds, listErr := store.New(l.db).ListFunds(context.Background())
	if listErr != nil {
		t.Fatalf("ListFunds() = %v, want no error", listErr)
	}
	if len(funds) != 0 {
		t.Errorf("ListFunds() = %d rows after a rejected call, want 0", len(funds))
	}
}

// A second run is refused with the named sentinel, and leaves the first
// fund - and only the first fund - standing.
func TestSetUpFundSecondCallIsRefusedAndPostsNoSecondFund(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()

	first, err := l.SetUpFund(ctx, SetUpFundParams{FundName: "Test Fund"})
	if err != nil {
		t.Fatalf("first SetUpFund() = %v, want no error", err)
	}

	_, err = l.SetUpFund(ctx, SetUpFundParams{FundName: "Second Fund"})
	if !errors.Is(err, ErrFundAlreadyExists) {
		t.Fatalf("second SetUpFund() = %v, want an error wrapping ErrFundAlreadyExists", err)
	}

	funds, err := store.New(l.db).ListFunds(ctx)
	if err != nil {
		t.Fatalf("ListFunds() = %v, want no error", err)
	}
	if len(funds) != 1 {
		t.Fatalf("ListFunds() = %d rows after a refused second setup, want exactly 1", len(funds))
	}
	if funds[0].ID != first.Fund.ID {
		t.Errorf("the surviving fund is %+v, want the first call's own fund %+v", funds[0], first.Fund)
	}
}

// Atomicity of the atomic core: if the purpose insert fails right after the
// fund insert already succeeded within the same withTx, the fund does not
// survive either - no orphan fund with nowhere to post, and no accounts
// created off the back of a purpose that never landed.
//
// Forcing this needs a real conflict, not a mocked one. Unlike incidental
// (#42's own atomicity test), account and purpose both use a plain
// autoincrementing PRIMARY KEY, and SQLite assigns a rowid table's next id as
// (current max + 1) - which means pre-occupying a row at the id an upcoming
// insert would take does not force a collision, it just pushes the next
// insert one further along. purpose_single_main is the lever that works
// instead: it is a genuine UNIQUE index (partial: WHERE kind = 'main'), so
// pre-occupying fund_id=1's only kind='main' slot before calling SetUpFund
// - with `PRAGMA foreign_keys` toggled off around the one raw INSERT needed
// to write a purpose row against a fund_id with no live fund behind it yet -
// guarantees SetUpFund's own main-purpose insert collides with it, once its
// fund insert lands at id 1 in this brand-new, empty database (no fixture -
// a fixture's own fund would trip the second-run guard before any of this
// matters).
func TestSetUpFundAtomicityNoOrphanFundWhenThePurposeInsertFails(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()

	const squattedFundID = 1 // the fund SetUpFund is about to create; see the comment above.

	if _, err := l.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disabling foreign_keys for setup = %v, want no error", err)
	}
	if _, err := l.db.ExecContext(ctx,
		`INSERT INTO purpose (fund_id, kind, name, created_at) VALUES (?, ?, ?, ?)`,
		squattedFundID, "main", "Squatter", 1,
	); err != nil {
		t.Fatalf("pre-occupying fund_id %d's main purpose = %v, want no error", squattedFundID, err)
	}
	if _, err := l.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("re-enabling foreign_keys after setup = %v, want no error", err)
	}

	_, err := l.SetUpFund(ctx, SetUpFundParams{FundName: "Test Fund"})
	if err == nil {
		t.Fatal("SetUpFund() = nil error, want a purpose_single_main conflict on the main purpose insert")
	}

	q := store.New(l.db)
	funds, err := q.ListFunds(ctx)
	if err != nil {
		t.Fatalf("ListFunds() after the forced failure = %v, want no error", err)
	}
	if len(funds) != 0 {
		t.Errorf("ListFunds() = %d rows after the forced failure, want 0 - no orphan fund", len(funds))
	}

	// Exactly the squatter row - SetUpFund's own attempt must have rolled back.
	var purposeCount int
	if err := l.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM purpose").Scan(&purposeCount); err != nil {
		t.Fatalf("counting purpose rows = %v, want no error", err)
	}
	if purposeCount != 1 {
		t.Errorf("purpose row count = %d, want 1 (only the pre-occupying squatter row)", purposeCount)
	}

	var accountCount int
	if err := l.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM account").Scan(&accountCount); err != nil {
		t.Fatalf("counting account rows = %v, want no error", err)
	}
	if accountCount != 0 {
		t.Errorf("account row count = %d, want 0 - the purpose failure must happen before either account insert", accountCount)
	}
}

func TestSetUpFundAbortsWhenTheSlugSourceFails(t *testing.T) {
	l := newTestLedger(t)
	ctx := context.Background()

	// A failing random source is not a real operating condition, but the slug
	// is the one value here that must never be quietly weakened: it is
	// generated once, never rotates, and PRD §7.9 leans on it being
	// unguessable. Setup must abort rather than fall back to anything.
	original := randInt
	randInt = func(io.Reader, *big.Int) (*big.Int, error) {
		return nil, errors.New("no entropy available")
	}
	t.Cleanup(func() { randInt = original })

	_, err := l.SetUpFund(ctx, SetUpFundParams{FundName: "Test Fund"})
	if err == nil {
		t.Fatal("SetUpFund() = nil error, want the slug failure to abort setup")
	}

	funds, err := store.New(l.db).ListFunds(ctx)
	if err != nil {
		t.Fatalf("ListFunds() = %v, want no error", err)
	}
	if len(funds) != 0 {
		t.Errorf("ListFunds() = %d rows, want 0 - a fund must not exist with an unverified slug", len(funds))
	}
}
