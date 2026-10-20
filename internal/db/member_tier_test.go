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

// versionedTable is a table ADR-040 makes append-only: its rows are a record, a
// mistake is a new row, and a view says which row stands. The guard tests below
// hold both tables to the same convention.
type versionedTable struct {
	table string
	view  string
	// deleters are the only statements allowed to delete from the table: restore's
	// clear-all, and for dues_rate a never-named tier's deletion. (A member's
	// deletion reaches member_tier through the foreign key, not a statement of its
	// own.)
	deleters map[string]string
	// viewExceptions are the only statements allowed to read the table itself: they
	// need a row the view hides.
	viewExceptions map[string]string
}

var versionedTables = []versionedTable{
	{
		table: "member_tier", view: "member_tier_standing",
		deleters: map[string]string{"DeleteAllMemberTiers": "restore's clear-all"},
		viewExceptions: map[string]string{
			"LatestMemberTierCorrection": "the next-correction read must see superseded rows",
			"ListMemberTiersForExport":   "the backup export carries every row",
		},
	},
	{
		table: "dues_rate", view: "dues_rate_standing",
		deleters: map[string]string{
			"DeleteAllDuesRates":    "restore's clear-all",
			"DeleteDuesRatesByTier": "a tier nobody was ever in goes with its rates",
		},
		viewExceptions: map[string]string{
			"GetDuesRateForFund":    "PATCH and DELETE look the row up by id, and must find one that no longer stands to refuse it",
			"LatestDuesRateOfMonth": "the next-correction read must see superseded rows and deleting ones",
			"ListDuesRatesByFund":   "the backup export carries every row",
		},
	},
}

// TestNoQueryDeletesVersionedRowsExceptTheNamedOnes is ADR-040's stand-in for a
// BEFORE DELETE trigger: member_tier and dues_rate rows are a record, so the only
// statements allowed to delete from them are the ones named above.
func TestNoQueryDeletesVersionedRowsExceptTheNamedOnes(t *testing.T) {
	t.Parallel()
	queries := queryStatements(t)
	for _, vt := range versionedTables {
		deletes := regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+"?` + vt.table + `"?\b`)
		for name, body := range queries {
			if _, ok := vt.deleters[name]; deletes.MatchString(body) && !ok {
				t.Errorf("query %s deletes from %s: a correction is a new row, and only %v may delete", name, vt.table, vt.deleters)
			}
		}
		for name := range vt.deleters {
			if !deletes.MatchString(queries[name]) {
				t.Errorf("%s no longer deletes from %s: the guard is looking at the wrong query", name, vt.table)
			}
		}
	}
}

// TestNoQueryUpdatesVersionedRows is the static half of the UPDATE triggers: a
// query that updated either table would be caught by the trigger at run time, and
// here before it ever runs.
func TestNoQueryUpdatesVersionedRows(t *testing.T) {
	t.Parallel()
	for _, vt := range versionedTables {
		updates := regexp.MustCompile(`(?i)\bUPDATE\s+"?` + vt.table + `"?\b`)
		for name, body := range queryStatements(t) {
			if updates.MatchString(body) {
				t.Errorf("query %s updates %s: a correction is a new row", name, vt.table)
			}
		}
	}
}

// tableReadPattern matches a query that reads table itself (not a view named
// after it): after FROM or JOIN, or as a comma-joined table, with the name
// ending at whitespace, a semicolon, a comma, a closing parenthesis or the end.
// "dues_rate_standing" and "dues_rate.id" do not match "dues_rate".
func tableReadPattern(table string) *regexp.Regexp {
	const end = `(\s|;|,|$|\))`
	return regexp.MustCompile(`(?i)(\b(FROM|JOIN)\s+|,\s*)"?` + table + `"?` + end)
}

// TestTableReadPatternSeesEveryShapeOfARead pins the guard's own regex: a
// pattern that missed "FROM t;" or a comma join would let a raw read of a
// versioned table pass the guard silently.
func TestTableReadPatternSeesEveryShapeOfARead(t *testing.T) {
	t.Parallel()
	re := tableReadPattern("dues_rate")
	for _, reads := range []string{
		"SELECT * FROM dues_rate",
		"SELECT * FROM dues_rate;",
		"SELECT * FROM dues_rate WHERE id = 1",
		"SELECT * FROM dues_rate dr",
		"SELECT * FROM dues_rate\nWHERE id = 1",
		`SELECT * FROM "dues_rate"`,
		"SELECT * FROM dues_tier, dues_rate",
		"SELECT * FROM dues_tier,dues_rate WHERE 1",
		"SELECT * FROM dues_tier, dues_rate;",
		"SELECT * FROM dues_tier JOIN dues_rate ON 1",
		"SELECT * FROM (SELECT 1 FROM dues_rate)",
		"select * from dues_rate",
	} {
		if !re.MatchString(reads) {
			t.Errorf("pattern misses a read of the table: %q", reads)
		}
	}
	for _, notReads := range []string{
		"SELECT * FROM dues_rate_standing",
		"SELECT * FROM dues_rate_standing;",
		"SELECT * FROM dues_tier, dues_rate_standing",
		"SELECT dues_rate.id FROM dues_tier",
		"SELECT * FROM dues_tier WHERE a = b, c",
		"INSERT INTO dues_rate (id) VALUES (1)",
	} {
		if re.MatchString(notReads) {
			t.Errorf("pattern flags something that is not a read of the table: %q", notReads)
		}
	}
}

// TestPricingReadsGoThroughTheStandingViews is the guard ADR-040 asks for: a query
// that read the table for a tier or a rate (rather than its standing view) would
// see a superseded row win its month, or a deleted rate keep pricing. The named
// exceptions are the reads that need every row.
func TestPricingReadsGoThroughTheStandingViews(t *testing.T) {
	t.Parallel()
	queries := queryStatements(t)
	for _, vt := range versionedTables {
		reads := tableReadPattern(vt.table)
		for name, body := range queries {
			// A DELETE names the table in its FROM; the delete guard above holds those.
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(body)), "DELETE") {
				continue
			}
			if reads.MatchString(body) {
				if _, ok := vt.viewExceptions[name]; !ok {
					t.Errorf("query %s reads the %s table; read %s so a superseded row cannot win a month", name, vt.table, vt.view)
				}
			}
		}
		for name := range vt.viewExceptions {
			if !reads.MatchString(queries[name]) {
				t.Errorf("the named exception %s no longer reads %s: drop it from the list", name, vt.table)
			}
		}
	}

	// The reads the rate work repointed, and the roster's page, read the views.
	for name, view := range map[string]string{
		"ListMembersPage":               "member_tier_standing",
		"ListDuesRatesForPricing":       "dues_rate_standing",
		"ListDuesRatesForMemberPricing": "dues_rate_standing",
		"GetEffectiveDuesRate":          "dues_rate_standing",
		"ListDuesRatesByTier":           "dues_rate_standing",
	} {
		if !strings.Contains(queries[name], view) {
			t.Errorf("%s does not read %s", name, view)
		}
	}
	if !strings.Contains(queries["ListMembersPage"], "dues_rate_standing") {
		t.Error("ListMembersPage's current_rate does not read dues_rate_standing")
	}
}
