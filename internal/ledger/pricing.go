package ledger

import (
	"sort"

	"github.com/kerti/uruni/internal/store"
)

// pricing answers the two lookups that price a dues month (ADR-040): which tier
// a member is in at a period, and which rate that tier carries then. Both are
// the same rule - a start-dated row holds from its month until the next row's
// month, so the answer for a period is the row of the latest month at or before
// it - applied to two series: member_tier (a member's tier) and dues_rate (a
// tier's price).
//
// It is built from rows fetched once for a whole fund or one member, so no read
// adds a per-member query. The rows must come from the standing views (one row
// per month, ordered by month): a superseded row never reaches this type, and a
// month whose standing rate was deleted is absent, so rateAt falls back to the
// earlier month with no deleted flag to check. Taking store.DuesRateStanding
// keeps a raw table row from being fed in.
type pricing struct {
	tiers map[int64][]tierSpan // by member id, oldest month first
	rates map[int64][]rateSpan // by tier id, oldest month first
}

// tierSpan is one standing member_tier row: from this month the member is in
// tierID, or in none (nil: no dues obligation).
type tierSpan struct {
	from   string // "YYYY-MM"
	tierID *int64
}

// rateSpan is one standing, non-deleted dues_rate row: from this month the tier
// costs amount.
type rateSpan struct {
	from   string // "YYYY-MM"
	amount int64
}

// newPricing folds standing member_tier rows (ordered by member, then month)
// and dues_rate rows (ordered by tier, then month) into per-member and per-tier
// timelines.
func newPricing(tierRows []store.MemberTierStanding, rateRows []store.DuesRateStanding) pricing {
	p := pricing{tiers: make(map[int64][]tierSpan), rates: make(map[int64][]rateSpan)}
	for _, r := range tierRows {
		p.tiers[r.MemberID] = append(p.tiers[r.MemberID], tierSpan{from: r.EffectiveFrom, tierID: r.TierID})
	}
	for _, r := range rateRows {
		p.rates[r.TierID] = append(p.rates[r.TierID], rateSpan{from: r.EffectiveFrom, amount: r.Amount})
	}
	return p
}

// hasTier reports whether the member has any standing row naming a tier; with
// none they owe nothing in any period.
func (p pricing) hasTier(memberID int64) bool {
	for _, s := range p.tiers[memberID] {
		if s.tierID != nil {
			return true
		}
	}
	return false
}

// tierAt is the member's tier at period: the tier on the row of the latest month
// at or before it. ok is false when no row reaches back that far or the row
// says "no tier": no obligation, never shown as unpaid.
func (p pricing) tierAt(memberID int64, period string) (tierID int64, ok bool) {
	spans := p.tiers[memberID]
	i := sort.Search(len(spans), func(i int) bool { return spans[i].from > period })
	if i == 0 || spans[i-1].tierID == nil {
		return 0, false
	}
	return *spans[i-1].tierID, true
}

// rateAt is the tier's rate at period: the latest month at or before it that
// still has a rate (a deleted month is not among them). ok is false when no
// rate is decided (the undecided-rate case, or only deleted ones): the period is
// skipped, never invented.
func (p pricing) rateAt(tierID int64, period string) (amount int64, ok bool) {
	spans := p.rates[tierID]
	i := sort.Search(len(spans), func(i int) bool { return spans[i].from > period })
	if i == 0 {
		return 0, false
	}
	return spans[i-1].amount, true
}

// priceAt is what the member owes for period: the tier in force then and its
// rate. ok is false when either lookup finds nothing, and the period owes
// nothing.
func (p pricing) priceAt(memberID int64, period string) (tierID, amount int64, ok bool) {
	tierID, ok = p.tierAt(memberID, period)
	if !ok {
		return 0, 0, false
	}
	amount, ok = p.rateAt(tierID, period)
	if !ok {
		return 0, 0, false
	}
	return tierID, amount, true
}

// currentTier is the member's tier now (ADR-040): the tier at the later of the
// current month and the join month, so a member who joins next month shows the
// tier entered for them. nil is no tier.
func (p pricing) currentTier(memberID int64, joinedOn, currentPeriod string) *int64 {
	at := currentPeriod
	if joined := joinedOn[:len(duesPeriodLayout)]; joined > at {
		at = joined
	}
	tierID, ok := p.tierAt(memberID, at)
	if !ok {
		return nil
	}
	return &tierID
}
