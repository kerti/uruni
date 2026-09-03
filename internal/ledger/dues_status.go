package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// DuesStatus classifies one member's standing for one dues period, derived
// entirely from the ledger (CLAUDE.md rule 2) - nothing here is stored.
type DuesStatus string

const (
	// DuesStatusUnpaid is a member who owes this period and has paid nothing
	// toward it.
	DuesStatusUnpaid DuesStatus = "unpaid"
	// DuesStatusPartial is a member who has paid something toward this
	// period, but less than their tier's effective rate.
	DuesStatusPartial DuesStatus = "partial"
	// DuesStatusPaid is a member who has paid at least their tier's
	// effective rate for this period. An overpayment (more than the rate)
	// is still Paid, not a distinct status - see DuesStatusForPeriod's
	// doc comment.
	DuesStatusPaid DuesStatus = "paid"
	// DuesStatusPaidInAdvance is a member who has fully paid this period
	// and has also paid a later period - PRD 7.3's "some pay several
	// months at once".
	DuesStatusPaidInAdvance DuesStatus = "paid_in_advance"
)

// MemberDuesStatus is one row of DuesStatusForPeriod's roster: one member who
// owes dues for the requested period, what their tier's rate says they owe,
// what they have actually paid toward that period, and the derived status.
type MemberDuesStatus struct {
	Member     store.Member
	OwedAmount money.Amount
	PaidAmount money.Amount
	Status     DuesStatus
}

// DuesStatusForPeriod returns one row per member who owes dues for period
// ("YYYY-MM"), across the whole fund, classified unpaid / partial / paid /
// paid in advance.
//
// Who is on the roster at all:
//   - member.tier_id IS NULL means no dues obligation - excluded entirely,
//     never shown as "unpaid".
//   - A member whose tier has no dues_rate row effective for this period
//     (the "madya TBD" case PRD 6 names) is omitted. Uruni does not invent
//     an amount and does not show an unknown one.
//   - joined_on / inactive_on bound the active window, inclusive by month at
//     both ends: a member owes every period from the month containing
//     joined_on through the month containing inactive_on, and a partial
//     month at either end counts in full - dues here are one monthly amount
//     paid after payday, not a pro-rated subscription. joined_on == nil
//     means "always was a member"; inactive_on == nil means "still active".
//
// Overpayment (paid > owed for this one period) reads as Paid, not a fifth
// status: the treasurer's question is "did they clear what they owe", and
// "yes, and then some" is still yes. The extra is visible in PaidAmount for
// anyone who wants the exact figure; PRD 7.3 asks for a paid/partial/unpaid/
// ahead view, not a running credit ledger.
//
// Paid in advance is an upgrade of Paid, not an independent branch: a period
// only reads as "paid in advance" when it is itself fully covered AND
// LatestDuesPeriodPaidByMember shows a later period was also paid. A member
// who paid March but skipped February still reads as Unpaid for February -
// "ahead" describes a period that is itself settled and then some, not a
// hole with a payment beyond it. This is a read-only method: no rows are
// ever written or removed, and it reports state without nagging, escalating
// or counting days overdue (PRD 7.3, PRD 4).
//
// This is a read: it uses l.q directly rather than withTx, because it is a
// handful of consistent SELECTs with no write in between (ADR-027).
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

		// Accepted limitation (ADR-024): member.tier_id names the member's
		// CURRENT tier, not the tier effective at `period`. A member
		// promoted mid-year has that later tier applied to every past
		// period too, so a period before the promotion can be misstated
		// here. See
		// TestDuesStatusForPeriodMidYearPromotionAppliesCurrentTierToPastPeriodsAcceptedLimitation
		// for the concrete, accepted-wrong case.
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
		if status == DuesStatusPaid {
			if latest, ok := latestByMember[m.ID]; ok && latest > period {
				status = DuesStatusPaidInAdvance
			}
		}

		statuses = append(statuses, MemberDuesStatus{
			Member: m, OwedAmount: owed, PaidAmount: paid, Status: status,
		})
	}

	return statuses, nil
}

// OutstandingDuesPeriod is one row of OutstandingDuesForMember's result: one
// period a member still owes something for. Unlike MemberDuesStatus this
// carries no store.Member - the caller already named the one member this
// whole result is about - and Status is only ever DuesStatusUnpaid or
// DuesStatusPartial: OutstandingDuesForMember never returns a period that
// came back Paid or PaidInAdvance from classifyDuesStatus, so this type has
// no representable state for either.
type OutstandingDuesPeriod struct {
	Period     string // "YYYY-MM"
	OwedAmount money.Amount
	PaidAmount money.Amount
	Status     DuesStatus
}

