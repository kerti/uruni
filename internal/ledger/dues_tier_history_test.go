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

// --- every dues read prices a period at the tier in force then (ADR-040) -----
//
// Tier A costs 10,000 and Tier B 50,000, both from January 2026. Each test
// promotes (or demotes) a member and asks what a period reads, in the status
// roster, the outstanding walk, the arrears badge or "paid through".

type tierHistoryFixture struct {
	l        *Ledger
	f        fixture
	q        *store.Queries
	a, b     int64
	memberID int64
}

// newTierHistory is a member who joined in January 2026 on Tier A and moves to
// Tier B from June.
func newTierHistory(t *testing.T) tierHistoryFixture {
	t.Helper()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, a, 10_000, "2026-01")
	b := createDuesTier(t, q, f.fundID, "Tier B")
	createDuesRate(t, q, b, 50_000, "2026-01")
	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Andi", tierID: &a, joinedOn: &joinedOn})
	setMemberTier(t, q, f.fundID, memberID, &b, "2026-06", 0)
	return tierHistoryFixture{l: l, f: f, q: q, a: a, b: b, memberID: memberID}
}

func (h tierHistoryFixture) pay(t *testing.T, amount money.Amount, periods ...string) {
	t.Helper()
	rows := make([]PeriodAmount, 0, len(periods))
	for _, p := range periods {
		rows = append(rows, PeriodAmount{DuesPeriod: p, Amount: amount})
	}
	if _, err := h.l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: h.f.fundID, AccountID: h.f.cashID, MemberID: h.memberID, OccurredOn: "2026-10-01", Periods: rows,
	}); err != nil {
		t.Fatalf("PostDuesPayments = %v, want no error", err)
	}
}

func (h tierHistoryFixture) status(t *testing.T, period string) (MemberDuesStatus, bool) {
	t.Helper()
	rows, err := h.l.DuesStatusForPeriod(context.Background(), h.f.fundID, period)
	if err != nil {
		t.Fatalf("DuesStatusForPeriod(%s) = %v, want no error", period, err)
	}
	return statusFor(t, rows, h.memberID)
}

// A month paid in full at the old tier stays Paid after the promotion. Under the
// old rule (the current tier everywhere) January read Bayar sebagian.
func TestDuesStatusForPeriodPastMonthPaidAtTheOldTierStaysPaidAfterAPromotion(t *testing.T) {
	t.Parallel()
	h := newTierHistory(t)
	h.pay(t, 10_000, "2026-01")
	h.pay(t, 3_000, "2026-02") // partial at the old tier

	jan, _ := h.status(t, "2026-01")
	// Paid in advance because February also has a payment; either way it is paid in
	// full against 10000, the tier then, not partial against today's 50000.
	if jan.Status != DuesStatusPaidInAdvance || jan.OwedAmount != 10_000 || jan.PaidAmount != 10_000 {
		t.Errorf("January = %s owed %d paid %d, want paid (in advance) at 10000, the tier then", jan.Status, jan.OwedAmount, jan.PaidAmount)
	}
	feb, _ := h.status(t, "2026-02")
	if feb.Status != DuesStatusPartial || feb.OwedAmount != 10_000 || feb.PaidAmount != 3_000 {
		t.Errorf("February = %s owed %d paid %d, want partial 3000 of 10000", feb.Status, feb.OwedAmount, feb.PaidAmount)
	}
	jun, _ := h.status(t, "2026-06")
	if jun.Status != DuesStatusUnpaid || jun.OwedAmount != 50_000 {
		t.Errorf("June = %s owed %d, want unpaid at 50000 (the new tier)", jun.Status, jun.OwedAmount)
	}
}

