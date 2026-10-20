package ledger

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// --- rate corrections leave a trail (ADR-040) --------------------------------
//
// One tier, Pelaksana, and one member who joined in January 2026 on it. Each test
// writes rates through the ledger's three verbs (create, correct, delete) and
// asks what a period reads, in the status roster, the outstanding walk, the
// roster page's current_rate and GetEffectiveDuesRate.

type rateFixture struct {
	l        *Ledger
	f        fixture
	q        *store.Queries
	tier     int64
	memberID int64
}

func newRateFixture(t *testing.T) rateFixture {
	t.Helper()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	tier := createDuesTier(t, q, f.fundID, "Pelaksana")
	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Andi", tierID: &tier, joinedOn: &joinedOn})
	return rateFixture{l: l, f: f, q: q, tier: tier, memberID: memberID}
}

func (h rateFixture) create(t *testing.T, amount money.Amount, from string) store.DuesRate {
	t.Helper()
	r, err := h.l.CreateDuesRate(context.Background(), CreateDuesRateParams{FundID: h.f.fundID, TierID: h.tier, Amount: amount, EffectiveFrom: from})
	if err != nil {
		t.Fatalf("CreateDuesRate(%d from %s) = %v, want no error", amount, from, err)
	}
	return r
}

func (h rateFixture) correct(t *testing.T, id int64, amount money.Amount) store.DuesRate {
	t.Helper()
	r, err := h.l.CorrectDuesRate(context.Background(), h.f.fundID, id, amount)
	if err != nil {
		t.Fatalf("CorrectDuesRate(%d, %d) = %v, want no error", id, amount, err)
	}
	return r
}

func (h rateFixture) remove(t *testing.T, id int64) {
	t.Helper()
	if err := h.l.DeleteDuesRate(context.Background(), h.f.fundID, id); err != nil {
		t.Fatalf("DeleteDuesRate(%d) = %v, want no error", id, err)
	}
}

// owed is what the roster says the member owes for period, and whether the
// member is listed at all (a period with no decided rate lists nobody).
func (h rateFixture) owed(t *testing.T, period string) (int64, bool) {
	t.Helper()
	rows, err := h.l.DuesStatusForPeriod(context.Background(), h.f.fundID, period)
	if err != nil {
		t.Fatalf("DuesStatusForPeriod(%s) = %v, want no error", period, err)
	}
	s, ok := statusFor(t, rows, h.memberID)
	return s.OwedAmount.Int64(), ok
}

func (h rateFixture) rowCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.l.db.QueryRow(`SELECT COUNT(*) FROM dues_rate WHERE tier_id = ?`, h.tier).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (h rateFixture) transactionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.l.db.QueryRow(`SELECT COUNT(*) FROM "transaction"`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCorrectDuesRateRepricesEveryPeriodItCoversAndLeavesTheOldRow(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	jan := h.create(t, 10_000, "2026-01")
	h.create(t, 20_000, "2026-06")
	if _, err := h.l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: h.f.fundID, AccountID: h.f.cashID, MemberID: h.memberID, OccurredOn: "2026-10-01",
		Periods: []PeriodAmount{{DuesPeriod: "2026-02", Amount: 10_000}, {DuesPeriod: "2026-03", Amount: 10_000}},
	}); err != nil {
		t.Fatal(err)
	}
	postedBefore := h.transactionCount(t)

	corrected := h.correct(t, jan.ID, 12_000)

	if corrected.ID == jan.ID || corrected.Correction != 1 || corrected.EffectiveFrom != "2026-01" || corrected.Deleted != 0 || corrected.Amount != 12_000 {
		t.Errorf("corrected = %+v, want a NEW row for 2026-01 at correction 1, amount 12000", corrected)
	}
	// January through May are repriced; June onward is the second row's and is not.
	for _, p := range []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05"} {
		if owed, ok := h.owed(t, p); !ok || owed != 12_000 {
			t.Errorf("%s owed = %d listed %v, want 12000 after the correction", p, owed, ok)
		}
	}
	if owed, _ := h.owed(t, "2026-06"); owed != 20_000 {
		t.Errorf("2026-06 owed = %d, want 20000: the correction covers only up to the next month's row", owed)
	}
	// What was paid does not move: February, paid in full at 10000, now reads partial.
	rows, err := h.l.DuesStatusForPeriod(context.Background(), h.f.fundID, "2026-02")
	if err != nil {
		t.Fatal(err)
	}
	if feb, _ := statusFor(t, rows, h.memberID); feb.Status != DuesStatusPartial || feb.PaidAmount != 10_000 {
		t.Errorf("February = %s paid %d, want partial with the 10000 still paid", feb.Status, feb.PaidAmount)
	}
	if n := h.transactionCount(t); n != postedBefore {
		t.Errorf("posted rows = %d, want %d: a correction moves what is owed, never what was paid", n, postedBefore)
	}

	// The original is retained: both rows are in the table, only the correction is in force.
	if n := h.rowCount(t); n != 3 {
		t.Errorf("dues_rate rows = %d, want 3 (the original, its correction, and June)", n)
	}
	listed, err := h.q.ListDuesRatesByTier(context.Background(), h.tier)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != corrected.ID || listed[0].Amount != 12_000 {
		t.Errorf("ListDuesRatesByTier = %+v, want the corrected January row and June, not the superseded original", listed)
	}
	effective, err := h.q.GetEffectiveDuesRate(context.Background(), store.GetEffectiveDuesRateParams{TierID: h.tier, EffectiveFrom: "2026-04"})
	if err != nil || effective.ID != corrected.ID {
		t.Errorf("GetEffectiveDuesRate(2026-04) = %+v, %v, want the corrected row %d", effective, err, corrected.ID)
	}
}

