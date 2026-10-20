package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// DuesStatus classifies one member's standing for one dues period, derived
// entirely from the ledger (CLAUDE.md rule 2) - nothing here is stored.
type DuesStatus string

const (
	// DuesStatusUnpaid is a member who owes this period and has paid nothing.
	DuesStatusUnpaid DuesStatus = "unpaid"
	// DuesStatusPartial is a member who paid something, but less than their
	// tier's effective rate.
	DuesStatusPartial DuesStatus = "partial"
	// DuesStatusPaid is a member who paid at least their tier's effective rate.
	// An overpayment is still Paid (see DuesStatusForPeriod).
	DuesStatusPaid DuesStatus = "paid"
	// DuesStatusPaidInAdvance is a member who fully paid this period and also a
	// later one (PRD 7.3: "some pay several months at once").
	DuesStatusPaidInAdvance DuesStatus = "paid_in_advance"
)

// MemberDuesStatus is one row of DuesStatusForPeriod's roster: a member who
// owes dues for the period, what their tier's rate says they owe, what they
// paid toward it, and the derived status.
type MemberDuesStatus struct {
	// Member.TierID is the tier at the requested period, the one that priced
	// OwedAmount (ADR-040): a row must not show a tier beside an amount that tier
	// did not set. It is not the member's current tier.
	Member     Member
	OwedAmount money.Amount
	PaidAmount money.Amount
	Status     DuesStatus
	// PaidThrough is the last period of the unbroken run of fully paid periods
	// starting at the requested one (#357), "lunas sampai". Set only when Status
	// is DuesStatusPaidInAdvance and the run reaches past the requested period.
	// See paidThrough.
	PaidThrough string
}

// DuesStatusForPeriod returns one row per member who owes dues for period
// ("YYYY-MM"), across the whole fund, classified unpaid / partial / paid /
// paid in advance.
//
// Who is on the roster at all:
//   - A member with no tier in force at the period - no member_tier row reaches
//     back that far, or the row in force says "no tier" - has no dues
//     obligation: excluded, never shown as "unpaid". The tier is the one in
//     force at the period, not today's (ADR-040), so promoting a member in June
//     leaves January priced at the old tier.
//   - A member whose tier has no dues_rate effective for this period (the
//     "madya TBD" case, PRD 6) is omitted. Uruni does not invent an amount.
//   - joined_on / inactive_on bound the active window, inclusive by month at
//     both ends: a member owes every period from the month containing
//     joined_on through the month containing inactive_on, and a partial month
//     counts in full (dues are one monthly amount paid after payday, not
//     pro-rated). joined_on is always set (#471: history starts at adoption,
//     PRD 7.1); inactive_on == nil means still active.
//
// Overpayment (paid > owed for this period) reads as Paid, not a fifth
// status: the treasurer's question is "did they clear what they owe". The
// extra is visible in PaidAmount; PRD 7.3 asks for a paid/partial/unpaid/
// ahead view, not a running credit ledger.
//
// Paid in advance upgrades Paid: a period reads "paid in advance" only when
// it is itself fully covered AND LatestDuesPeriodPaidByMember shows a later
// period was also paid. A member who paid March but skipped February is
// Unpaid for February. Read-only: it reports state without nagging,
// escalating or counting days overdue (PRD 7.3, PRD 4).
//
// It uses l.q directly rather than withTx: a handful of consistent SELECTs
// with no write in between (ADR-027).
func (l *Ledger) DuesStatusForPeriod(ctx context.Context, fundID int64, period string) ([]MemberDuesStatus, error) {
	if err := validateDuesPeriod(period); err != nil {
		return nil, err
	}

	members, err := l.q.ListMembersByFund(ctx, fundID)
	if err != nil {
		return nil, fmt.Errorf("listing members: %w", err)
	}

	// The whole roster is priced from two reads folded per member, not a query
	// per member (ADR-040).
	tierRows, err := l.q.ListMemberTiersByFund(ctx, fundID)
	if err != nil {
		return nil, fmt.Errorf("listing member tiers: %w", err)
	}
	rateRows, err := l.q.ListDuesRatesForPricing(ctx, fundID)
	if err != nil {
		return nil, fmt.Errorf("listing dues rates: %w", err)
	}
	prices := newPricing(tierRows, rateRows)

	paidRows, err := l.q.DuesPaidByPeriod(ctx, store.DuesPaidByPeriodParams{FundID: fundID, DuesPeriod: &period})
	if err != nil {
		return nil, fmt.Errorf("dues paid by period: %w", err)
	}
	paidByMember := make(map[int64]money.Amount, len(paidRows))
	for _, row := range paidRows {
		if row.MemberID == nil {
			continue
		}
		paidByMember[*row.MemberID] = money.FromDB(row.PaidAmount)
	}

	latestRows, err := l.q.LatestDuesPeriodPaidByMember(ctx, fundID)
	if err != nil {
		return nil, fmt.Errorf("latest dues period paid by member: %w", err)
	}
	latestByMember := make(map[int64]string, len(latestRows))
	for _, row := range latestRows {
		if row.MemberID == nil {
			continue
		}
		latestByMember[*row.MemberID] = row.LatestPeriod
	}

	var statuses []MemberDuesStatus
	for _, m := range members {
		if !memberOwesPeriod(m, period) {
			continue // outside the joined_on..inactive_on window
		}

		// The tier in force at `period` and the rate that tier carried then. Either
		// missing means nothing is owed: no tier is no obligation, and a tier with no
		// rate yet effective is the "madya TBD" case.
		tierID, rate, ok := prices.priceAt(m.ID, period)
		if !ok {
			continue
		}

		owed := money.FromDB(rate)
		paid := paidByMember[m.ID] // zero value if the member paid nothing this period

		status := classifyDuesStatus(owed, paid)
		var through string
		if status == DuesStatusPaid {
			if latest, ok := latestByMember[m.ID]; ok && latest > period {
				status = DuesStatusPaidInAdvance
				through, err = l.paidThrough(ctx, fundID, m, prices, period, latest)
				if err != nil {
					return nil, err
				}
			}
		}

		statuses = append(statuses, MemberDuesStatus{
			Member: Member{Member: m, TierID: &tierID}, OwedAmount: owed, PaidAmount: paid, Status: status, PaidThrough: through,
		})
	}

	return statuses, nil
}

