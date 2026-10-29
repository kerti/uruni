package demo

// The cast of "CS Shift Sore" (ADR-045 decision 5): the customer-service agents
// of one office afternoon shift. Every name is invented and ordinary; every
// amount is a toy amount. This file is only data - what each person does month
// by month is told in story.go.

// Tier keys. The names the treasurer sees are in tierNames.
const (
	tierAgen  = "agen"
	tierLead  = "lead"
	tierSuper = "super"
)

type tierSpec struct {
	key  string
	name string
	// before is the monthly rate until rateRiseMonth, from is the rate that
	// month on (rupiah).
	before, from int64
}

// rateRiseMonth is the month every rate rises (Mulai bulan, M-6).
const rateRiseMonth = -6

var tierSpecs = []tierSpec{
	{tierAgen, "Agen", 25_000, 30_000},
	{tierLead, "Team Leader", 40_000, 50_000},
	{tierSuper, "Supervisor", 60_000, 75_000},
}

// rate is what tier key costs per month in month offset.
func rate(key string, offset int) int64 {
	for _, t := range tierSpecs {
		if t.key == key {
			if offset < rateRiseMonth {
				return t.before
			}
			return t.from
		}
	}
	return 0
}

// when is a day of a month offset.
type when struct{ month, day int }

// tierFrom is "from this month on the member is in this tier"; an empty tier is
// Tanpa kategori (no dues obligation).
type tierFrom struct {
	month int
	tier  string
}

type person struct {
	name string
	// joined is the join date; the tier timeline starts at its month.
	joined when
	// left, when set, is the day the member goes inactive.
	left *when
	// tiers is the tier history as the story finally leaves it, oldest first,
	// starting at the join month. A tier entered wrongly and corrected shows only
	// its corrected form here: this is what the member owes, month by month.
	tiers []tierFrom
	// payDay is the day of the month the member usually pays.
	payDay int
	// viaBank is whether the member pays by transfer into the bank account.
	viaBank bool
	// enteredAs, when set, is the tier the member was first entered in by
	// mistake, corrected to tiers[0] the same day.
	enteredAs string
}

func (p person) tierAt(offset int) string {
	tier := ""
	for _, t := range p.tiers {
		if t.month <= offset {
			tier = t.tier
		}
	}
	return tier
}

// owes is the amount the member owes for month offset: nothing before they
// joined, after the month they left, or with no tier.
func (p person) owes(offset int) int64 {
	if offset < p.joined.month || (p.left != nil && offset > p.left.month) {
		return 0
	}
	return rate(p.tierAt(offset), offset)
}

func stays(tier string, joinedMonth int) []tierFrom { return []tierFrom{{joinedMonth, tier}} }

func ptr[T any](v T) *T { return &v }

// roster is every member of the fund, in the order they are created: the eleven
// of the first month, then the two who join later.
//
//	Bu Erna       Supervisor    leaves for another unit in M-8
//	Pak Arga      Team Leader
//	Mbak Dewi     Team Leader
//	Mbak Indri    Agen          promoted to Team Leader in M-5
//	Mbak Sari     Agen
//	Mas Pandu     Agen
//	Mbak Dini     Agen
//	Mas Yoga      Agen          resigns in M-4
//	Mas Rizki     Agen          falls behind from M-5
//	Mbak Wulan    Agen          falls behind from M-3
//	Pak Lingga    Agen          Tanpa kategori for M-9 and M-8
//	Mbak Anisa    Agen          joins from Shift Pagi in M-10
//	Mas Fajar     Agen          joins in M-3
var roster = []person{
	{name: "Bu Erna", joined: when{firstMonth, 1}, left: ptr(when{-8, 28}), tiers: stays(tierSuper, firstMonth), payDay: 3},
	{name: "Pak Arga", joined: when{firstMonth, 1}, tiers: stays(tierLead, firstMonth), payDay: 5, viaBank: true},
	{name: "Mbak Dewi", joined: when{firstMonth, 1}, tiers: stays(tierLead, firstMonth), payDay: 6},
	{name: "Mbak Indri", joined: when{firstMonth, 1}, tiers: []tierFrom{{firstMonth, tierAgen}, {-5, tierLead}}, payDay: 7},
	{name: "Mbak Sari", joined: when{firstMonth, 1}, tiers: stays(tierAgen, firstMonth), payDay: 8},
	{name: "Mas Pandu", joined: when{firstMonth, 1}, tiers: stays(tierAgen, firstMonth), payDay: 9, viaBank: true},
	{name: "Mbak Dini", joined: when{firstMonth, 1}, tiers: stays(tierAgen, firstMonth), payDay: 10},
	{name: "Mas Yoga", joined: when{firstMonth, 1}, left: ptr(when{-4, 20}), tiers: stays(tierAgen, firstMonth), payDay: 11},
	{name: "Mas Rizki", joined: when{firstMonth, 1}, tiers: stays(tierAgen, firstMonth), payDay: 12},
	{name: "Mbak Wulan", joined: when{firstMonth, 1}, tiers: stays(tierAgen, firstMonth), payDay: 13, viaBank: true},
	{name: "Pak Lingga", joined: when{firstMonth, 1}, tiers: []tierFrom{{firstMonth, tierAgen}, {-9, ""}, {-7, tierAgen}}, payDay: 14},
	{name: "Mbak Anisa", joined: when{-10, 12}, tiers: stays(tierAgen, -10), payDay: 15},
	{name: "Mas Fajar", joined: when{-3, 9}, tiers: stays(tierAgen, -3), payDay: 16, enteredAs: tierLead},
}