func TestCorrectDuesRateChainsCorrectionNumbersAndTheHighestStands(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	r := h.create(t, 10_000, "2026-01")
	for want := int64(1); want <= 3; want++ {
		r = h.correct(t, r.ID, money.Amount(10_000+want*1_000))
		if r.Correction != want {
			t.Fatalf("correction number = %d, want %d", r.Correction, want)
		}
	}
	if owed, _ := h.owed(t, "2026-03"); owed != 13_000 {
		t.Errorf("owed = %d, want 13000, the third correction", owed)
	}
}

func TestCorrectDuesRateWithTheStandingAmountWritesNothing(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	jan := h.create(t, 10_000, "2026-01")

	got := h.correct(t, jan.ID, 10_000)
	if got.ID != jan.ID || got.Correction != 0 {
		t.Errorf("a no-op correction answered %+v, want the standing row %d unchanged", got, jan.ID)
	}
	if n := h.rowCount(t); n != 1 {
		t.Errorf("dues_rate rows = %d, want 1: re-saving the form leaves no stray row", n)
	}

	// After a real correction the standing amount is the corrected one.
	c := h.correct(t, jan.ID, 12_000)
	if got := h.correct(t, c.ID, 12_000); got.ID != c.ID {
		t.Errorf("repeat of the corrected amount answered %+v, want row %d", got, c.ID)
	}
	if n := h.rowCount(t); n != 2 {
		t.Errorf("dues_rate rows = %d, want 2", n)
	}
}

func TestCorrectAndDeleteRefuseARowThatNoLongerStands(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	jan := h.create(t, 10_000, "2026-01")
	h.correct(t, jan.ID, 12_000) // jan.ID is superseded now
	jun := h.create(t, 20_000, "2026-06")
	h.remove(t, jun.ID) // jun.ID is superseded by a deletion, and the deleting row is no rate

	rowsBefore := h.rowCount(t)
	var deletingID int64
	if err := h.l.db.QueryRow(`SELECT id FROM dues_rate WHERE tier_id = ? AND deleted = 1`, h.tier).Scan(&deletingID); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]int64{"a superseded row": jan.ID, "a withdrawn rate": jun.ID, "the deleting row itself": deletingID} {
		if _, err := h.l.CorrectDuesRate(context.Background(), h.f.fundID, id, 99_000); !errors.Is(err, ErrDuesRateSuperseded) {
			t.Errorf("CorrectDuesRate(%s) = %v, want ErrDuesRateSuperseded", name, err)
		}
		if err := h.l.DeleteDuesRate(context.Background(), h.f.fundID, id); !errors.Is(err, ErrDuesRateSuperseded) {
			t.Errorf("DeleteDuesRate(%s) = %v, want ErrDuesRateSuperseded", name, err)
		}
	}
	if n := h.rowCount(t); n != rowsBefore {
		t.Errorf("dues_rate rows = %d, want %d: a refused edit writes nothing, not even onto the successor", n, rowsBefore)
	}
	if owed, _ := h.owed(t, "2026-03"); owed != 12_000 {
		t.Errorf("owed = %d, want the successor's 12000 untouched", owed)
	}
}

