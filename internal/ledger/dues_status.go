package ledger

import (
	"context"
	"database/sql"
	"errors"
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
	Member     store.Member
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
//   - member.tier_id IS NULL means no dues obligation: excluded, never shown
//     as "unpaid".
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
		if m.TierID == nil {
			continue // no dues obligation
		}
		if !memberOwesPeriod(m, period) {
			continue // outside the joined_on..inactive_on window
		}

		// Accepted limitation (ADR-024): member.tier_id names the member's CURRENT
		// tier, not the tier effective at `period`, so a period before a promotion
		// can be misstated. See
		// TestDuesStatusForPeriodMidYearPromotionAppliesCurrentTierToPastPeriodsAcceptedLimitation.
		rate, err := l.q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{
			TierID: *m.TierID, EffectiveFrom: period,
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue // tier has no rate effective for this period ("madya TBD")
		}
		if err != nil {
			return nil, fmt.Errorf("effective dues rate for tier %d: %w", *m.TierID, err)
		}

		owed := money.FromDB(rate.Amount)
		paid := paidByMember[m.ID] // zero value if the member paid nothing this period

		status := classifyDuesStatus(owed, paid)
		var through string
		if status == DuesStatusPaid {
			if latest, ok := latestByMember[m.ID]; ok && latest > period {
				status = DuesStatusPaidInAdvance
				through, err = l.paidThrough(ctx, fundID, m, period, latest)
				if err != nil {
					return nil, err
				}
			}
		}

		statuses = append(statuses, MemberDuesStatus{
			Member: m, OwedAmount: owed, PaidAmount: paid, Status: status, PaidThrough: through,
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
// reckoned in Asia/Jakarta (tz.Jakarta), never the server's zone: production
// runs UTC, so on a month's last evening the host clock still reads the old
// month while the treasurer is in the next (#379). The report's "current
// month" is reckoned the same way (ADR-035).
func CurrentDuesPeriod(now time.Time) string {
	return now.In(tz.Jakarta).Format(duesPeriodLayout)
}

// OutstandingDuesForMember returns, oldest first, every period fundID's
// member memberID still owes something for: the per-period derivation
// DuesStatusForPeriod applies to its roster, walked across a range of
// periods for one member. A square member gets an empty slice and no error:
// "you owe nothing" is a normal answer.
//
// through bounds the end of the range ("YYYY-MM"); empty defaults to
// CurrentDuesPeriod(now). now is injected so a test can stand on either side
// of a Jakarta month boundary (as ReportParams.Now does); it is read only
// when through is empty.
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
//   - member.tier_id == nil: no obligation, empty result.
//   - memberOwesPeriod bounds every period, both ends inclusive by month.
//   - a period whose tier has no dues_rate effective yet is skipped, not
//     invented, and does not truncate the walk: a later period can still be
//     owed once a rate exists.
//   - the mid-year-promotion limitation (ADR-024) is accepted and carried
//     over unchanged; see
//     TestDuesStatusForPeriodMidYearPromotionAppliesCurrentTierToPastPeriodsAcceptedLimitation.
//
// A period appears only when classifyDuesStatus says Unpaid or Partial; Paid
// and PaidInAdvance are "nothing outstanding" and dropped. So unlike
// DuesStatusForPeriod it needs no LatestDuesPeriodPaidByMember call.
//
// It uses l.q directly rather than withTx, like DuesStatusForPeriod (ADR-027).
func (l *Ledger) OutstandingDuesForMember(ctx context.Context, fundID, memberID int64, through string, now time.Time) ([]OutstandingDuesPeriod, error) {
	if through == "" {
		through = CurrentDuesPeriod(now)
	} else if err := validateDuesPeriod(through); err != nil {
		return nil, err
	}

	member, err := l.q.GetMemberForFund(ctx, store.GetMemberForFundParams{ID: memberID, FundID: fundID})
	if err != nil {
		return nil, fmt.Errorf("fetching member: %w", err)
	}

	if member.TierID == nil {
		return nil, nil // no dues obligation
	}

	// The end of the walk is `through` alone. inactive_on is deliberately not
	// narrowed in here: memberOwesPeriod in the loop already refuses every period
	// after the month a member left, and a second bound would be a second
	// definition of the window rule that can drift. The cost is a few string
	// comparisons before the rate query, so no extra database work.
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

		// Accepted limitation (ADR-024); see DuesStatusForPeriod.
		rate, err := l.q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{
			TierID: *member.TierID, EffectiveFrom: period,
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue // tier has no rate effective for this period ("madya TBD")
		}
		if err != nil {
			return nil, fmt.Errorf("effective dues rate for tier %d: %w", *member.TierID, err)
		}

		owed := money.FromDB(rate.Amount)
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
// member's active window ends the run; a period whose tier has no effective
// rate owes nothing, so it is skipped without breaking the run; a period
// paid below its effective rate breaks it, so a rate rise in a prepaid month
// reads honestly as not paid through.
func (l *Ledger) paidThrough(ctx context.Context, fundID int64, m store.Member, period, latest string) (string, error) {
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
		rate, err := l.q.GetEffectiveDuesRate(ctx, store.GetEffectiveDuesRateParams{
			TierID: *m.TierID, EffectiveFrom: next,
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue // nothing owed this period - skipped, not a break
		}
		if err != nil {
			return "", fmt.Errorf("effective dues rate for tier %d: %w", *m.TierID, err)
		}
		if classifyDuesStatus(money.FromDB(rate.Amount), paidByPeriod[next]) != DuesStatusPaid {
			return through, nil
		}
		through = next
	}
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
