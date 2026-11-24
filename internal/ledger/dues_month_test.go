package ledger

import (
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// duesBook.month is the one decision every dues walk asks (window, then price,
// then classify), so these tests hold it directly over a book in memory: no
// database, no walk. The walks' own tests (dues_status_test.go and friends)
// stay the net that nothing they report moved.

const (
	monthTierA int64 = 10 // 25k from 2026-01, 30k from 2026-05
	monthTierB int64 = 11 // 40k from 2026-01
	monthTierC int64 = 12 // no rate before 2026-06
)

// monthPricing is three tiers and their rates, shared by every case.
func monthPricing(memberTiers ...store.MemberTierStanding) pricing {
	return newPricing(memberTiers, []store.DuesRateStanding{
		{TierID: monthTierA, Amount: 25_000, EffectiveFrom: "2026-01"},
		{TierID: monthTierA, Amount: 30_000, EffectiveFrom: "2026-05"},
		{TierID: monthTierB, Amount: 40_000, EffectiveFrom: "2026-01"},
		{TierID: monthTierC, Amount: 15_000, EffectiveFrom: "2026-06"},
	})
}

func monthTier(memberID, tierID int64, from string) store.MemberTierStanding {
	return store.MemberTierStanding{MemberID: memberID, TierID: &tierID, EffectiveFrom: from}
}

func monthMember(id int64, joinedOn string, inactiveOn *string) store.Member {
	return store.Member{ID: id, JoinedOn: joinedOn, InactiveOn: inactiveOn}
}

func TestDuesBookMonthOwesNothing(t *testing.T) {
	t.Parallel()
	left := "2026-06-20"
	book := duesBook{prices: monthPricing(
		monthTier(1, monthTierA, "2026-01"),
		monthTier(2, monthTierA, "2026-01"),
		// Member 3: a tier, then "no tier" from June.
		monthTier(3, monthTierA, "2026-01"),
		store.MemberTierStanding{MemberID: 3, TierID: nil, EffectiveFrom: "2026-06"},
		// Member 4: a tier with no rate until June.
		monthTier(4, monthTierC, "2026-01"),
		// Member 5 has no member_tier row at all.
	)}

	tests := []struct {
		name   string
		member store.Member
		period string
	}{
		{"before the month they joined", monthMember(1, "2026-03-15", nil), "2026-02"},
		{"after the month they went inactive", monthMember(2, "2026-01-01", &left), "2026-07"},
		{"a row says no tier", monthMember(3, "2026-01-01", nil), "2026-06"},
		{"no member_tier row", monthMember(5, "2026-01-01", nil), "2026-03"},
		{"before the tier's first rate", monthMember(4, "2026-01-01", nil), "2026-05"},
		{"before any tier row reaches back", monthMember(1, "2025-06-01", nil), "2025-12"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := book.month(tc.member, tc.period); ok {
				t.Errorf("month(%s) = %+v, true; want it to owe nothing", tc.period, got)
			}
		})
	}
}

func TestDuesBookMonthWindowIsInclusiveByMonthAtBothEnds(t *testing.T) {
	t.Parallel()
	left := "2026-06-02"
	book := duesBook{prices: monthPricing(monthTier(1, monthTierA, "2026-01"))}
	m := monthMember(1, "2026-03-31", &left)

	for period, want := range map[string]bool{
		"2026-02": false, "2026-03": true, "2026-04": true, "2026-06": true, "2026-07": false,
	} {
		if _, ok := book.month(m, period); ok != want {
			t.Errorf("month(%s) owes = %t, want %t (joined 2026-03-31, inactive 2026-06-02)", period, ok, want)
		}
	}
}

