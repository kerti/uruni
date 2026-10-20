package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/store"
)

// member_tier is ADR-040's effective-dated tier: one-sided month rows, a
// correction number within the month, a standing view, an UPDATE-only
// immutability trigger. These tests hold the schema half of it; the pricing
// reads are internal/ledger's.

func createJoinedMember(t *testing.T, sqlDB *sql.DB, fundID int64, name, joinedOn string) int64 {
	t.Helper()
	m, err := store.New(sqlDB).CreateMember(context.Background(), store.CreateMemberParams{
		FundID: fundID, Name: name, JoinedOn: joinedOn, CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember(%q) = %v, want no error", name, err)
	}
	return m.ID
}

func memberTier(fundID, memberID int64, tierID *int64, from string, correction int64) store.CreateMemberTierParams {
	return store.CreateMemberTierParams{
		FundID: fundID, MemberID: memberID, TierID: tierID, EffectiveFrom: from, Correction: correction, CreatedAt: 1,
	}
}

func TestMemberTierCannotBorrowAnotherFundsTierOrMember(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)

	fundA := createFund(t, sqlDB, "Fund A", validSlug)
	fundB := createFund(t, sqlDB, "Fund B", "bcdefghijklmnopqrstuvw")
	tierA := createDuesTier(t, sqlDB, fundA, "warga")
	tierB := createDuesTier(t, sqlDB, fundB, "warga")
	jane := createJoinedMember(t, sqlDB, fundA, "Jane", "2026-01-01")

	// The tier exists, so a single-column FK would accept this; only the composite
	// (fund_id, tier_id) FK knows it belongs to the wrong fund.
	if _, err := q.CreateMemberTier(ctx, memberTier(fundA, jane, &tierB, "2026-01", 0)); err == nil {
		t.Error("member_tier naming another fund's tier = nil error, want the composite FK to reject it")
	}
	// Same for the member: it exists, but not in fund B.
	if _, err := q.CreateMemberTier(ctx, memberTier(fundB, jane, &tierB, "2026-01", 0)); err == nil {
		t.Error("member_tier naming another fund's member = nil error, want the composite FK to reject it")
	}
	if _, err := q.CreateMemberTier(ctx, memberTier(fundA, jane, &tierA, "2026-01", 0)); err != nil {
		t.Errorf("member_tier with its own fund's tier and member = %v, want no error", err)
	}
	// NULL tier_id is "no tier from this month", and SQLite's MATCH SIMPLE leaves
	// the composite FK satisfied.
	if _, err := q.CreateMemberTier(ctx, memberTier(fundA, jane, nil, "2026-06", 0)); err != nil {
		t.Errorf("member_tier with a NULL tier_id = %v, want no error", err)
	}
}

func TestMemberTierStartMonthIsAMonthNoEarlierThan2000(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, fundID, "warga")
	memberID := createJoinedMember(t, sqlDB, fundID, "Jane", "2026-01-01")

	for _, tt := range []struct {
		from    string
		wantErr bool
	}{
		{"2000-01", false}, // the floor
		{"1999-12", true},  // one step below it
		{"2026-13", true},  // not a calendar month
		{"2026-1", true},
		{"2026-01-15", true}, // a date, not a month
		{"", true},
	} {
		_, err := q.CreateMemberTier(ctx, memberTier(fundID, memberID, &tierID, tt.from, 0))
		if (err != nil) != tt.wantErr {
			t.Errorf("effective_from %q: err = %v, wantErr %v", tt.from, err, tt.wantErr)
		}
	}
}

func TestMemberTierMonthHoldsOneRowPerCorrectionAndNoNegativeOnes(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, fundID, "warga")
	memberID := createJoinedMember(t, sqlDB, fundID, "Jane", "2026-01-01")

	if _, err := q.CreateMemberTier(ctx, memberTier(fundID, memberID, &tierID, "2026-01", 0)); err != nil {
		t.Fatalf("first row = %v, want no error", err)
	}
	if _, err := q.CreateMemberTier(ctx, memberTier(fundID, memberID, &tierID, "2026-01", 0)); err == nil {
		t.Error("the same member, month and correction twice = nil error, want UNIQUE to reject it")
	}
	if _, err := q.CreateMemberTier(ctx, memberTier(fundID, memberID, &tierID, "2026-01", 1)); err != nil {
		t.Errorf("the next correction of the month = %v, want no error", err)
	}
	if _, err := q.CreateMemberTier(ctx, memberTier(fundID, memberID, &tierID, "2026-02", -1)); err == nil {
		t.Error("a negative correction = nil error, want the CHECK to reject it")
	}
}

