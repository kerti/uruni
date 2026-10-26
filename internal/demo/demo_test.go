package demo

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("db.Open() = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Up(context.Background(), sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v", err)
	}
	return sqlDB
}

var fixedNow = func() time.Time { return time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC) }

// ADR-045 decision 5, the part of it this slice writes: one fund of the fixed
// name, zone and slug, a cash and a bank account, and a treasurer login that
// signs in with the published credentials. The seed registers through auth, so a
// password on the common-passwords denylist would fail here rather than ship.
func TestEnsureSeedsAnEmptyDatabase(t *testing.T) {
	t.Parallel()
	sqlDB := newDB(t)
	ctx := context.Background()

	if err := Ensure(ctx, sqlDB, fixedNow); err != nil {
		t.Fatalf("Ensure() on an empty database = %v, want nil", err)
	}

	q := store.New(sqlDB)
	funds, err := q.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("ListFunds() = %d funds, %v; want exactly one", len(funds), err)
	}
	fund := funds[0]
	if fund.Name != "CS Shift Sore" {
		t.Errorf("fund name = %q, want %q", fund.Name, "CS Shift Sore")
	}
	if fund.TimeZone != "Asia/Jakarta" {
		t.Errorf("fund time zone = %q, want Asia/Jakarta (WIB)", fund.TimeZone)
	}
	if fund.ReportSlug != "CsShiftSoreDemoUruni2026" {
		t.Errorf("report slug = %q, want the fixed %q", fund.ReportSlug, "CsShiftSoreDemoUruni2026")
	}
	if got := time.Unix(fund.CreatedAt, 0).UTC(); !got.Equal(fixedNow()) {
		t.Errorf("fund created_at = %v, want the injected clock %v", got, fixedNow())
	}

	accounts, err := q.ListAccountsByFund(ctx, fund.ID)
	if err != nil {
		t.Fatalf("ListAccountsByFund() = %v", err)
	}
	kinds := map[string]int{}
	for _, a := range accounts {
		kinds[a.Kind]++
	}
	if len(accounts) != 2 || kinds["cash"] != 1 || kinds["bank"] != 1 {
		t.Errorf("accounts = %v, want one cash and one bank", kinds)
	}

	user, err := auth.New(sqlDB).Authenticate(ctx, Email, Password)
	if err != nil {
		t.Fatalf("Authenticate(%s, the demo password) = %v, want the login to work", Email, err)
	}
	if user.Email != Email {
		t.Errorf("user email = %q, want %q", user.Email, Email)
	}
}

// A boot over an earlier boot of the demo does not reseed: the fixture is
// written once, and the visitors' changes stay until the hour is up.
func TestEnsureLeavesAnExistingDemoAlone(t *testing.T) {
	t.Parallel()
	sqlDB := newDB(t)
	ctx := context.Background()
	if err := Ensure(ctx, sqlDB, fixedNow); err != nil {
		t.Fatalf("first Ensure() = %v", err)
	}
	funds, err := store.New(sqlDB).ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("ListFunds() = %d, %v", len(funds), err)
	}
	// A visitor renames the fund.
	if _, err := sqlDB.ExecContext(ctx, `UPDATE fund SET name = 'Diubah pengunjung'`); err != nil {
		t.Fatalf("renaming the fund: %v", err)
	}

	if err := Ensure(ctx, sqlDB, fixedNow); err != nil {
		t.Fatalf("second Ensure() = %v, want nil on the demo's own database", err)
	}
	var name string
	if err := sqlDB.QueryRowContext(ctx, `SELECT name FROM fund`).Scan(&name); err != nil {
		t.Fatalf("reading the fund: %v", err)
	}
	if name != "Diubah pengunjung" {
		t.Errorf("fund name = %q after a second Ensure, want the visitor's change kept (no reseed)", name)
	}
}

// The setting cannot be turned on over a real fund by accident (ADR-045
// decision 2): a login that is not the demo's stops the boot, naming URUNI_DEMO,
// and nothing is written.
func TestEnsureRefusesAForeignLogin(t *testing.T) {
	t.Parallel()
	sqlDB := newDB(t)
	ctx := context.Background()
	if _, err := auth.New(sqlDB).Register(ctx, "bendahara@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("Register() = %v", err)
	}

	err := Ensure(ctx, sqlDB, fixedNow)
	if !errors.Is(err, ErrForeignData) {
		t.Fatalf("Ensure() over someone's login = %v, want ErrForeignData", err)
	}
	if !strings.Contains(err.Error(), "URUNI_DEMO") {
		t.Errorf("error = %q, want it to name URUNI_DEMO", err)
	}
	funds, listErr := store.New(sqlDB).ListFunds(ctx)
	if listErr != nil || len(funds) != 0 {
		t.Errorf("ListFunds() = %d, %v; want nothing seeded over the refused database", len(funds), listErr)
	}
}

// A fund with no login is somebody's data too (a real instance between its
// setup and its first login cannot reach this, a restored file can): refused,
// not seeded over.
func TestEnsureRefusesAFundWithNoLogin(t *testing.T) {
	t.Parallel()
	sqlDB := newDB(t)
	ctx := context.Background()
	if _, err := ledger.New(sqlDB).SetUpFund(ctx, ledger.SetUpFundParams{
		FundName: "Kas Asli", Accounts: []ledger.AccountInput{{Kind: "cash", Name: "Tunai"}},
	}); err != nil {
		t.Fatalf("SetUpFund() = %v", err)
	}

	if err := Ensure(ctx, sqlDB, fixedNow); !errors.Is(err, ErrForeignData) {
		t.Fatalf("Ensure() over a fund with no login = %v, want ErrForeignData", err)
	}
}

// The login is matched as auth normalises it, so a demo login stored in another
// case is still the demo's.
func TestEnsureAcceptsTheDemoLoginInAnyCase(t *testing.T) {
	t.Parallel()
	sqlDB := newDB(t)
	ctx := context.Background()
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO "user" (email, password_hash, created_at) VALUES ('Demo@Uruni.Test', 'x', 1)`); err != nil {
		t.Fatalf("inserting the login: %v", err)
	}
	if err := Ensure(ctx, sqlDB, fixedNow); err != nil {
		t.Fatalf("Ensure() = %v, want nil: the login is the demo's", err)
	}
}