func TestDuesBookMonthClassifies(t *testing.T) {
	t.Parallel()
	m := monthMember(1, "2026-01-01", nil)
	note := "dana duka"
	empty := ""

	tests := []struct {
		name       string
		paid       money.Amount // 0 means no entry at all
		waived     bool
		waiverNote *string
		want       DuesStatus
	}{
		{"nothing paid", 0, false, nil, DuesStatusUnpaid},
		{"part paid", 10_000, false, nil, DuesStatusPartial},
		{"one rupiah short", 24_999, false, nil, DuesStatusPartial},
		{"paid exactly", 25_000, false, nil, DuesStatusPaid},
		{"overpaid is still paid", 60_000, false, nil, DuesStatusPaid},
		{"waived, nothing paid, no note", 0, true, nil, DuesStatusWaived},
		{"waived with a note", 0, true, &note, DuesStatusWaived},
		{"waived with an empty note keeps it", 0, true, &empty, DuesStatusWaived},
		{"waived and part paid", 10_000, true, &note, DuesStatusWaived},
		{"waived but paid in full is still waived", 25_000, true, nil, DuesStatusWaived},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			book := duesBook{prices: monthPricing(monthTier(1, monthTierA, "2026-01")), paid: duesPaid{}, waivers: map[int64]map[string]*string{}}
			if tc.paid != 0 {
				book.paid[1] = map[string]money.Amount{"2026-03": tc.paid}
			}
			if tc.waived {
				book.waivers[1] = map[string]*string{"2026-03": tc.waiverNote}
			}

			got, ok := book.month(m, "2026-03")
			if !ok {
				t.Fatal("month(2026-03) owes nothing, want a priced month")
			}
			want := duesMonth{TierID: monthTierA, Owed: 25_000, Paid: tc.paid, Status: tc.want, WaiverNote: tc.waiverNote}
			if got != want {
				t.Errorf("month(2026-03) = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDuesBookMonthIgnoresWhatOtherMembersAndMonthsDid(t *testing.T) {
	t.Parallel()
	book := duesBook{
		prices:  monthPricing(monthTier(1, monthTierA, "2026-01"), monthTier(2, monthTierA, "2026-01")),
		paid:    duesPaid{2: {"2026-03": 25_000}, 1: {"2026-04": 25_000}},
		waivers: map[int64]map[string]*string{2: {"2026-03": nil}, 1: {"2026-05": nil}},
	}
	got, ok := book.month(monthMember(1, "2026-01-01", nil), "2026-03")
	if !ok || got.Paid != 0 || got.Status != DuesStatusUnpaid || got.WaiverNote != nil {
		t.Errorf("month(member 1, 2026-03) = %+v, %t; want unpaid, nothing paid, from their own rows only", got, ok)
	}
}

func TestDuesBookMonthPricesAtTheTierInForceThen(t *testing.T) {
	t.Parallel()
	// Tier A until April, tier B from May (ADR-040); tier A's own rate also rises
	// in May, which a member who left A never sees.
	book := duesBook{
		prices: monthPricing(monthTier(1, monthTierA, "2026-01"), monthTier(1, monthTierB, "2026-05")),
		paid:   duesPaid{1: {"2026-04": 25_000, "2026-05": 25_000}},
	}
	m := monthMember(1, "2026-01-01", nil)

	april, ok := book.month(m, "2026-04")
	if !ok || april.TierID != monthTierA || april.Owed != 25_000 || april.Status != DuesStatusPaid {
		t.Errorf("month(2026-04) = %+v, %t; want tier A, owed 25000, paid", april, ok)
	}
	may, ok := book.month(m, "2026-05")
	if !ok || may.TierID != monthTierB || may.Owed != 40_000 || may.Status != DuesStatusPartial {
		t.Errorf("month(2026-05) = %+v, %t; want tier B, owed 40000, partial: 25k paid at the old price is short at the new one", may, ok)
	}
}

func TestDuesBookMonthFollowsARateRiseWithinOneTier(t *testing.T) {
	t.Parallel()
	book := duesBook{prices: monthPricing(monthTier(1, monthTierA, "2026-01"))}
	m := monthMember(1, "2026-01-01", nil)

	for period, want := range map[string]money.Amount{"2026-04": 25_000, "2026-05": 30_000, "2026-09": 30_000} {
		if got, ok := book.month(m, period); !ok || got.Owed != want {
			t.Errorf("month(%s) = %+v, %t; want owed %d", period, got, ok, want)
		}
	}
}

func TestDuesPaidAddDropsAnUnattributedRowAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	paid := duesPaid{}
	id := int64(7)
	paid.add(nil, "2026-03", 5_000)
	paid.add(&id, "2026-03", 25_000)
	paid.add(&id, "2026-04", 10_000)

	if len(paid) != 1 || paid[7]["2026-03"] != 25_000 || paid[7]["2026-04"] != 10_000 {
		t.Errorf("paid = %v, want only member 7 with 25000 and 10000", paid)
	}
}

// paidThrough asks duesBook.month, so a month the member owes nothing for is
// skipped whether it is unpriced or outside the window. These pin that for the
// out-of-window case, which used to end the run (#894): the answer cannot differ,
// because the walk starts at an owed month and every month past inactive_on is
// past it too.

func TestPaidThroughStopsAtTheMonthTheMemberLeftEvenWithLaterPaymentsOnFile(t *testing.T) {
	t.Parallel()
	// PostDuesPayments checks the month against the fund's floor and ceiling and
	// the waivers, not against the member's window, so a payment for a month
	// after inactive_on can exist; it is never counted.
	left := "2026-11-15"
	m := monthMember(1, "2026-01-01", &left)
	prices := monthPricing(monthTier(1, monthTierA, "2026-01"))
	paid := map[string]money.Amount{"2026-10": 30_000, "2026-11": 30_000, "2026-12": 30_000, "2027-01": 30_000}

	got, err := paidThrough(oneMemberBook(m, prices, paid, nil), m, "2026-10", "2027-01")
	if err != nil {
		t.Fatalf("paidThrough = %v, want no error", err)
	}
	if got != "2026-11" {
		t.Errorf("paidThrough = %q, want \"2026-11\": nothing is owed, so nothing is paid through, after the month the member left", got)
	}
}

func TestPaidThroughIsEmptyWhenTheMemberLeftAtTheRequestedMonth(t *testing.T) {
	t.Parallel()
	left := "2026-10-30"
	m := monthMember(1, "2026-01-01", &left)
	prices := monthPricing(monthTier(1, monthTierA, "2026-01"))
	paid := map[string]money.Amount{"2026-10": 30_000, "2026-11": 30_000}

	got, err := paidThrough(oneMemberBook(m, prices, paid, nil), m, "2026-10", "2026-11")
	if err != nil {
		t.Fatalf("paidThrough = %v, want no error", err)
	}
	if got != "" {
		t.Errorf("paidThrough = %q, want \"\": October is the last month the member owes", got)
	}
}

func TestPaidThroughSkipsAnUnpricedMonthWithoutBreakingTheRun(t *testing.T) {
	t.Parallel()
	// A row says "no tier" for November only; December is priced again.
	prices := monthPricing(
		monthTier(1, monthTierA, "2026-01"),
		store.MemberTierStanding{MemberID: 1, TierID: nil, EffectiveFrom: "2026-11"},
		monthTier(1, monthTierA, "2026-12"),
	)
	m := monthMember(1, "2026-01-01", nil)
	paid := map[string]money.Amount{"2026-10": 30_000, "2026-12": 30_000}

	got, err := paidThrough(oneMemberBook(m, prices, paid, nil), m, "2026-10", "2026-12")
	if err != nil {
		t.Fatalf("paidThrough = %v, want no error", err)
	}
	if got != "2026-12" {
		t.Errorf("paidThrough = %q, want \"2026-12\": November owes nothing, so it neither breaks the run nor extends it", got)
	}
}

func TestPaidThroughStopsAtAWaivedMonth(t *testing.T) {
	t.Parallel()
	prices := monthPricing(monthTier(1, monthTierA, "2026-01"))
	m := monthMember(1, "2026-01-01", nil)
	paid := map[string]money.Amount{"2026-10": 30_000, "2026-11": 30_000, "2026-12": 30_000}
	waived := map[string]*string{"2026-11": nil}

	got, err := paidThrough(oneMemberBook(m, prices, paid, waived), m, "2026-10", "2026-12")
	if err != nil {
		t.Fatalf("paidThrough = %v, want no error", err)
	}
	if got != "" {
		t.Errorf("paidThrough = %q, want \"\": November is waived, not paid, even though it carries a full payment", got)
	}
}

// oneMemberBook is the book paidThrough reads for m alone.
func oneMemberBook(m store.Member, prices pricing, paid map[string]money.Amount, waived map[string]*string) duesBook {
	return duesBook{prices: prices, paid: duesPaid{m.ID: paid}, waivers: map[int64]map[string]*string{m.ID: waived}}
}
