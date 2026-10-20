package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// minJoinedOn is the earliest join date a member may be given. member_tier's
// start month has a 2000-01 floor in the schema, and a member's first tier row
// starts at the join month, so a join date before it could not be written with a
// tier. Until #632 puts the same floor on member.joined_on as a CHECK, with its
// typed error, the ledger refuses such a date here as ErrInvalidArgument rather
// than let the member_tier CHECK surface as a constraint error. #632 replaces
// this with its own check.
const minJoinedOn = "2000-01-01"

// refuseJoinedBeforeFloor refuses a well-formed date before minJoinedOn. A string
// that is not a date is left for the schema's CHECK on joined_on to refuse.
func refuseJoinedBeforeFloor(joinedOn string) error {
	t, err := time.Parse(time.DateOnly, joinedOn)
	if err != nil || t.Format(time.DateOnly) != joinedOn {
		return nil
	}
	if joinedOn < minJoinedOn {
		return fmt.Errorf("%w: joined_on %q is before %s", ErrInvalidArgument, joinedOn, minJoinedOn)
	}
	return nil
}

// Member is a member row with the one fact that is no longer a column of it: the
// tier (ADR-040). The tier lives in member_tier, month by month, so a bare
// store.Member cannot carry it.
//
// TierID's meaning depends on who built the value, and the builder says which:
// every method of this file returns the member's CURRENT tier (the tier at the
// later of the current month and the join month; nil is no tier), and
// MemberDuesStatus.Member carries the tier at the requested period.
type Member struct {
	store.Member
	TierID *int64
}

// CreateMemberParams is every argument CreateMember needs.
type CreateMemberParams struct {
	FundID int64
	Name   string
	// TierID is the member's tier from the month of JoinedOn; nil writes no
	// member_tier row (no dues obligation), as ADR-040 requires.
	TierID *int64
	// JoinedOn is "YYYY-MM-DD". The caller owns the default (today, in the zone it
	// reads "today" in); the schema's CHECK refuses a date that is not a real one.
	JoinedOn string
}