// How each member-month of dues goes when it is not "paid in full on the
// member's usual day". The story's exceptions, and nothing else.
type duesKind int

const (
	duesSkipped  duesKind = iota // nothing is paid for the month
	duesPartial                  // part of it is paid (the amount rides along)
	duesWithNext                 // paid together with the next month, in one go
	duesAhead                    // paid ahead: this month pays it and the n after
	duesPrepaid                  // settled earlier by another payment, nothing on its own day
)

type duesCase struct {
	kind duesKind
	// amount is a partial payment (duesPartial), months is how many months ahead
	// a payment reaches (duesAhead).
	amount int64
	months int
}

type duesKey struct {
	name  string
	month int
}

// prepaidBeforeUruni are the members whose first months were paid before the
// fund existed. The money is already in the opening balance; the seeder records
// the months anyway, as the product tells a treasurer to (a dues payment on the
// setup day), and settles the recorded total away with an adjusted cek kas plug,
// so the recorded balance stays the real one. The join date stays the setup
// date: Uruni's history starts at adoption.
var prepaidBeforeUruni = []struct {
	name   string
	months []int
}{
	{"Pak Arga", []int{firstMonth, firstMonth + 1, firstMonth + 2}},
	{"Mbak Dewi", []int{firstMonth, firstMonth + 1}},
	{"Bu Erna", []int{firstMonth, firstMonth + 1}},
}

// duesCases are the exceptions to everyone-pays-on-time that make the dues
// screens interesting: two members behind for months, a part payment, a part
// payment topped up later, months paid in pairs, one member paid ahead, dues
// paid before the fund existed, and a few not paid yet this month.
var duesCases = func() map[duesKey]duesCase {
	m := map[duesKey]duesCase{}
	add := func(name string, month int, c duesCase) { m[duesKey{name, month}] = c }

	// Paid before Uruni: their own days in those months stay empty.
	for _, pre := range prepaidBeforeUruni {
		for _, month := range pre.months {
			add(pre.name, month, duesCase{kind: duesPrepaid})
		}
	}

	// Mas Rizki falls behind from M-5 and never catches up; Mbak Wulan from M-3.
	for month := -5; month <= thisMonth; month++ {
		add("Mas Rizki", month, duesCase{kind: duesSkipped})
	}
	for month := -3; month <= thisMonth; month++ {
		add("Mbak Wulan", month, duesCase{kind: duesSkipped})
	}

	// Mas Yoga resigns in M-4 owing that month.
	add("Mas Yoga", -4, duesCase{kind: duesSkipped})

	// Months paid in pairs, the first of the pair on the second one's day.
	add("Mas Yoga", -11, duesCase{kind: duesWithNext})
	add("Mbak Sari", -4, duesCase{kind: duesWithNext})
	add("Mbak Indri", -8, duesCase{kind: duesWithNext})

	// Part payments: Mbak Dini never completes hers, Mas Pandu tops his up a month
	// later (a separate event in story.go).
	add("Mbak Dini", -7, duesCase{kind: duesPartial, amount: 15_000})
	add("Mas Pandu", -2, duesCase{kind: duesPartial, amount: 15_000})

	// Mbak Dini pays ahead in M-1: this month, the current one and the next.
	add("Mbak Dini", -1, duesCase{kind: duesAhead, months: 2})
	add("Mbak Dini", 0, duesCase{kind: duesPrepaid})

	// This month, not everyone has paid yet, and one has paid only part.
	add("Mas Pandu", 0, duesCase{kind: duesSkipped})
	add("Mbak Anisa", 0, duesCase{kind: duesSkipped})
	add("Mbak Indri", 0, duesCase{kind: duesPartial, amount: 20_000})
	return m
}()