// OutstandingDuesPeriod is one period a member still owes something for.
// Unlike MemberDuesStatus it carries no store.Member (the caller named the
// member), and Status is only DuesStatusUnpaid or DuesStatusPartial:
// OutstandingDuesForMember never returns a Paid or PaidInAdvance period.
type OutstandingDuesPeriod struct {
	Period     string // "YYYY-MM"
	OwedAmount money.Amount
	PaidAmount money.Amount
	Status     DuesStatus
}

// CurrentDuesPeriod is the dues period ("YYYY-MM") that now falls in,
// reckoned in loc, the fund's time zone (ADR-041), never the server's zone:
// production runs UTC, so on a month's last evening the host clock still reads
// the old month while the treasurer is in the next (#379), and a WIT fund
// turns the month over two hours before a WIB one. The report's "current
// month" is reckoned the same way (ADR-035). It is the one seam every
// "current month" read goes through; loc comes from FundLocation.
func CurrentDuesPeriod(now time.Time, loc *time.Location) string {
	return now.In(loc).Format(duesPeriodLayout)
}

// FundLocation is the fund's time zone as a *time.Location (ADR-041). A name
// tz.Load refuses cannot get past the schema's CHECK, so an error here means the
// database is broken: the caller fails the request and logs it, and nothing
// falls back to UTC or WIB.
func FundLocation(f store.Fund) (*time.Location, error) {
	loc, err := tz.Load(f.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("fund %d has an unusable time zone: %w", f.ID, err)
	}
	return loc, nil
}