// CreateMember writes a member and, when a tier is given, its first member_tier
// row at the month of joined_on, in one transaction (ADR-040: "POST /api/members
// and Setup write the first row at the join month when a tier is given, and no
// row when it is not"). The row is bounded by the join date's own ceiling
// (#632), not by the tier-start ceiling, which applies to the edit form only.
//
// Neither the name nor the date nor the tier id is pre-checked: the schema's
// CHECKs and foreign keys refuse them (ADR-027), and the HTTP layer classifies
// the wrapped constraint error. The month is read off the stored row, which has
// already passed the date CHECK, so it can never be a short or malformed string.
func (l *Ledger) CreateMember(ctx context.Context, p CreateMemberParams) (Member, error) {
	if err := refuseJoinedBeforeFloor(p.JoinedOn); err != nil {
		return Member{}, err
	}
	var out Member
	err := l.withTx(ctx, func(q store.Querier) error {
		now := time.Now().Unix()
		m, err := q.CreateMember(ctx, store.CreateMemberParams{
			FundID: p.FundID, Name: p.Name, JoinedOn: p.JoinedOn, CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("creating member: %w", err)
		}
		out = Member{Member: m}
		if p.TierID == nil {
			return nil
		}
		if _, err := q.CreateMemberTier(ctx, store.CreateMemberTierParams{
			FundID: m.FundID, MemberID: m.ID, TierID: p.TierID,
			EffectiveFrom: m.JoinedOn[:len(duesPeriodLayout)], Correction: 0, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("writing the member's first tier: %w", err)
		}
		out.TierID = p.TierID
		return nil
	})
	if err != nil {
		return Member{}, err
	}
	return out, nil
}

// UpdateMemberParams is every argument UpdateMember needs. A nil Name or
// JoinedOn means "leave alone", as the PATCH body's absent key does; the two
// nullable facts need their Set flag, since a nil there is "clear it".
type UpdateMemberParams struct {
	FundID   int64
	MemberID int64

	Name     *string
	JoinedOn *string // "YYYY-MM-DD"; moving it earlier may append a member_tier row

	SetInactiveOn bool
	InactiveOn    *string

	// SetTier states what is true from TierFrom (ADR-040's "from a month"):
	// TierID is the tier from then on, nil meaning no tier. The engine decides
	// whether that is a new row, the next correction of a month that already has
	// one, or nothing.
	SetTier bool
	TierID  *int64
	// TierFrom is "YYYY-MM"; empty defaults to the current month, or the join
	// month if that is later.
	TierFrom string

	// Now is the clock the current month is read from (through CurrentDuesPeriod,
	// the seam that moves to the fund's zone with #639).
	Now time.Time
}

// UpdateMember changes a member's name, join date, leaving date and tier in one
// transaction and returns the member with the tier now in force. It reads before
// it writes (ADR-040), which is why member writes live here and not in direct
// CRUD (ADR-027's amendment).
//
// The tier is never overwritten. The three rules, in the order they apply:
//
//  1. Join date moved earlier. A joined_on in a month before the member's
//     earliest row appends a row at the new join month carrying that row's tier
//     (when it has one), provided that row starts at or before the OLD join
//     month. "At or before", because a join date once moved later leaves the
//     earliest row ahead of the join month. This holds "the earliest row reaches
//     back to joined_on" as an invariant, without which a live-arrears backdating
//     (PRD 7.1) would open months in which the member has no tier and owes
//     nothing. If the earliest row began after the old join month (a member
//     untiered at first), the longer untiered stretch stays untiered.
//  2. A statement "tier T from month M" is refused before the member's join month
//     and after the current month (or the join month, if that is later) with
//     ErrInvalidArgument. Otherwise it reads the tier in force at M: if that is
//     already T it writes nothing, so re-saving the form leaves no stray rows and
//     a later row is never cancelled by an earlier statement ("A from March"
//     with B from June changes nothing); else it writes the next correction of
//     month M (0 when M has no row yet).
//  3. Name and the leaving date are plain updates, with no history.
//
// Rule 2's month bounds read the join month AFTER this call's own change, and a
// tier given without TierFrom is stated from the current month or the join month
// if later.
func (l *Ledger) UpdateMember(ctx context.Context, p UpdateMemberParams) (Member, error) {
	if p.JoinedOn != nil {
		if err := refuseJoinedBeforeFloor(*p.JoinedOn); err != nil {
			return Member{}, err
		}
	}
	if p.TierFrom != "" && !p.SetTier {
		return Member{}, fmt.Errorf("%w: a tier start month needs a tier", ErrInvalidArgument)
	}
	if p.TierFrom != "" {
		if err := validateDuesPeriod(p.TierFrom); err != nil {
			return Member{}, err
		}
	}
	current := CurrentDuesPeriod(p.Now)

	var out Member
	err := l.withTx(ctx, func(q store.Querier) error {
		old, err := q.GetMemberForFund(ctx, store.GetMemberForFundParams{ID: p.MemberID, FundID: p.FundID})
		if err != nil {
			return fmt.Errorf("fetching member: %w", err)
		}
		timeline, err := q.ListMemberTiersForMember(ctx, store.ListMemberTiersForMemberParams{FundID: p.FundID, MemberID: p.MemberID})
		if err != nil {
			return fmt.Errorf("listing member tiers: %w", err)
		}

		updated, err := q.UpdateMember(ctx, store.UpdateMemberParams{
			ID: p.MemberID, Name: p.Name, JoinedOn: p.JoinedOn,
			SetInactiveOn: boolToInt(p.SetInactiveOn), InactiveOn: p.InactiveOn,
		})
		if err != nil {
			return fmt.Errorf("updating member: %w", err)
		}
		oldMonth, newMonth := old.JoinedOn[:len(duesPeriodLayout)], updated.JoinedOn[:len(duesPeriodLayout)]
		now := time.Now().Unix()

		// Rule 1: the earliest row follows the join date back.
		if newMonth < oldMonth && len(timeline) > 0 {
			earliest := timeline[0]
			if newMonth < earliest.EffectiveFrom && earliest.EffectiveFrom <= oldMonth && earliest.TierID != nil {
				row, err := q.CreateMemberTier(ctx, store.CreateMemberTierParams{
					FundID: p.FundID, MemberID: p.MemberID, TierID: earliest.TierID,
					EffectiveFrom: newMonth, Correction: 0, CreatedAt: now,
				})
				if err != nil {
					return fmt.Errorf("moving the member's first tier back to the new join month: %w", err)
				}
				timeline = append([]store.MemberTierStanding{standingOf(row)}, timeline...)
			}
		}

		// Rule 2: state the tier from a month.
		if p.SetTier {
			from := p.TierFrom
			ceiling := max(current, newMonth)
			if from == "" {
				from = ceiling
			}
			if from < newMonth {
				return fmt.Errorf("%w: the tier cannot start before the member's join month %s", ErrInvalidArgument, newMonth)
			}
			if from > ceiling {
				return fmt.Errorf("%w: the tier cannot start after %s", ErrInvalidArgument, ceiling)
			}

			prices := newPricing(timeline, nil)
			inForce, has := prices.tierAt(p.MemberID, from)
			if !sameTier(p.TierID, inForce, has) {
				latest, err := q.LatestMemberTierCorrection(ctx, store.LatestMemberTierCorrectionParams{MemberID: p.MemberID, EffectiveFrom: from})
				if err != nil {
					return fmt.Errorf("reading the month's latest correction: %w", err)
				}
				row, err := q.CreateMemberTier(ctx, store.CreateMemberTierParams{
					FundID: p.FundID, MemberID: p.MemberID, TierID: p.TierID,
					EffectiveFrom: from, Correction: latest + 1, CreatedAt: now,
				})
				if err != nil {
					return fmt.Errorf("writing the member's tier: %w", err)
				}
				timeline = withStanding(timeline, standingOf(row))
			}
		}

		out = Member{
			Member: updated,
			TierID: newPricing(timeline, nil).currentTier(p.MemberID, updated.JoinedOn, current),
		}
		return nil
	})
	if err != nil {
		return Member{}, err
	}
	return out, nil
}

// sameTier reports whether the tier asked for is the one already in force: both
// "none", or both the same tier.
func sameTier(want *int64, inForce int64, has bool) bool {
	if want == nil {
		return !has
	}
	return has && *want == inForce
}

// standingOf reads a just-written member_tier row as the standing row of its
// month: it carries the highest correction of that month, so it stands.
func standingOf(r store.MemberTier) store.MemberTierStanding {
	return store.MemberTierStanding(r)
}

// withStanding returns timeline with row in place of any row of the same month,
// kept oldest month first.
func withStanding(timeline []store.MemberTierStanding, row store.MemberTierStanding) []store.MemberTierStanding {
	out := make([]store.MemberTierStanding, 0, len(timeline)+1)
	placed := false
	for _, t := range timeline {
		switch {
		case t.EffectiveFrom == row.EffectiveFrom:
			out = append(out, row)
			placed = true
		case t.EffectiveFrom > row.EffectiveFrom && !placed:
			out = append(out, row, t)
			placed = true
		default:
			out = append(out, t)
		}
	}
	if !placed {
		out = append(out, row)
	}
	return out
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