func TestMemberTierStandingViewKeepsTheHighestCorrectionOfEachMonth(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	tierA := createDuesTier(t, sqlDB, fundID, "A")
	tierB := createDuesTier(t, sqlDB, fundID, "B")
	tierC := createDuesTier(t, sqlDB, fundID, "C")
	memberID := createJoinedMember(t, sqlDB, fundID, "Jane", "2026-01-01")
	other := createJoinedMember(t, sqlDB, fundID, "Joko", "2026-01-01")

	// Written out of order on purpose: the standing row is the highest correction,
	// never the latest id or created_at.
	for _, r := range []store.CreateMemberTierParams{
		memberTier(fundID, memberID, &tierC, "2026-01", 2),
		memberTier(fundID, memberID, &tierA, "2026-01", 0),
		memberTier(fundID, memberID, &tierB, "2026-01", 1),
		memberTier(fundID, memberID, nil, "2026-06", 0), // no tier from June...
		memberTier(fundID, memberID, &tierA, "2026-06", 1),
		memberTier(fundID, other, &tierA, "2026-01", 0),
	} {
		if _, err := q.CreateMemberTier(ctx, r); err != nil {
			t.Fatalf("CreateMemberTier(%s #%d) = %v, want no error", r.EffectiveFrom, r.Correction, err)
		}
	}

	rows, err := q.ListMemberTiersForMember(ctx, store.ListMemberTiersForMemberParams{FundID: fundID, MemberID: memberID})
	if err != nil {
		t.Fatalf("ListMemberTiersForMember = %v, want no error", err)
	}
	type standing struct {
		from       string
		tier       int64
		correction int64
	}
	var got []standing
	for _, r := range rows {
		if r.TierID == nil {
			t.Fatalf("%s stands as NULL, want the corrected tier", r.EffectiveFrom)
		}
		got = append(got, standing{r.EffectiveFrom, *r.TierID, r.Correction})
	}
	want := []standing{{"2026-01", tierC, 2}, {"2026-06", tierA, 1}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("standing rows = %+v, want %+v (one per month, the highest correction, oldest first)", got, want)
	}

	all, err := q.ListMemberTiersByFund(ctx, fundID)
	if err != nil {
		t.Fatalf("ListMemberTiersByFund = %v, want no error", err)
	}
	if len(all) != 3 {
		t.Errorf("ListMemberTiersByFund returned %d standing rows, want 3 (two for Jane, one for Joko)", len(all))
	}

	export, err := q.ListMemberTiersForExport(ctx, fundID)
	if err != nil {
		t.Fatalf("ListMemberTiersForExport = %v, want no error", err)
	}
	if len(export) != 6 {
		t.Errorf("ListMemberTiersForExport returned %d rows, want all 6 (superseded ones are the trail)", len(export))
	}

	// A NULL tier that stands is a row like any other: the month says "no tier".
	if _, err := q.CreateMemberTier(ctx, memberTier(fundID, other, nil, "2026-03", 0)); err != nil {
		t.Fatalf("CreateMemberTier(NULL) = %v, want no error", err)
	}
	rows, err = q.ListMemberTiersForMember(ctx, store.ListMemberTiersForMemberParams{FundID: fundID, MemberID: other})
	if err != nil {
		t.Fatalf("ListMemberTiersForMember = %v, want no error", err)
	}
	if len(rows) != 2 || rows[1].TierID != nil {
		t.Errorf("rows = %+v, want the March row to stand as no tier", rows)
	}

	latest, err := q.LatestMemberTierCorrection(ctx, store.LatestMemberTierCorrectionParams{MemberID: memberID, EffectiveFrom: "2026-01"})
	if err != nil || latest != 2 {
		t.Errorf("LatestMemberTierCorrection(Jan) = %d, %v, want 2", latest, err)
	}
	latest, err = q.LatestMemberTierCorrection(ctx, store.LatestMemberTierCorrectionParams{MemberID: memberID, EffectiveFrom: "2026-09"})
	if err != nil || latest != -1 {
		t.Errorf("LatestMemberTierCorrection(a month with no row) = %d, %v, want -1", latest, err)
	}
}

