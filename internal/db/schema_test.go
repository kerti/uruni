package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// migratedTestDB opens a throwaway database (via openTestDB, from db_test.go)
// and applies every migration, which every schema test needs before it can
// exercise a table.
func migratedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB := openTestDB(t)
	if _, err := Up(context.Background(), sqlDB, discardLogger()); err != nil {
		t.Fatalf("Up() = %v, want no error", err)
	}
	return sqlDB
}

// validSlug is 22 characters — the minimum report_slug length — so tests that
// aren't exercising the slug constraint itself can just reuse it.
const validSlug = "abcdefghijklmnopqrstuv"

func createFund(t *testing.T, sqlDB *sql.DB, name, slug string) int64 {
	t.Helper()
	f, err := store.New(sqlDB).CreateFund(context.Background(), store.CreateFundParams{
		Name:       name,
		Currency:   "IDR",
		ReportSlug: slug,
		CreatedAt:  1,
	})
	if err != nil {
		t.Fatalf("CreateFund(%q, %q) = %v, want no error", name, slug, err)
	}
	return f.ID
}

func TestStrictRejectsAFloatInAnIntegerColumn(t *testing.T) {
	sqlDB := migratedTestDB(t)

	// created_at is INTEGER; STRICT is what turns "1000.50" into an error
	// instead of a silently truncated or coerced value (ADR-024, ADR-006).
	_, err := sqlDB.Exec(
		`INSERT INTO fund (name, currency, report_slug, created_at) VALUES (?, ?, ?, 1000.50)`,
		"Kas RT", "IDR", validSlug,
	)
	if err == nil {
		t.Fatal("INSERT with a float into created_at = nil error, want STRICT to reject it")
	}
}

func TestPurposeSingleMainIsScopedPerFund(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	fundA := createFund(t, sqlDB, "Fund A", validSlug)
	fundB := createFund(t, sqlDB, "Fund B", "bcdefghijklmnopqrstuvw")

	if _, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: fundA, Kind: "main", Name: "Kas Utama", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("first main purpose in fund A = %v, want no error", err)
	}

	if _, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: fundA, Kind: "main", Name: "Kas Utama Kedua", CreatedAt: 1,
	}); err == nil {
		t.Fatal("second main purpose in the same fund = nil error, want the partial index to reject it")
	}

	if _, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: fundB, Kind: "main", Name: "Kas Utama", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("main purpose in a different fund = %v, want no error", err)
	}
}

func TestAccountRejectsAFundIDThatDoesNotExist(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	_, err := q.CreateAccount(ctx, store.CreateAccountParams{
		FundID: 999999, Kind: "cash", Name: "Kas tunai", CreatedAt: 1,
	})
	if err == nil {
		t.Fatal("CreateAccount with a nonexistent fund_id = nil error, want the FK to reject it")
	}
}

func TestReportSlugLengthAndUniqueness(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	if _, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Too Short", Currency: "IDR", ReportSlug: strings.Repeat("a", 21), CreatedAt: 1,
	}); err == nil {
		t.Fatal("CreateFund with a 21-char report_slug = nil error, want the length CHECK to reject it")
	}

	if _, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "First", Currency: "IDR", ReportSlug: validSlug, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("first fund with a valid slug = %v, want no error", err)
	}

	if _, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Second", Currency: "IDR", ReportSlug: validSlug, CreatedAt: 1,
	}); err == nil {
		t.Fatal("CreateFund with a duplicate report_slug = nil error, want the UNIQUE constraint to reject it")
	}
}

func TestNameCannotBeEmptyOrWhitespaceOnly(t *testing.T) {
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Host fund", validSlug)

	tests := []struct {
		name string
		bad  string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
	}

	for _, tt := range tests {
		t.Run("fund/"+tt.name, func(t *testing.T) {
			if _, err := q.CreateFund(ctx, store.CreateFundParams{
				Name: tt.bad, Currency: "IDR", ReportSlug: "zzzzzzzzzzzzzzzzzzzzzz", CreatedAt: 1,
			}); err == nil {
				t.Errorf("CreateFund with name %q = nil error, want the CHECK to reject it", tt.bad)
			}
		})

		t.Run("account/"+tt.name, func(t *testing.T) {
			if _, err := q.CreateAccount(ctx, store.CreateAccountParams{
				FundID: fundID, Kind: "cash", Name: tt.bad, CreatedAt: 1,
			}); err == nil {
				t.Errorf("CreateAccount with name %q = nil error, want the CHECK to reject it", tt.bad)
			}
		})

		t.Run("purpose/"+tt.name, func(t *testing.T) {
			if _, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
				FundID: fundID, Kind: "incidental", Name: tt.bad, CreatedAt: 1,
			}); err == nil {
				t.Errorf("CreatePurpose with name %q = nil error, want the CHECK to reject it", tt.bad)
			}
		})
	}
}

func TestMigrationAppliesFromEmptyAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	sqlDB := openTestDB(t)

	if _, err := Up(ctx, sqlDB, discardLogger()); err != nil {
		t.Fatalf("Up() from an empty database = %v, want no error", err)
	}

	// The three tables exist and are queryable through the generated store.
	q := store.New(sqlDB)
	if _, err := q.ListFunds(ctx); err != nil {
		t.Fatalf("ListFunds() after Up() = %v, want no error", err)
	}

	if _, err := Down(ctx, sqlDB, discardLogger()); err != nil {
		t.Fatalf("Down() = %v, want no error", err)
	}
	status, err := Status(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Status() after Down() = %v, want no error", err)
	}
	for _, m := range status {
		if m.Applied {
			t.Errorf("migration %d (%s) still applied after Down() to version 0", m.Version, m.Name)
		}
	}

	if _, err := Up(ctx, sqlDB, discardLogger()); err != nil {
		t.Fatalf("second Up() after Down() = %v, want no error", err)
	}
	if _, err := q.ListFunds(ctx); err != nil {
		t.Fatalf("ListFunds() after reapplying = %v, want no error", err)
	}
}