// OutstandingDuesForMember returns, oldest first, every period fundID's
// member memberID still owes something for: the per-period derivation
// DuesStatusForPeriod applies to its roster, walked across a range of
// periods for one member. A square member gets an empty slice and no error:
// "you owe nothing" is a normal answer.
//
// through bounds the end of the range ("YYYY-MM"); empty defaults to
// CurrentDuesPeriod(now, the fund's zone). now is injected so a test can stand
// on either side of a month boundary (as ReportParams.Now does); it is read
// only when through is empty.
//
// The range:
//   - End: through. A member who left owes nothing after they left, but that
//     bound is memberOwesPeriod's inside the loop, not a second narrowing
//     here: one window rule, one owner.
//   - Start: the month containing joined_on, as memberOwesPeriod bounds it.
//     joined_on is always set (#471), so the walk never borrows a start from
//     the tier's earliest dues_rate, which would let a rate backdated for one
//     member's live arrears (PRD 7.1) put the same periods on everyone in
//     that tier.
//
// Within the range DuesStatusForPeriod's rules decide what is owed:
//   - a member with no tier in any period (no member_tier row, or only rows
//     saying "no tier"): no obligation, empty result.
//   - each period is priced at the tier in force then, not today's (ADR-040), so
//     a promotion never reaches back and the chain of arrears follows the tiers
//     the member was actually in.
//   - memberOwesPeriod bounds every period, both ends inclusive by month.
//   - a period with no tier in force, or whose tier has no dues_rate effective
//     yet, is skipped, not invented, and does not truncate the walk: a later
//     period can still be owed once a tier or rate exists.
//
// A period appears only when classifyDuesStatus says Unpaid or Partial; Paid
// and PaidInAdvance are "nothing outstanding" and dropped. So unlike
// DuesStatusForPeriod it needs no LatestDuesPeriodPaidByMember call.
//
// It uses l.q directly rather than withTx, like DuesStatusForPeriod (ADR-027).
// It reads the member's tier timeline and those tiers' rates once and folds
// them per period, a constant number of queries however long the walk.
func (l *Ledger) OutstandingDuesForMember(ctx context.Context, fundID, memberID int64, through string, now time.Time) ([]OutstandingDuesPeriod, error) {
	if through == "" {
		fund, err := l.q.GetFund(ctx, fundID)
		if err != nil {
			return nil, fmt.Errorf("fetching fund: %w", err)
		}
		loc, err := FundLocation(fund)
		if err != nil {
			return nil, err
		}
		through = CurrentDuesPeriod(now, loc)
	} else if err := validateDuesPeriod(through); err != nil {
		return nil, err
	}

	member, err := l.q.GetMemberForFund(ctx, store.GetMemberForFundParams{ID: memberID, FundID: fundID})
	if err != nil {
		return nil, fmt.Errorf("fetching member: %w", err)
	}

	prices, err := l.memberPricing(ctx, fundID, memberID)
	if err != nil {
		return nil, err
	}
	if !prices.hasTier(memberID) {
		return nil, nil // no dues obligation, in any period
	}

	// The end of the walk is `through` alone. inactive_on is deliberately not
	// narrowed in here: memberOwesPeriod in the loop already refuses every period
	// after the month a member left, and a second bound would be a second
	// definition of the window rule that can drift. The cost is a few string
	// comparisons before the rate lookup, which reads rows already in memory.
	end := through

	start := member.JoinedOn[:7]
	if start > end {
		return nil, nil
	}

	startT, err := time.Parse(duesPeriodLayout, start)
	if err != nil {
		return nil, fmt.Errorf("parsing range start %q: %w", start, err)
	}
	endT, err := time.Parse(duesPeriodLayout, end)
	if err != nil {
		return nil, fmt.Errorf("parsing range end %q: %w", end, err)
	}

	paidRows, err := l.q.DuesPaidByMemberGroupedByPeriod(ctx, store.DuesPaidByMemberGroupedByPeriodParams{
		FundID: fundID, MemberID: &memberID,
	})
	if err != nil {
		return nil, fmt.Errorf("dues paid by member: %w", err)
	}
	paidByPeriod := make(map[string]money.Amount, len(paidRows))
	for _, row := range paidRows {
		paidByPeriod[row.DuesPeriod] = money.FromDB(row.PaidAmount)
	}

	var outstanding []OutstandingDuesPeriod
	for t := startT; !t.After(endT); t = t.AddDate(0, 1, 0) {
		period := t.Format(duesPeriodLayout)
		if !memberOwesPeriod(member, period) {
			continue // outside the joined_on..inactive_on window
		}

		// The tier in force at this period and its rate then (ADR-040); see
		// DuesStatusForPeriod. Neither found means nothing is owed this period.
		_, rate, ok := prices.priceAt(memberID, period)
		if !ok {
			continue
		}

		owed := money.FromDB(rate)
		paid := paidByPeriod[period] // zero value if the member paid nothing this period

		status := classifyDuesStatus(owed, paid)
		if status == DuesStatusPaid {
			continue // square for this period - not outstanding
		}

		outstanding = append(outstanding, OutstandingDuesPeriod{
			Period: period, OwedAmount: owed, PaidAmount: paid, Status: status,
		})
	}

	return outstanding, nil
}

// ArrearsMonthsForMember is Anggota's roster badge (#233, ADR-032): N, in
// whole months, of periods strictly BEFORE currentPeriod that fundID's member
// memberID still owes something for. It is a thin count over
// OutstandingDuesForMember, not a second derivation: every rule deciding who
// owes what lives once, there and in DuesStatusForPeriod beneath it.
//
// currentPeriod is excluded by construction (OutstandingDuesForMember is
// asked for everything through the month before it). ADR-032 spells out why:
// on the first of every month the whole roster would flip to "owing", and
// early in a month the badge could not tell a member who has not paid yet
// (benign) from one six months behind (the only case the badge exists to
// surface). A member who joined this period, or paid ahead of it, reads zero
// arrears with no special case: neither has anything before currentPeriod.
//
// currentPeriod is passed in rather than computed with a second clock read.
// GET /api/members has no ?through= or ?period= to override it (ADR-032
// keeps a period selector out of Anggota), so the handler computes it once
// per request with CurrentDuesPeriod and hands it to every member's call and
// to ListMembersPage's current_rate column, so a clock tick cannot make a
// row's current_rate and arrears count disagree about "now".
//
// A part-paid earlier period counts as one month, like an unpaid one
// (ADR-032): OutstandingDuesForMember makes no Unpaid/Partial distinction,
// so len() of its result is the count.
func (l *Ledger) ArrearsMonthsForMember(ctx context.Context, fundID, memberID int64, currentPeriod string) (int, error) {
	if err := validateDuesPeriod(currentPeriod); err != nil {
		return 0, err
	}

	t, err := time.Parse(duesPeriodLayout, currentPeriod)
	if err != nil {
		return 0, fmt.Errorf("parsing current period %q: %w", currentPeriod, err)
	}
	through := t.AddDate(0, -1, 0).Format(duesPeriodLayout)

	outstanding, err := l.OutstandingDuesForMember(ctx, fundID, memberID, through, time.Time{}) // through is non-empty: now is never read
	if err != nil {
		return 0, err
	}
	return len(outstanding), nil
}