func TestDeletingALaterRateFallsBackToTheEarlierMonthsRate(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	h.create(t, 50_000, "2026-01")
	jun := h.create(t, 70_000, "2026-06")

	if owed, _ := h.owed(t, "2026-07"); owed != 70_000 {
		t.Fatalf("before the delete 2026-07 owes %d, want 70000", owed)
	}
	h.remove(t, jun.ID)

	// The naive read (drop deleted rows, then take the latest) would still find June's
	// own 70000 here; the standing row of June is the deletion, so June has no rate and
	// the periods fall back to January's.
	for _, p := range []string{"2026-05", "2026-06", "2026-07", "2026-12"} {
		if owed, ok := h.owed(t, p); !ok || owed != 50_000 {
			t.Errorf("%s owed = %d listed %v, want 50000 (January's rate): the deleted June is no rate", p, owed, ok)
		}
	}
	effective, err := h.q.GetEffectiveDuesRate(context.Background(), store.GetEffectiveDuesRateParams{TierID: h.tier, EffectiveFrom: "2026-06"})
	if err != nil || effective.Amount != 50_000 {
		t.Errorf("GetEffectiveDuesRate(2026-06) = %+v, %v, want January's 50000", effective, err)
	}
	page, err := h.q.ListMembersPage(context.Background(), store.ListMembersPageParams{CurrentPeriod: "2026-07", FundID: h.f.fundID, PageLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var andiRate *int64
	for _, row := range page {
		if row.ID == h.memberID {
			andiRate = row.CurrentRate
		}
	}
	if andiRate == nil || *andiRate != 50_000 {
		t.Errorf("roster current_rate = %v, want 50000", andiRate)
	}
	out, err := h.l.OutstandingDuesForMember(context.Background(), h.f.fundID, h.memberID, "2026-07", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range out {
		if p.OwedAmount != 50_000 {
			t.Errorf("outstanding %s owes %d, want 50000", p.Period, p.OwedAmount)
		}
	}
	listed, err := h.q.ListDuesRatesByTier(context.Background(), h.tier)
	if err != nil || len(listed) != 1 || listed[0].EffectiveFrom != "2026-01" {
		t.Errorf("ListDuesRatesByTier = %+v, %v, want January only: a deleted month does not show", listed, err)
	}
}

func TestDeletingTheFirstRateLeavesTheTierUndecided(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	jan := h.create(t, 500_000, "2026-01") // a typo for 50000, filed against the wrong month too
	h.remove(t, jan.ID)

	if _, ok := h.owed(t, "2026-03"); ok {
		t.Error("2026-03 lists the member, want no rate decided (the madya case): skipped, never invented")
	}
	out, err := h.l.OutstandingDuesForMember(context.Background(), h.f.fundID, h.memberID, "2026-05", time.Time{})
	if err != nil || len(out) != 0 {
		t.Errorf("outstanding = %+v, %v, want none", out, err)
	}
	if _, err := h.q.GetEffectiveDuesRate(context.Background(), store.GetEffectiveDuesRateParams{TierID: h.tier, EffectiveFrom: "2026-03"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetEffectiveDuesRate = %v, want sql.ErrNoRows", err)
	}
}

func TestAWrongMonthRateIsCorrectedByDeletingItAndAddingTheRightMonth(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	h.create(t, 10_000, "2026-01")
	wrong := h.create(t, 20_000, "2026-04") // meant for June

	h.remove(t, wrong.ID)
	h.create(t, 20_000, "2026-06")

	for p, want := range map[string]int64{"2026-04": 10_000, "2026-05": 10_000, "2026-06": 20_000} {
		if owed, _ := h.owed(t, p); owed != want {
			t.Errorf("%s owed = %d, want %d", p, owed, want)
		}
	}
}

func TestAReAddAtTheSameMonthAfterADeleteIsTheNextCorrection(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	h.create(t, 10_000, "2026-01")
	jun := h.create(t, 20_000, "2026-06")
	h.remove(t, jun.ID)

	again := h.create(t, 25_000, "2026-06")
	if again.Correction != 2 || again.Deleted != 0 {
		t.Errorf("re-added = %+v, want correction 2 (0 filed, 1 deleted, 2 re-added), not deleted", again)
	}
	if owed, _ := h.owed(t, "2026-07"); owed != 25_000 {
		t.Errorf("2026-07 owed = %d, want 25000", owed)
	}
	// ...and it can be corrected and deleted again: the numbers never collide.
	h.correct(t, again.ID, 26_000)
	var standing int64
	if err := h.l.db.QueryRow(`SELECT id FROM dues_rate_standing WHERE tier_id = ? AND effective_from = '2026-06'`, h.tier).Scan(&standing); err != nil {
		t.Fatal(err)
	}
	h.remove(t, standing)
	if owed, _ := h.owed(t, "2026-07"); owed != 10_000 {
		t.Errorf("after deleting again 2026-07 owed = %d, want January's 10000", owed)
	}
}

func TestCreateDuesRateIntoAMonthThatHasARateIsRefused(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	jan := h.create(t, 10_000, "2026-01")

	_, err := h.l.CreateDuesRate(context.Background(), CreateDuesRateParams{FundID: h.f.fundID, TierID: h.tier, Amount: 11_000, EffectiveFrom: "2026-01"})
	if !errors.Is(err, ErrDuesRateMonthTaken) {
		t.Errorf("CreateDuesRate into a month with a rate = %v, want ErrDuesRateMonthTaken", err)
	}
	// Also after a correction: the standing row is the corrected one, not row 0.
	h.correct(t, jan.ID, 12_000)
	_, err = h.l.CreateDuesRate(context.Background(), CreateDuesRateParams{FundID: h.f.fundID, TierID: h.tier, Amount: 11_000, EffectiveFrom: "2026-01"})
	if !errors.Is(err, ErrDuesRateMonthTaken) {
		t.Errorf("CreateDuesRate into a corrected month = %v, want ErrDuesRateMonthTaken", err)
	}
	if n := h.rowCount(t); n != 2 {
		t.Errorf("dues_rate rows = %d, want 2: a refused add writes nothing", n)
	}
}

func TestRateWritesStillMeetTheCeilingAndTheSchemaChecks(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	jan := h.create(t, 10_000, "2026-01")
	rows := h.rowCount(t)

	over := money.MaxAmount + 1
	if _, err := h.l.CreateDuesRate(context.Background(), CreateDuesRateParams{FundID: h.f.fundID, TierID: h.tier, Amount: over, EffectiveFrom: "2026-06"}); !errors.Is(err, ErrAmountTooLarge) {
		t.Errorf("CreateDuesRate above the ceiling = %v, want ErrAmountTooLarge", err)
	}
	if _, err := h.l.CorrectDuesRate(context.Background(), h.f.fundID, jan.ID, over); !errors.Is(err, ErrAmountTooLarge) {
		t.Errorf("CorrectDuesRate above the ceiling = %v, want ErrAmountTooLarge", err)
	}
	// The schema CHECKs (#622) are what refuse a negative amount and a malformed month.
	if _, err := h.l.CorrectDuesRate(context.Background(), h.f.fundID, jan.ID, -1); err == nil {
		t.Error("CorrectDuesRate with a negative amount = nil error, want the CHECK to refuse")
	}
	if _, err := h.l.CreateDuesRate(context.Background(), CreateDuesRateParams{FundID: h.f.fundID, TierID: h.tier, Amount: 1, EffectiveFrom: "2026-13"}); err == nil {
		t.Error("CreateDuesRate in month 13 = nil error, want the CHECK to refuse")
	}
	if n := h.rowCount(t); n != rows {
		t.Errorf("dues_rate rows = %d, want %d: a refused write leaves nothing", n, rows)
	}

	// The ceiling itself is allowed, and a correction to zero is a free tier.
	atMax := h.correct(t, jan.ID, money.MaxAmount)
	if atMax.Amount != int64(money.MaxAmount) {
		t.Errorf("correction to the ceiling = %+v", atMax)
	}
	if free := h.correct(t, atMax.ID, 0); free.Amount != 0 {
		t.Errorf("correction to zero = %+v, want a free month", free)
	}
}

func TestRateWritesAreScopedToTheFund(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	jan := h.create(t, 10_000, "2026-01")
	other, err := h.q.CreateFund(context.Background(), store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.l.CorrectDuesRate(context.Background(), other.ID, jan.ID, 99_000); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("CorrectDuesRate from another fund = %v, want sql.ErrNoRows (not found)", err)
	}
	if err := h.l.DeleteDuesRate(context.Background(), other.ID, jan.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeleteDuesRate from another fund = %v, want sql.ErrNoRows", err)
	}
	if _, err := h.l.CreateDuesRate(context.Background(), CreateDuesRateParams{FundID: other.ID, TierID: h.tier, Amount: 1, EffectiveFrom: "2026-06"}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("CreateDuesRate onto another fund's tier = %v, want sql.ErrNoRows", err)
	}
	if n := h.rowCount(t); n != 1 {
		t.Errorf("dues_rate rows = %d, want 1", n)
	}
}

// A tier a rate trail hangs off still deletes when nobody was ever in it: the
// trail (corrected and deleting rows included) goes with it, as the rates always did.
func TestDeleteDuesTierRemovesAnUnusedTiersWholeRateTrail(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	typo := createDuesTier(t, h.q, h.f.fundID, "Slah ketik")
	create := func(a money.Amount, from string) store.DuesRate {
		r, err := h.l.CreateDuesRate(context.Background(), CreateDuesRateParams{FundID: h.f.fundID, TierID: typo, Amount: a, EffectiveFrom: from})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a := create(10_000, "2026-01")
	b, err := h.l.CorrectDuesRate(context.Background(), h.f.fundID, a.ID, 11_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.l.DeleteDuesRate(context.Background(), h.f.fundID, b.ID); err != nil {
		t.Fatal(err)
	}

	if err := h.l.DeleteDuesTier(context.Background(), h.f.fundID, typo); err != nil {
		t.Fatalf("DeleteDuesTier(a tier nobody was in, with a rate trail) = %v, want no error", err)
	}
	var n int
	if err := h.l.db.QueryRow(`SELECT COUNT(*) FROM dues_rate WHERE tier_id = ?`, typo).Scan(&n); err != nil || n != 0 {
		t.Errorf("rate rows of the deleted tier = %d, %v, want 0", n, err)
	}
}

// A tier a member_tier row names is refused, and the refusal rolls back the
// deletion of ALL its rate rows - the superseded and the deleting ones too.
func TestDeleteDuesTierRefusedKeepsTheWholeRateTrail(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t) // the member is in h.tier
	jan := h.create(t, 10_000, "2026-01")
	c := h.correct(t, jan.ID, 11_000)
	h.remove(t, c.ID)
	before := h.rowCount(t)
	if before != 3 {
		t.Fatalf("rate rows = %d, want 3 before the refused delete", before)
	}

	if err := h.l.DeleteDuesTier(context.Background(), h.f.fundID, h.tier); err == nil {
		t.Fatal("DeleteDuesTier(a tier a member is in) = nil error, want the foreign key to refuse")
	}
	if n := h.rowCount(t); n != before {
		t.Errorf("rate rows = %d after the refused delete, want all %d back", n, before)
	}
}

// DeleteDuesRatesByTier is keyed by tier id alone, so the ledger resolves the tier
// within the fund first: another fund's tier id is not found, and its rate trail
// is left exactly as it was.
func TestDeleteDuesTierOfAnotherFundIsNotFoundAndKeepsItsRates(t *testing.T) {
	t.Parallel()
	h := newRateFixture(t)
	jan := h.create(t, 10_000, "2026-01")
	h.correct(t, jan.ID, 11_000)
	before := h.rowCount(t)

	other, err := h.q.CreateFund(context.Background(), store.CreateFundParams{
		Name: "Other Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "zyxwvutsrqponmlkjihgfe", CreatedAt: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := h.l.DeleteDuesTier(context.Background(), other.ID, h.tier); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("DeleteDuesTier(another fund's tier) = %v, want sql.ErrNoRows (not found)", err)
	}
	if n := h.rowCount(t); n != before {
		t.Errorf("rate rows = %d, want %d: a foreign fund's call must not touch the trail", n, before)
	}
	if _, err := h.q.GetDuesTierForFund(context.Background(), store.GetDuesTierForFundParams{ID: h.tier, FundID: h.f.fundID}); err != nil {
		t.Errorf("the tier is gone after a foreign delete: %v", err)
	}
}
