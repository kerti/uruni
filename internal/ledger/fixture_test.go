package ledger

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/store"
)

// newTestLedger returns a Ledger over a private in-memory database carrying the
// real schema.
//
// It reuses internal/db.Open rather than opening a connection itself, so the
// tests run against production's configuration: SetMaxOpenConns(1) and all four
// pragmas, foreign_keys=ON among them — SQLite leaves that off by default, per
// connection, and the DSN is what turns it on everywhere (ADR-028).
//
// ":memory:" is private to this connection, with no cache=shared. That is safe
// only because the pool never opens a second connection; raising MaxOpenConns
// above 1 would silently give a second connection an empty database. Whoever
// revisits pooling should read ADR-028 as well as ADR-004.
//
// This differs from internal/db's own helper, which uses a real temp file
// deliberately: those tests assert the WAL pragma itself, and WAL degrades to a
// memory journal in-memory. Nothing here asserts a pragma — these tests are
// about CHECKs, foreign keys and triggers, which behave identically.
func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	return New(newTestDB(t))
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()

	sqlDB, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("db.Open(\":memory:\") = %v, want no error", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("Close() = %v, want no error", err)
		}
	})

	if _, err := db.Up(context.Background(), sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v, want no error", err)
	}
	return sqlDB
}

// validSlug is 22 characters, the minimum report_slug length, so a fixture that
// is not exercising the slug constraint can reuse it.
const validSlug = "abcdefghijklmnopqrstuv"

// fixture is one fund with the rows the domain tests need under it. It mirrors
// internal/db's ledgerFixture rather than sharing it: that one is unexported in
// another package, and one more small fixture file is cheaper than exporting
// test-only surface across a package boundary (ADR-028).
type fixture struct {
	fundID    int64
	cashID    int64
	bankID    int64
	mainID    int64
	memberID  int64
	incidenID int64
	passID    int64
}

func newFixture(t *testing.T, l *Ledger) fixture {
	t.Helper()

	ctx := context.Background()
	q := store.New(l.db)
	f := fixture{}

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", ReportSlug: validSlug, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	f.fundID = fund.ID

	f.cashID = createAccount(t, q, f.fundID, "cash", "Cash")
	f.bankID = createAccount(t, q, f.fundID, "bank", "Bank Account")
	f.mainID = createPurpose(t, q, f.fundID, "main", "Primary Cash")
	f.incidenID = createPurpose(t, q, f.fundID, "incidental", "John's birthday")
	f.passID = createPurpose(t, q, f.fundID, "pass_through", "Pass-through")

	member, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: f.fundID, Name: "Jane", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember() = %v, want no error", err)
	}
	f.memberID = member.ID

	return f
}

func createAccount(t *testing.T, q *store.Queries, fundID int64, kind, name string) int64 {
	t.Helper()
	a, err := q.CreateAccount(context.Background(), store.CreateAccountParams{
		FundID: fundID, Kind: kind, Name: name, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateAccount(%q) = %v, want no error", kind, err)
	}
	return a.ID
}

// defaultSetupAccounts is the cash+bank pair every SetUpFund test in this
// package builds on unless it's specifically exercising #78's "N accounts,
// not a fixed two" - one shared default rather than each test re-typing the
// same two-element literal, the ledger-package equivalent of
// internal/http/testhelpers_test.go's shared fixture.
var defaultSetupAccounts = []AccountInput{
	{Kind: "cash", Name: "Tunai"},
	{Kind: "bank", Name: "Bank"},
}

// CashAccountID and BankAccountID find SetUpFundResult's own cash/bank
// account by Kind rather than assuming a fixed slice order or count - #78
// dropped both assumptions from production code, so the test helper that
// reads a result back out does not get to lean on them either.
func (r SetUpFundResult) CashAccountID(t *testing.T) int64 {
	return accountIDByKind(t, r.Accounts, "cash")
}

func (r SetUpFundResult) BankAccountID(t *testing.T) int64 {
	return accountIDByKind(t, r.Accounts, "bank")
}

func accountIDByKind(t *testing.T, accounts []store.Account, kind string) int64 {
	t.Helper()
	for _, a := range accounts {
		if a.Kind == kind {
			return a.ID
		}
	}
	t.Fatalf("no account of kind %q among %+v", kind, accounts)
	return 0
}

func createPurpose(t *testing.T, q *store.Queries, fundID int64, kind, name string) int64 {
	t.Helper()
	p, err := q.CreatePurpose(context.Background(), store.CreatePurposeParams{
		FundID: fundID, Kind: kind, Name: name, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreatePurpose(%q) = %v, want no error", kind, err)
	}
	return p.ID
}