// classifyDuesStatus derives unpaid / partial / paid from owed vs paid alone,
// for one member and one period: the comparison DuesStatusForPeriod (followed
// by its paid-in-advance upgrade) and OutstandingDuesForMember (followed by
// dropping Paid) both build on. Extracted so "paid enough" has one definition.
func classifyDuesStatus(owed, paid money.Amount) DuesStatus {
	switch {
	case paid >= owed:
		return DuesStatusPaid
	case paid > 0:
		return DuesStatusPartial
	default:
		return DuesStatusUnpaid
	}
}

// paidThrough walks forward from the period after `period` and returns the
// last of the unbroken run of fully paid periods (#357), or "" when the very
// next owed period is not fully paid: a member who paid October and December
// but skipped November is paid through October, not December. `latest` is
// LatestDuesPeriodPaidByMember's value and bounds the walk.
//
// Each step applies DuesStatusForPeriod's rules: a period outside the
// member's active window ends the run; a period with no tier in force or no
// effective rate owes nothing, so it is skipped without breaking the run; a
// period paid below what its own tier and rate say breaks it, so a rate or tier
// rise in a prepaid month reads honestly as not paid through. Each period is
// priced at the tier in force then (ADR-040), through prices.
func (l *Ledger) paidThrough(ctx context.Context, fundID int64, m store.Member, prices pricing, period, latest string) (string, error) {
	paidRows, err := l.q.DuesPaidByMemberGroupedByPeriod(ctx, store.DuesPaidByMemberGroupedByPeriodParams{
		FundID: fundID, MemberID: &m.ID,
	})
	if err != nil {
		return "", fmt.Errorf("dues paid by member: %w", err)
	}
	paidByPeriod := make(map[string]money.Amount, len(paidRows))
	for _, row := range paidRows {
		paidByPeriod[row.DuesPeriod] = money.FromDB(row.PaidAmount)
	}

	t, err := time.Parse(duesPeriodLayout, period)
	if err != nil {
		return "", fmt.Errorf("parsing period %q: %w", period, err)
	}

	var through string
	for {
		t = t.AddDate(0, 1, 0)
		next := t.Format(duesPeriodLayout)
		if next > latest || !memberOwesPeriod(m, next) {
			return through, nil
		}
		_, rate, ok := prices.priceAt(m.ID, next)
		if !ok {
			continue // nothing owed this period - skipped, not a break
		}
		if classifyDuesStatus(money.FromDB(rate), paidByPeriod[next]) != DuesStatusPaid {
			return through, nil
		}
		through = next
	}
}

// memberPricing fetches what prices one member's walk: their standing tier rows
// and the rates of the tiers those rows name, two queries however many periods
// follow.
func (l *Ledger) memberPricing(ctx context.Context, fundID, memberID int64) (pricing, error) {
	tierRows, err := l.q.ListMemberTiersForMember(ctx, store.ListMemberTiersForMemberParams{FundID: fundID, MemberID: memberID})
	if err != nil {
		return pricing{}, fmt.Errorf("listing member tiers: %w", err)
	}
	rateRows, err := l.q.ListDuesRatesForMemberPricing(ctx, store.ListDuesRatesForMemberPricingParams{FundID: fundID, MemberID: memberID})
	if err != nil {
		return pricing{}, fmt.Errorf("listing dues rates: %w", err)
	}
	return newPricing(tierRows, rateRows), nil
}

// memberOwesPeriod reports whether period falls within the member's active
// window: the month containing joined_on through the month containing
// inactive_on, both ends inclusive. Both are "YYYY-MM-DD", so their first
// seven characters are the containing "YYYY-MM", comparable lexicographically
// against period like dues_period itself (ADR-024).
func memberOwesPeriod(m store.Member, period string) bool {
	if period < m.JoinedOn[:7] {
		return false
	}
	if m.InactiveOn != nil && period > (*m.InactiveOn)[:7] {
		return false
	}
	return true
}