func TestMemberTierRowsRefuseAnUpdate(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, fundID, "warga")
	memberID := createJoinedMember(t, sqlDB, fundID, "Jane", "2026-01-01")
	row, err := q.CreateMemberTier(ctx, memberTier(fundID, memberID, &tierID, "2026-01", 0))
	if err != nil {
		t.Fatalf("CreateMemberTier = %v, want no error", err)
	}

	const want = "member_tier rows are immutable"
	for _, stmt := range []string{
		`UPDATE member_tier SET tier_id = NULL WHERE id = ?`,
		`UPDATE member_tier SET effective_from = '2026-02' WHERE id = ?`,
		`UPDATE member_tier SET correction = 1 WHERE id = ?`,
	} {
		_, err := sqlDB.ExecContext(ctx, stmt, row.ID)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s = %v, want an error containing %q", stmt, err, want)
		}
	}
}

func TestDeletingAMemberTakesItsTierRowsButAPostedRowKeepsThem(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	f := newScenarioFund(t, sqlDB, "Test Fund", validSlug)
	tierID := createDuesTier(t, sqlDB, f.fundID, "warga")

	dup := createJoinedMember(t, sqlDB, f.fundID, "Jane (dua kali)", "2026-01-01")
	paid := createJoinedMember(t, sqlDB, f.fundID, "Jane", "2026-01-01")
	for _, m := range []int64{dup, paid} {
		if _, err := q.CreateMemberTier(ctx, memberTier(f.fundID, m, &tierID, "2026-01", 0)); err != nil {
			t.Fatalf("CreateMemberTier = %v, want no error", err)
		}
	}
	period := "2026-01"
	f.entry(t, sqlDB, store.CreateTransactionParams{
		AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 25_000,
		OccurredOn: "2026-01-05", Kind: "dues", MemberID: &paid, DuesPeriod: &period,
	})

	count := func(memberID int64) int {
		var n int
		if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM member_tier WHERE member_id = ?`, memberID).Scan(&n); err != nil {
			t.Fatalf("counting member_tier: %v", err)
		}
		return n
	}

	if err := q.DeleteMember(ctx, dup); err != nil {
		t.Fatalf("DeleteMember(a duplicate nobody paid) = %v, want no error", err)
	}
	if n := count(dup); n != 0 {
		t.Errorf("the deleted member still has %d member_tier rows, want the cascade to have taken them", n)
	}

	if err := q.DeleteMember(ctx, paid); err == nil {
		t.Fatal("DeleteMember(a member a payment names) = nil error, want the foreign key to refuse")
	}
	if n := count(paid); n != 1 {
		t.Errorf("a refused delete left %d member_tier rows, want its 1 untouched", n)
	}
}

func TestATierAnyMemberTierRowEverNamedCannotBeDeleted(t *testing.T) {
	t.Parallel()
	sqlDB := migratedTestDB(t)
	ctx := context.Background()
	q := store.New(sqlDB)
	fundID := createFund(t, sqlDB, "Test Fund", validSlug)
	named := createDuesTier(t, sqlDB, fundID, "salah ketik")
	current := createDuesTier(t, sqlDB, fundID, "benar")
	untouched := createDuesTier(t, sqlDB, fundID, "tidak pernah dipakai")
	memberID := createJoinedMember(t, sqlDB, fundID, "Jane", "2026-01-01")

	// "salah ketik" was put on Jane by a slip and corrected away: only a
	// superseded row names it now.
	for _, r := range []store.CreateMemberTierParams{
		memberTier(fundID, memberID, &named, "2026-01", 0),
		memberTier(fundID, memberID, &current, "2026-01", 1),
	} {
		if _, err := q.CreateMemberTier(ctx, r); err != nil {
			t.Fatalf("CreateMemberTier = %v, want no error", err)
		}
	}

	if err := q.DeleteDuesTier(ctx, store.DeleteDuesTierParams{ID: named, FundID: fundID}); err == nil {
		t.Error("deleting a tier only a superseded row names = nil error, want the foreign key to refuse")
	}
	if err := q.DeleteDuesTier(ctx, store.DeleteDuesTierParams{ID: current, FundID: fundID}); err == nil {
		t.Error("deleting a tier a standing row names = nil error, want the foreign key to refuse")
	}
	if err := q.DeleteDuesTier(ctx, store.DeleteDuesTierParams{ID: untouched, FundID: fundID}); err != nil {
		t.Errorf("deleting a tier nobody was ever in = %v, want no error", err)
	}
}

// queryStatements reads internal/store/queries and returns each named query's
// SQL with its comments stripped, keyed by name.
func queryStatements(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "store", "queries", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("finding the query files: %v (%d found)", err, len(files))
	}
	nameLine := regexp.MustCompile(`^-- name: (\w+) :\w+`)
	out := map[string]string{}
	for _, file := range files {
		raw, err := os.ReadFile(file) //nolint:gosec // a path globbed from this repo's own queries directory
		if err != nil {
			t.Fatal(err)
		}
		var name string
		var body strings.Builder
		flush := func() {
			if name != "" {
				out[name] = body.String()
			}
			body.Reset()
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if m := nameLine.FindStringSubmatch(line); m != nil {
				flush()
				name = m[1]
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			body.WriteString(line + "\n")
		}
		flush()
	}
	return out
}

// TestNoQueryDeletesMemberTierRowsExceptRestoresClearAll is ADR-040's stand-in
// for a BEFORE DELETE trigger: member_tier rows are a record, so the only
// statement allowed to delete from the table is restore's clear-all. (A member's
// deletion and a never-named tier's reach the table through the foreign key, not
// through a statement of their own.)
func TestNoQueryDeletesMemberTierRowsExceptRestoresClearAll(t *testing.T) {
	t.Parallel()
	deletes := regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+"?member_tier"?\b`)
	allowed := map[string]bool{"DeleteAllMemberTiers": true}
	for name, body := range queryStatements(t) {
		if deletes.MatchString(body) && !allowed[name] {
			t.Errorf("query %s deletes from member_tier: a correction is a new row, and only restore's clear-all may delete", name)
		}
	}
	if !deletes.MatchString(queryStatements(t)["DeleteAllMemberTiers"]) {
		t.Error("DeleteAllMemberTiers no longer deletes from member_tier: the guard is looking at the wrong query")
	}
}

