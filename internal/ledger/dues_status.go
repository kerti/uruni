package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

		status := DuesStatusUnpaid
		switch {
		case paid >= owed:
			status = DuesStatusPaid
		case paid > 0:
			status = DuesStatusPartial
		}
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