func TestOutstandingDuesAndArrearsFollowTheTiersTheMemberWasIn(t *testing.T) {
	t.Parallel()
	h := newTierHistory(t)
	h.pay(t, 10_000, "2026-01", "2026-02", "2026-03") // January..March settled at the old tier
	h.pay(t, 10_000, "2026-06")                       // June paid at the OLD rate: short against the new one

	out, err := h.l.OutstandingDuesForMember(context.Background(), h.f.fundID, h.memberID, "2026-07", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := []OutstandingDuesPeriod{
		{Period: "2026-04", OwedAmount: 10_000, Status: DuesStatusUnpaid},
		{Period: "2026-05", OwedAmount: 10_000, Status: DuesStatusUnpaid},
		{Period: "2026-06", OwedAmount: 50_000, PaidAmount: 10_000, Status: DuesStatusPartial},
		{Period: "2026-07", OwedAmount: 50_000, Status: DuesStatusUnpaid},
	}
	if len(out) != len(want) {
		t.Fatalf("outstanding = %+v, want %+v", out, want)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("outstanding[%d] = %+v, want %+v", i, out[i], want[i])
		}
	}

	// The badge counts months strictly before the current one: April, May, June.
	n, err := h.l.ArrearsMonthsForMember(context.Background(), h.f.fundID, h.memberID, "2026-07")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("ArrearsMonthsForMember(2026-07) = %d, want 3 (April and May unpaid, June short)", n)
	}
}

func TestPaidThroughChainsAcrossATierChange(t *testing.T) {
	t.Parallel()
	h := newTierHistory(t)
	// Prepaid January..July: the old tier's 10,000 every month. The promotion
	// lands on the prepaid months from June, exactly as a rate raise does.
	h.pay(t, 10_000, "2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07")

	jan, _ := h.status(t, "2026-01")
	if jan.Status != DuesStatusPaidInAdvance || jan.PaidThrough != "2026-05" {
		t.Errorf("January = %s through %q, want paid in advance through 2026-05 (June reads partial at 50000)", jan.Status, jan.PaidThrough)
	}
	jun, _ := h.status(t, "2026-06")
	if jun.Status != DuesStatusPartial {
		t.Errorf("June = %s, want partial: a tier raise lands on a prepaid month", jun.Status)
	}

	// Paid at the new tier's price from June: the run reaches July.
	h2 := newTierHistory(t)
	h2.pay(t, 10_000, "2026-01", "2026-02", "2026-03", "2026-04", "2026-05")
	h2.pay(t, 50_000, "2026-06", "2026-07")
	may, _ := h2.status(t, "2026-05")
	if may.PaidThrough != "2026-07" {
		t.Errorf("May PaidThrough = %q, want 2026-07: the run carries across the tier change when each month is paid at its own tier", may.PaidThrough)
	}
}

