package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
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

// validSlug is 22 characters - the minimum report_slug length - so tests that
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
	t.Parallel()
	sqlDB := migratedTestDB(t)

	// created_at is INTEGER; STRICT is what turns "1000.50" into an error
	// instead of a silently truncated or coerced value (ADR-024, ADR-006).
	_, err := sqlDB.Exec(
		`INSERT INTO fund (name, currency, report_slug, created_at) VALUES (?, ?, ?, 1000.50)`,
		"Test Fund", "IDR", validSlug,
	)
	if err == nil {
		t.Fatal("INSERT with a float into created_at = nil error, want STRICT to reject it")
	}
}

func TestPurposeSingleMainIsScopedPerFund(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	fundA := createFund(t, sqlDB, "Fund A", validSlug)
	fundB := createFund(t, sqlDB, "Fund B", "bcdefghijklmnopqrstuvw")

	if _, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: fundA, Kind: "main", Name: "Main", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("first main purpose in fund A = %v, want no error", err)
	}

	if _, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: fundA, Kind: "main", Name: "Second Main", CreatedAt: 1,
	}); err == nil {
		t.Fatal("second main purpose in the same fund = nil error, want the partial index to reject it")
	}

	if _, err := q.CreatePurpose(ctx, store.CreatePurposeParams{
		FundID: fundB, Kind: "main", Name: "Main", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("main purpose in a different fund = %v, want no error", err)
	}
}

func TestAccountRejectsAFundIDThatDoesNotExist(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	_, err := q.CreateAccount(ctx, store.CreateAccountParams{
		FundID: 999999, Kind: "cash", Name: "Cash", CreatedAt: 1,
	})
	if err == nil {
		t.Fatal("CreateAccount with a nonexistent fund_id = nil error, want the FK to reject it")
	}
}

func TestReportSlugLengthAndUniqueness(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	ctx := context.Background()
	sqlDB := openTestDB(t)

	if _, err := Up(ctx, sqlDB, discardLogger()); err != nil {
		t.Fatalf("Up() from an empty database = %v, want no error", err)
	}

	// The tables exist and are queryable through the generated store.
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

func createDuesTier(t *testing.T, sqlDB *sql.DB, fundID int64, name string) int64 {
	t.Helper()
	tier, err := store.New(sqlDB).CreateDuesTier(context.Background(), store.CreateDuesTierParams{
		FundID: fundID, Name: name, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateDuesTier(%d, %q) = %v, want no error", fundID, name, err)
	}
	return tier.ID
}

func TestEffectiveDuesRateFollowsAMidYearChange(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, fundID, "warga")

	// One-sided intervals: the July row ends the January row by existing.
	for _, r := range []struct {
		from   string
		amount int64
	}{{"2026-01", 25000}, {"2026-07", 30000}} {
		if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
			TierID: tierID, Amount: r.amount, EffectiveFrom: r.from, CreatedAt: 1,
		}); err != nil {
			t.Fatalf("CreateDuesRate(%s, %d) = %v, want no error", r.from, r.amount, err)
		}
	}

	tests := []struct {
		period string
		want   int64
	}{
		{"2026-01", 25000},
		{"2026-06", 25000}, // the month before the change still pays the old rate
		{"2026-07", 30000}, // the change applies from its own month
		{"2026-12", 30000},
		{"2027-03", 30000}, // no end date, so the latest rate runs forward forever
	}
	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			got, err := q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{
				TierID: tierID, EffectiveFrom: tt.period,
			})
			if err != nil {
				t.Fatalf("GetEffectiveDuesRate(%s) = %v, want no error", tt.period, err)
			}
			if got.Amount != tt.want {
				t.Errorf("GetEffectiveDuesRate(%s).Amount = %d, want %d", tt.period, got.Amount, tt.want)
			}
		})
	}

	// A period before every rate has no answer, and that is not an error state
	// the schema can express - it is simply no row.
	if _, err := q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{
		TierID: tierID, EffectiveFrom: "2025-12",
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetEffectiveDuesRate before the first rate = %v, want sql.ErrNoRows", err)
	}
}

func TestMemberCannotBorrowAnotherFundsTier(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	fundA := createFund(t, sqlDB, "Fund A", validSlug)
	fundB := createFund(t, sqlDB, "Fund B", "bcdefghijklmnopqrstuvw")
	tierB := createDuesTier(t, sqlDB, fundB, "warga")

	// The tier exists, so a single-column FK would have accepted this. Only the
	// composite (fund_id, tier_id) FK knows it belongs to the wrong fund.
	if _, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: fundA, Name: "Jane", TierID: &tierB, CreatedAt: 1,
	}); err == nil {
		t.Fatal("CreateMember with another fund's tier = nil error, want the composite FK to reject it")
	}

	tierA := createDuesTier(t, sqlDB, fundA, "warga")
	if _, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: fundA, Name: "Jane", TierID: &tierA, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateMember with its own fund's tier = %v, want no error", err)
	}

	// NULL tier_id is a member with no dues obligation, and SQLite's MATCH
	// SIMPLE leaves the composite FK satisfied.
	if _, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: fundA, Name: "Alex", TierID: nil, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateMember with a NULL tier_id = %v, want no error", err)
	}
}

