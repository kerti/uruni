package ledger

import (
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// duesPaid is what members paid toward dues, by member and then month
// ("YYYY-MM"): the one shape every dues walk reads payments in, reversed
// payments already excluded by the queries (ADR-029). A member or month who paid
// nothing has no entry, and reads as the zero Amount.
type duesPaid map[int64]map[string]money.Amount

// add records that memberID paid amount toward period. A nil member is a row
// the query could not attribute to anyone (the column is nullable though every
// dues row has one) and is dropped. It is the one place the nested map is
// built, whichever query's rows feed it, so the fold cannot differ per read.
func (p duesPaid) add(memberID *int64, period string, amount int64) {
	if memberID == nil {
		return
	}
	byPeriod := p[*memberID]
	if byPeriod == nil {
		byPeriod = make(map[string]money.Amount)
		p[*memberID] = byPeriod
	}
	byPeriod[period] = money.FromDB(amount)
}

// duesBook is everything a dues walk knows about the months it reads: what
// prices them (ADR-040), what was paid toward them, and which are waived
// (ADR-050), already folded in memory. Each walk's loader fills one from its own
// scope's reads (the fund's month, one member, a page of members, a twelve-month
// window - the #532 fixed read counts), and then asks it, so the decision for one
// member and month is written once here and not once per walk.
type duesBook struct {
	prices  pricing
	paid    duesPaid
	waivers map[int64]map[string]*string // foldWaivers: member, month, note
}

// duesMonth is one member's dues for one month they owe something for.
type duesMonth struct {
	// TierID is the tier in force at the month, the one that priced Owed
	// (ADR-040), not the member's current tier.
	TierID int64
	Owed   money.Amount
	Paid   money.Amount
	// Status is classifyDuesMonth's: never PaidInAdvance, which is
	// DuesStatusForPeriod's upgrade over the member's later payments.
	Status DuesStatus
	// WaiverNote is the waiver's note when the month is waived and one was given.
	WaiverNote *string
}

// month is member m's dues in period, and ok is false when they owe nothing then:
// the month is outside their joined_on..inactive_on window, or no tier is in force
// or the tier has no rate effective yet (the undecided-rate case), because Uruni
// does not invent an amount. "Owes nothing" is one outcome, not two: a month that
// does not exist for the member is skipped, never read as unpaid.
//
// This is where window, then price, then classify is decided, for the dues roster,
// the member's outstanding months, the paid-through walk and the grid alike. A
// rule that changes what a month is (ADR-050's waiver was one) changes here, once.
func (b duesBook) month(m store.Member, period string) (duesMonth, bool) {
	if !memberOwesPeriod(m, period) {
		return duesMonth{}, false
	}
	tierID, rate, ok := b.prices.priceAt(m.ID, period)
	if !ok {
		return duesMonth{}, false
	}
	owed := money.FromDB(rate)
	paid := b.paid[m.ID][period] // zero value if the member paid nothing this period
	note, waived := b.waivers[m.ID][period]
	return duesMonth{
		TierID: tierID, Owed: owed, Paid: paid,
		Status: classifyDuesMonth(owed, paid, waived), WaiverNote: note,
	}, true
}
