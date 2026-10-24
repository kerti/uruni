package ledger

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// newTestLedger returns a Ledger over a private in-memory database with the
// real schema.
//
// It reuses internal/db.Open, so tests run on production's configuration:
// SetMaxOpenConns(1) and all four pragmas, foreign_keys=ON among them (SQLite
// leaves it off per connection; the DSN turns it on everywhere, ADR-028).
//
// ":memory:" is private to this connection, with no cache=shared. That is safe
// only because the pool never opens a second connection; raising MaxOpenConns
// above 1 would silently give a second connection an empty database. Read
// ADR-028 as well as ADR-004 before revisiting pooling.
//
// This differs from internal/db's helper, which uses a real temp file because
// those tests assert the WAL pragma, and WAL degrades to a memory journal
// in-memory. Nothing here asserts a pragma: these tests are about CHECKs,
// foreign keys and triggers, which behave identically.
func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	return New(newTestDB(t))
}

// at is l reading the clock as t: a copy over the same database, so the calls
// that follow stand on that instant and l itself keeps its own clock.
func (l *Ledger) at(t time.Time) *Ledger {
	tl := *l
	tl.now = func() time.Time { return t }
	return &tl
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

// validSlug is 22 characters, the minimum report_slug length, for fixtures not
// exercising the slug constraint.
const validSlug = "abcdefghijklmnopqrstuv"

// fixture is one fund with the rows the domain tests need. It mirrors
// internal/db's ledgerFixture rather than sharing it: that one is unexported
// in another package, and a small fixture file is cheaper than exporting
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
		Name: "Test Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: validSlug, CreatedAt: 1,
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

	member, err := q.CreateMember(ctx, store.CreateMemberParams{JoinedOn: "2000-01-01",
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

// defaultSetupAccounts is the cash+bank pair every SetUpFund test builds on
// unless exercising #78's "N accounts, not a fixed two"; one shared default,
// as internal/http/testhelpers_test.go's fixture.
var defaultSetupAccounts = []AccountInput{
	{Kind: "cash", Name: "Tunai"},
	{Kind: "bank", Name: "Bank"},
}

// CashAccountID and BankAccountID find SetUpFundResult's cash/bank account by
// Kind, not by slice order or count: #78 dropped both assumptions from
// production code, so test helpers may not lean on them either.
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

// postOpeningBalance gives an existing account a starting figure for
// reconciliation_test.go and reimbursement_test.go: #230 removed
// PostOpeningBalance and its route, so an opening balance posts only with its
// account (SetUpFund, Ledger.CreateAccount), neither of which fits a test
// whose account came from this file's createAccount. It uses the package's
// unexported insert path rather than add a production entry point for tests.
func postOpeningBalance(t *testing.T, l *Ledger, fundID, accountID, purposeID int64, amount money.Amount, occurredOn string) store.Transaction {
	t.Helper()
	ob := OpeningBalance{Amount: amount, OccurredOn: occurredOn}
	if err := validateOpeningBalance(ob, noCeiling); err != nil {
		t.Fatalf("validateOpeningBalance() = %v, want no error", err)
	}

	ctx := context.Background()
	var posted store.Transaction
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		posted, err = postOpeningBalanceRow(ctx, q, fundID, accountID, purposeID, ob, time.Now().Unix())
		return err
	})
	if err != nil {
		t.Fatalf("postOpeningBalanceRow() = %v, want no error", err)
	}
	return posted
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

// postReversalOn posts the row ReverseTransaction would post for the original,
// dated on a day the ledger would not pick. The ledger dates a reversal on the
// original's own day, or today once a cek kas has counted it (ADR-042), so a
// test whose subject is the walk, a report or a total over reversals scattered
// across months writes the row directly, exactly as a legacy file or a restore
// holds one. The copied fields are the ledger's: the other way, the original's
// amount, account, member and period, and its EFFECTIVE purpose.
func postReversalOn(t *testing.T, l *Ledger, fundID, originalID int64, on string) store.Transaction {
	t.Helper()
	ctx := context.Background()
	q := store.New(l.db)
	original, err := q.GetReversalTarget(ctx, store.GetReversalTargetParams{FundID: fundID, ID: originalID})
	if err != nil {
		t.Fatalf("GetReversalTarget(%d) = %v, want no error", originalID, err)
	}
	direction := "in"
	if original.Direction == "in" {
		direction = "out"
	}
	row, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: fundID, AccountID: original.AccountID, PurposeID: original.EffectivePurposeID,
		Direction: direction, Amount: original.Amount, OccurredOn: on, Kind: "adjustment",
		MemberID: original.MemberID, DuesPeriod: original.DuesPeriod,
		ReversesTransactionID: &originalID, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("posting the reversal of %d on %s = %v, want no error", originalID, on, err)
	}
	return row
}

// countEverything takes a cek kas that finds every named location matched at
// its recorded balance. It covers every row posted so far: the count's cutoff is
// the fund's highest transaction id.
func countEverything(t *testing.T, l *Ledger, fundID int64, accountIDs ...int64) store.Reconciliation {
	t.Helper()
	ctx := context.Background()
	counts := make([]AccountCount, 0, len(accountIDs))
	for _, id := range accountIDs {
		balance, err := l.AccountBalance(ctx, fundID, id)
		if err != nil {
			t.Fatalf("AccountBalance(%d) = %v, want no error", id, err)
		}
		counts = append(counts, AccountCount{AccountID: id, ActualAmount: balance, Resolution: "matched"})
	}
	rec, err := l.TakeReconciliation(ctx, TakeReconciliationParams{FundID: fundID, Counts: counts})
	if err != nil {
		t.Fatalf("TakeReconciliation() = %v, want no error", err)
	}
	return rec
}