func TestDuesTierNameIsUniquePerFund(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	fundA := createFund(t, sqlDB, "Fund A", validSlug)
	fundB := createFund(t, sqlDB, "Fund B", "bcdefghijklmnopqrstuvw")
	createDuesTier(t, sqlDB, fundA, "warga")

	if _, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{
		FundID: fundA, Name: "warga", CreatedAt: 1,
	}); err == nil {
		t.Fatal("duplicate tier name in the same fund = nil error, want UNIQUE (fund_id, name) to reject it")
	}

	if _, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{
		FundID: fundB, Name: "warga", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("the same tier name in a different fund = %v, want no error", err)
	}
}

func TestDateAndPeriodChecksRejectImpossibleValues(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, fundID, "warga")

	// date() validation, not LIKE '____-__-__': ADR-024 records that the LIKE
	// form accepts every one of these.
	for _, bad := range []string{"2026-13-45", "not-a-date", "2026-02-30", "2026-08"} {
		t.Run("joined_on/"+bad, func(t *testing.T) {
			d := bad
			if _, err := q.CreateMember(ctx, store.CreateMemberParams{
				FundID: fundID, Name: "Jane", JoinedOn: &d, CreatedAt: 1,
			}); err == nil {
				t.Errorf("CreateMember with joined_on %q = nil error, want the CHECK to reject it", bad)
			}
		})
	}

	valid := "2026-08-12"
	if _, err := q.CreateMember(ctx, store.CreateMemberParams{
		FundID: fundID, Name: "Jane", JoinedOn: &valid, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateMember with joined_on %q = %v, want no error", valid, err)
	}

	// 'YYYY-MM' periods: GLOB restricts to digits where LIKE's _ does not, and
	// date(p||'-01') rejects a month that does not exist.
	for _, bad := range []string{"2026-13", "2026-8", "202x-08", "2026-08-12", "aaaa-bb"} {
		t.Run("effective_from/"+bad, func(t *testing.T) {
			if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{
				TierID: tierID, Amount: 25000, EffectiveFrom: bad, CreatedAt: 1,
			}); err == nil {
				t.Errorf("CreateDuesRate with effective_from %q = nil error, want the CHECK to reject it", bad)
			}
		})
	}
}

func TestDuesRateAmountCannotBeNegative(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()

	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, fundID, "warga")

	if _, err := store.New(sqlDB).CreateDuesRate(ctx, store.CreateDuesRateParams{
		TierID: tierID, Amount: -1, EffectiveFrom: "2026-01", CreatedAt: 1,
	}); err == nil {
		t.Fatal("CreateDuesRate with a negative amount = nil error, want the CHECK to reject it")
	}
}

// A member is a name and the dates around it, nothing to reach them by: PRD
// section 6 ("Member ... no contact details") and section 8 (data
// minimization), CLAUDE.md rule 6. Uruni collects only names, amounts, dates
// and notes, so no member email, phone or other contact column may exist - the
// treasurer's own login email lives on "user", which is not a member.
//
// Read off the real migrated schema (PRAGMA table_info), so a column added to
// the one migration file fails here whether or not any query uses it. The
// pattern list is generous on purpose: Indonesian words for the same things
// (telepon, hp, alamat, kontak) and the usual messaging handles.
func TestMemberSchemaCarriesNoContactColumn(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)

	rows, err := sqlDB.Query(`SELECT name FROM pragma_table_info('member')`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(member) = %v, want no error", err)
	}
	defer func() { _ = rows.Close() }()

	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning a member column name: %v", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading member columns: %v", err)
	}

	// Guard against a vacuous pass: an empty or renamed table would satisfy
	// the loop below trivially.
	if len(columns) == 0 {
		t.Fatal("member has no columns, want the real table - was it renamed?")
	}
	hasName := false
	for _, c := range columns {
		if c == "name" {
			hasName = true
		}
	}
	if !hasName {
		t.Fatalf("member columns = %v, want a name column - the guard is reading the wrong table", columns)
	}

	// Long enough to be unambiguous anywhere in a column name, so
	// "phonenumber" and "cellphone" are caught as surely as "phone".
	forbiddenAnywhere := []string{
		"email", "mail", "phone", "telp", "telepon", "mobile", "whatsapp", "telegram",
		"contact", "kontak", "address", "alamat", "msisdn",
	}
	// Too short to match inside other words ("tel" in "hotel_id", "wa" in
	// "award"), so these count only as a whole underscore-separated word.
	forbiddenWords := []string{"tel", "hp", "cell", "wa", "nik", "ktp"}
	for _, c := range columns {
		lower := strings.ToLower(c)
		for _, sub := range forbiddenAnywhere {
			if strings.Contains(lower, sub) {
				t.Errorf("member column %q reads as contact data (%q): collect only names, amounts, dates and notes (PRD 6, 8)", c, sub)
			}
		}
		for _, part := range strings.Split(lower, "_") {
			if slices.Contains(forbiddenWords, part) {
				t.Errorf("member column %q reads as contact data (%q): collect only names, amounts, dates and notes (PRD 6, 8)", c, part)
			}
		}
	}
}