// OutstandingDuesForMember returns, oldest first, every period fundID's
// member memberID still owes something for - the same per-member,
// per-period derivation DuesStatusForPeriod applies to its whole roster,
// walked here across a range of periods for one member instead of across
// every member for one period. A member who is square gets an empty slice
// and no error, never a 404: "you owe nothing" is a normal answer, not a
// failure to find something.
//
// through bounds the end of the range ("YYYY-MM"); an empty string defaults
// to the server's current month. The caller, not this method, decides
// whether that default is right for its request - the HTTP handler is what
// actually cares that the server does not share the treasurer's timezone
// (issue #186).
//
// The range:
//   - End: through, or the month containing inactive_on when that is
//     earlier - a member who left owes nothing for a month after they left,
//     however far `through` reaches.
//   - Start: the month containing joined_on, exactly as memberOwesPeriod
//     bounds it for DuesStatusForPeriod. joined_on == nil means "always was
//     a member" (same meaning as everywhere else in this package), but this
//     method still needs *some* period to start walking from - scanning
//     from the epoch would mean one GetEffectiveDuesRate call per calendar
//     month since 1970 for every such member, so the start is bounded
//     instead at the tier's own earliest dues_rate.effective_from: no rate
//     was ever effective before that row exists, so no period before it
//     could ever be owed regardless of how long the member has been on the
//     roster. A tier with no dues_rate row at all (the "madya TBD" case)
//     has, by the same reasoning, never had anything owed against it -
//     nothing to walk, so this returns empty rather than picking an
//     arbitrary start.
//
// Within that range, exactly DuesStatusForPeriod's own rules decide what is
// owed and skipped - see its doc comment, unchanged here:
//   - member.tier_id == nil: no obligation at all, empty result.
//   - memberOwesPeriod bounds every period the same way, both ends
//     inclusive by month.
//   - a period whose tier has no dues_rate effective yet is skipped, not
//     invented, and does not truncate the walk - a later period can still
//     be owed once a rate exists again.
//   - the mid-year-promotion limitation (ADR-024): member.tier_id names the
//     member's CURRENT tier, so a past period can be misstated against a
//     rate that was not actually in force yet when it happened. Accepted
//     and unchanged, exactly as DuesStatusForPeriod carries it - see
//     TestDuesStatusForPeriodMidYearPromotionAppliesCurrentTierToPastPeriodsAcceptedLimitation.
//
// A period only appears in the result when classifyDuesStatus says Unpaid
// or Partial for it; Paid and PaidInAdvance are both "nothing outstanding"
// here and are silently dropped, never returned as a fourth or fifth
// status. That is also why this method needs no "paid in advance" signal of
// its own (unlike DuesStatusForPeriod, it never calls
// LatestDuesPeriodPaidByMember): a period that would read as paid in
// advance is fully covered either way, and fully covered periods do not
// appear in an "outstanding" list regardless of what, if anything, was
// paid beyond them.
//
// This is a read: it uses l.q directly rather than withTx, for the same
// reason DuesStatusForPeriod does (ADR-027) - a handful of consistent
// SELECTs with no write in between.
func (l *Ledger) OutstandingDuesForMember(ctx context.Context, fundID, memberID int64, through string) ([]OutstandingDuesPeriod, error) {
	if through == "" {
		through = time.Now().Format(duesPeriodLayout)
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

	end := through
	if member.InactiveOn != nil {
		if inactiveMonth := (*member.InactiveOn)[:7]; inactiveMonth < end {
			end = inactiveMonth
		}
	}

	var start string
	if member.JoinedOn != nil {
		start = (*member.JoinedOn)[:7]
	} else {
		rates, err := l.q.ListDuesRatesByTier(ctx, *member.TierID)
		if err != nil {
			return nil, fmt.Errorf("listing dues rates for tier %d: %w", *member.TierID, err)
		}
		if len(rates) == 0 {
			return nil, nil // tier has never had an effective rate - nothing was ever owed
		}
		start = rates[0].EffectiveFrom // ListDuesRatesByTier orders ASC - the earliest row
	}

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

		// Accepted limitation (ADR-024) - see this method's own doc comment
		// and DuesStatusForPeriod's identical call site.
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

// classifyDuesStatus derives unpaid / partial / paid from owed vs paid alone,
// for one member and one period - the three-way comparison both
// DuesStatusForPeriod (there, followed by its own paid-in-advance upgrade)
// and OutstandingDuesForMember (there, followed by dropping anything that
// comes back Paid) build on. Extracted so the comparison itself - what "paid
// enough" means - has exactly one definition in this package, not one
// per caller that happens to read the same today.
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

// memberOwesPeriod reports whether period falls within the member's active
// window: the month containing joined_on through the month containing
// inactive_on, both ends inclusive. joined_on / inactive_on are "YYYY-MM-DD"
// so their first seven characters are the "YYYY-MM" period that contains
// them, comparable lexicographically against period exactly as dues_period
// itself is (ADR-024).
func memberOwesPeriod(m store.Member, period string) bool {
	if m.JoinedOn != nil && period < (*m.JoinedOn)[:7] {
		return false
	}
	if m.InactiveOn != nil && period > (*m.InactiveOn)[:7] {
		return false
	}
	return true
}