// TestPricingReadsOfMemberTierGoThroughTheStandingView is the guard ADR-040
// asks for: a query that read the member_tier table for a tier (rather than the
// member_tier_standing view) would see a superseded row win its month. The
// named exceptions are the reads that need every row.
func TestPricingReadsOfMemberTierGoThroughTheStandingView(t *testing.T) {
	t.Parallel()
	reads := regexp.MustCompile(`(?i)\b(FROM|JOIN)\s+"?member_tier"?(\s|$|\))`)
	exceptions := map[string]string{
		"LatestMemberTierCorrection": "the next-correction read must see superseded rows",
		"ListMemberTiersForExport":   "the backup export carries every row",
	}
	for name, body := range queryStatements(t) {
		if reads.MatchString(body) {
			if _, ok := exceptions[name]; !ok {
				t.Errorf("query %s reads the member_tier table; read member_tier_standing so a superseded row cannot win a month", name)
			}
		}
	}
	for name := range exceptions {
		if !reads.MatchString(queryStatements(t)[name]) {
			t.Errorf("the named exception %s no longer reads member_tier: drop it from the list", name)
		}
	}

	// The roster's page reads the tier through the view as well.
	if !strings.Contains(queryStatements(t)["ListMembersPage"], "member_tier_standing") {
		t.Error("ListMembersPage does not read member_tier_standing")
	}
}