func TestTierRemovalOwesNothingFromThatMonthAndNeverReadsUnpaid(t *testing.T) {
	t.Parallel()
	h := newTierHistory(t)
	setMemberTier(t, h.q, h.f.fundID, h.memberID, nil, "2026-09", 0) // no tier from September

	if aug, ok := h.status(t, "2026-08"); !ok || aug.OwedAmount != 50_000 {
		t.Errorf("August = %+v listed %v, want owed 50000 (still on Tier B)", aug, ok)
	}
	if _, ok := h.status(t, "2026-09"); ok {
		t.Error("September lists the member, want no obligation from the month the tier was removed")
	}
	if _, ok := h.status(t, "2027-03"); ok {
		t.Error("2027-03 lists the member, want no obligation")
	}

	out, err := h.l.OutstandingDuesForMember(context.Background(), h.f.fundID, h.memberID, "2026-12", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range out {
		if p.Period >= "2026-09" {
			t.Errorf("outstanding lists %s, want nothing owed after the tier was removed", p.Period)
		}
	}
}

func TestTierGivenLaterLeavesTheEarlierMonthsUntiered(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")
	createDuesRate(t, q, a, 10_000, "2026-01")
	joinedOn := "2026-01-01"
	memberID := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Citra", joinedOn: &joinedOn})
	setMemberTier(t, q, f.fundID, memberID, &a, "2026-04", 0)

	out, err := l.OutstandingDuesForMember(context.Background(), f.fundID, memberID, "2026-05", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Period != "2026-04" {
		t.Errorf("outstanding = %+v, want April and May only: January..March had no tier", out)
	}
}

func TestTheHighestCorrectionOfAMonthPricesIt(t *testing.T) {
	t.Parallel()
	h := newTierHistory(t)
	// June was stated as Tier B, then corrected to Tier A. A third row says A from
	// June again: the standing row is the highest correction, whatever was written
	// last or has the highest id.
	setMemberTier(t, h.q, h.f.fundID, h.memberID, &h.a, "2026-06", 2)
	setMemberTier(t, h.q, h.f.fundID, h.memberID, &h.b, "2026-06", 1)

	jun, ok := h.status(t, "2026-06")
	if !ok || jun.OwedAmount != 10_000 || *jun.Member.TierID != h.a {
		t.Errorf("June = %+v listed %v, want Tier A (correction 2 stands over the rows below it)", jun, ok)
	}
}

// pricingCalls counts the reads that price a period. Embedding the Querier
// forwards every other call untouched.
type pricingCalls struct {
	store.Querier
	tiersByFund, ratesForPricing, tiersForMember, ratesForMember, effectiveRate int
}

func (c *pricingCalls) ListMemberTiersByFund(ctx context.Context, fundID int64) ([]store.MemberTierStanding, error) {
	c.tiersByFund++
	return c.Querier.ListMemberTiersByFund(ctx, fundID)
}

func (c *pricingCalls) ListDuesRatesForPricing(ctx context.Context, fundID int64) ([]store.DuesRateStanding, error) {
	c.ratesForPricing++
	return c.Querier.ListDuesRatesForPricing(ctx, fundID)
}

func (c *pricingCalls) ListMemberTiersForMember(ctx context.Context, arg store.ListMemberTiersForMemberParams) ([]store.MemberTierStanding, error) {
	c.tiersForMember++
	return c.Querier.ListMemberTiersForMember(ctx, arg)
}

func (c *pricingCalls) ListDuesRatesForMemberPricing(ctx context.Context, arg store.ListDuesRatesForMemberPricingParams) ([]store.DuesRateStanding, error) {
	c.ratesForMember++
	return c.Querier.ListDuesRatesForMemberPricing(ctx, arg)
}

func (c *pricingCalls) GetEffectiveDuesRate(ctx context.Context, arg store.GetEffectiveDuesRateParams) (store.DuesRateStanding, error) {
	c.effectiveRate++
	return c.Querier.GetEffectiveDuesRate(ctx, arg)
}

// The tier at a period costs no query per member: a roster is priced from two
// reads however many members it holds, and one member's walk from two reads
// however many months it covers (ADR-040: "no read adds a per-member query").
func TestPricingReadsDoNotGrowWithMembersOrMonths(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")
	createDuesRate(t, q, a, 10_000, "2024-01")
	var last int64
	for i := 0; i < 40; i++ {
		last = createDuesMember(t, q, f.fundID, duesMemberParams{name: "Warga " + string(rune('A'+i%26)) + string(rune('a'+i/26)), tierID: &a})
	}
	// Prepaid, so every paid-in-advance member also walks paidThrough.
	if _, err := l.PostDuesPayments(context.Background(), PostDuesPaymentsParams{
		FundID: f.fundID, AccountID: f.cashID, MemberID: last, OccurredOn: "2026-08-01",
		Periods: []PeriodAmount{{DuesPeriod: "2026-08", Amount: 10_000}, {DuesPeriod: "2026-09", Amount: 10_000}},
	}); err != nil {
		t.Fatal(err)
	}

	calls := &pricingCalls{Querier: l.q}
	l.q = calls

	rows, err := l.DuesStatusForPeriod(context.Background(), f.fundID, "2026-08")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 40 {
		t.Fatalf("roster = %d rows, want 40", len(rows))
	}
	if calls.tiersByFund != 1 || calls.ratesForPricing != 1 || calls.effectiveRate != 0 {
		t.Errorf("DuesStatusForPeriod made %d tier reads, %d rate reads, %d per-member rate lookups; want 1, 1, 0 for 40 members",
			calls.tiersByFund, calls.ratesForPricing, calls.effectiveRate)
	}

	if _, err := l.OutstandingDuesForMember(context.Background(), f.fundID, last, "2026-08", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if calls.tiersForMember != 1 || calls.ratesForMember != 1 || calls.effectiveRate != 0 {
		t.Errorf("OutstandingDuesForMember made %d tier reads, %d rate reads, %d per-period rate lookups; want 1, 1, 0 across 31 months",
			calls.tiersForMember, calls.ratesForMember, calls.effectiveRate)
	}
}

// --- deleting a tier ----------------------------------------------------------

func TestDeleteDuesTierRefusesATierOnlyASupersededRowNamesAndKeepsItsRates(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	slip := createDuesTier(t, q, f.fundID, "Salah pilih")
	createDuesRate(t, q, slip, 10_000, "2026-01")
	right := createDuesTier(t, q, f.fundID, "Benar")
	m := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Andi", tierID: &slip})
	// Corrected away: the slip's row is superseded, no member is in it now.
	setMemberTier(t, q, f.fundID, m, &right, longAgoJoinedOn[:7], 1)

	err := l.DeleteDuesTier(context.Background(), f.fundID, slip)
	if err == nil {
		t.Fatal("DeleteDuesTier(a tier a superseded row names) = nil error, want the foreign key to refuse")
	}
	rates, err := q.ListDuesRatesByTier(context.Background(), slip)
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 {
		t.Errorf("the refused delete left %d rates, want its 1 back: the rate deletion must roll back", len(rates))
	}
}

func TestDeleteDuesTierStillRemovesATierNobodyWasEverIn(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	typo := createDuesTier(t, q, f.fundID, "Slah ketik")
	createDuesRate(t, q, typo, 10_000, "2026-01")

	if err := l.DeleteDuesTier(context.Background(), f.fundID, typo); err != nil {
		t.Fatalf("DeleteDuesTier() = %v, want no error", err)
	}
	if _, err := q.GetDuesTierForFund(context.Background(), store.GetDuesTierForFundParams{ID: typo, FundID: f.fundID}); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetDuesTierForFund after delete = %v, want sql.ErrNoRows", err)
	}
}

// --- the wire tier of a roster row ------------------------------------------

func TestListMembersPageShowsTheCurrentTierNotAFutureOrASupersededOne(t *testing.T) {
	t.Parallel()
	l := newTestLedger(t)
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "A")
	b := createDuesTier(t, q, f.fundID, "B")
	createDuesRate(t, q, a, 10_000, "2026-01")
	createDuesRate(t, q, b, 50_000, "2026-01")
	joinedOn := "2026-01-01"
	m := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Andi", tierID: &a, joinedOn: &joinedOn})
	setMemberTier(t, q, f.fundID, m, &b, "2026-06", 0)
	setMemberTier(t, q, f.fundID, m, &a, "2026-06", 1) // June corrected back to A

	// Joins next month; shows the tier entered for them.
	next := "2026-09-01"
	nm := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Baru", tierID: &b, joinedOn: &next})

	// Joins next month on a tier whose first rate starts that month: the rate is
	// read at the same month as the tier, so both show, not a tier with no rate.
	c := createDuesTier(t, q, f.fundID, "C")
	createDuesRate(t, q, c, 70_000, "2026-09")
	cm := createDuesMember(t, q, f.fundID, duesMemberParams{name: "Baru Lagi", tierID: &c, joinedOn: &next})

	rows, err := q.ListMembersPage(context.Background(), store.ListMembersPageParams{FundID: f.fundID, CurrentPeriod: "2026-08", PageLimit: 50})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]store.ListMembersPageRow{}
	for _, r := range rows {
		got[r.ID] = r
	}
	if r := got[m]; r.TierID == nil || *r.TierID != a || r.CurrentRate == nil || *r.CurrentRate != 10_000 {
		t.Errorf("Andi = tier %v rate %v, want Tier A at 10000 (June's correction stands)", r.TierID, r.CurrentRate)
	}
	if r := got[nm]; r.TierID == nil || *r.TierID != b || r.TierName == nil || *r.TierName != "B" {
		t.Errorf("Baru = tier %v name %v, want Tier B: a member who joins next month shows the tier entered for them", r.TierID, r.TierName)
	}
	if r := got[cm]; r.TierID == nil || *r.TierID != c || r.CurrentRate == nil || *r.CurrentRate != 70_000 {
		t.Errorf("Baru Lagi = tier %v rate %v, want Tier C at 70000: tier and rate read at the join month", r.TierID, r.CurrentRate)
	}
	if r := got[f.memberID]; r.TierID != nil || r.TierName != nil || r.CurrentRate != nil {
		t.Errorf("Jane (no tier) = %+v, want nil tier, name and rate", r)
	}
}
