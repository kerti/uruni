package ledger

import (
	"context"
	"fmt"
	"sort"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ParticipationState classifies one expected member's standing against an
// envelope's contributions (ADR-034) - the envelope's counterpart of
// DuesStatus, deliberately not sharing its vocabulary: a sumbangan is not a
// debt, and CONTEXT.md keeps the two words apart.
type ParticipationState string

const (
	// ParticipationSudah is an expected member who has contributed anything
	// at all - even below a minimum, when no minimum is set.
	ParticipationSudah ParticipationState = "sudah"
	// ParticipationBelum is an expected member who has contributed nothing.
	ParticipationBelum ParticipationState = "belum"
	// ParticipationKurang is an expected member who has contributed
	// something, but less than the envelope's own minimum - only reachable
	// when a minimum is set at all (ADR-034); a contribution below zero
	// minimum-worth of nothing is Belum, not Kurang.
	ParticipationKurang ParticipationState = "kurang"
)

// MemberParticipation is one expected member's row in an envelope's
// participation table.
type MemberParticipation struct {
	Member            store.Member
	ContributedAmount money.Amount
	State             ParticipationState
}

// UnexpectedContribution is a contribution from someone the envelope did not
// expect - a member who joined after it opened, or a recipient who gave
// anyway (ADR-034). Always its own list: never folded into the expected
// table and never read as "not yet".
type UnexpectedContribution struct {
	Member            store.Member
	ContributedAmount money.Amount
}

// IncidentalParticipation is one envelope's participation table (ADR-034,
// #211): every expected member and where they stand, plus anyone who
// contributed without being expected.
type IncidentalParticipation struct {
	Expected   []MemberParticipation
	Unexpected []UnexpectedContribution
}

// GetIncidentalParticipation derives one envelope's participation table
// entirely from the ledger and the envelope's own facts (CLAUDE.md rule 2) -
// nothing here is stored.
//
// Expected is every member active on the day the envelope opened -
// joined_on NULL or <= opened_on, inactive_on NULL or > opened_on, tier
// ignored entirely (unlike dues) - minus its recipients. Fixed by that one
// date, so the list never shifts when someone later leaves.
//
// Contributed is summed per member by ContributedByIncidentalMember, which
// already excludes a row some reversal points at - the same NOT EXISTS
// exclusion DuesPaidByPeriod uses (ADR-029), so a reversed contribution
// disappears rather than counting twice. A member who contributed without
// being expected - a later joiner, or a recipient who gave anyway - is never
// folded into the expected table; they surface in Unexpected instead,
// however much they gave (ADR-034: "never shown as not yet").
//
// State per expected member: Sudah for anything contributed, Belum for
// nothing, and Kurang only when the envelope's own minimum is set and the
// sum is below it - a contribution below no stated minimum is still Sudah,
// not Kurang.
//
// Both result slices are in ListMembersByFund's order - by name, ignoring
// case - the order every member list reads.
//
// This is a read: it uses l.q directly rather than withTx, the same reason
// DuesStatusForPeriod does (ADR-027) - a handful of consistent SELECTs with
// no write in between.
func (l *Ledger) GetIncidentalParticipation(ctx context.Context, fundID, purposeID int64) (IncidentalParticipation, error) {
	return l.incidentalParticipation(ctx, fundID, purposeID, nil)
}

// incidentalParticipation is GetIncidentalParticipation counting only
// contributions dated on or before through, nil for all of them - the public
// report's past-month envelope cards (#408).
func (l *Ledger) incidentalParticipation(ctx context.Context, fundID, purposeID int64, through *string) (IncidentalParticipation, error) {
	envelope, err := l.q.GetIncidental(ctx, store.GetIncidentalParams{PurposeID: purposeID, FundID: fundID})
	if err != nil {
		return IncidentalParticipation{}, fmt.Errorf("fetching incidental: %w", err)
	}

	recipientRows, err := l.q.ListIncidentalRecipients(ctx, purposeID)
	if err != nil {
		return IncidentalParticipation{}, fmt.Errorf("listing incidental recipients: %w", err)
	}
	isRecipient := make(map[int64]bool, len(recipientRows))
	for _, r := range recipientRows {
		isRecipient[r.MemberID] = true
	}

	members, err := l.q.ListMembersByFund(ctx, fundID)
	if err != nil {
		return IncidentalParticipation{}, fmt.Errorf("listing members: %w", err)
	}
	membersByID := make(map[int64]store.Member, len(members))
	for _, m := range members {
		membersByID[m.ID] = m
	}

	contributionRows, err := l.q.ContributedByIncidentalMember(ctx, store.ContributedByIncidentalMemberParams{
		FundID: fundID, PurposeID: purposeID, Through: through,
	})
	if err != nil {
		return IncidentalParticipation{}, fmt.Errorf("contributed by incidental member: %w", err)
	}
	contributedByMember := make(map[int64]money.Amount, len(contributionRows))
	for _, row := range contributionRows {
		if row.MemberID == nil {
			continue // the schema's own CHECK never actually allows this row shape; defensive, not load-bearing
		}
		contributedByMember[*row.MemberID] = money.FromDB(row.ContributedAmount)
	}

	var minimum *money.Amount
	if envelope.MinimumPerMember != nil {
		v := money.FromDB(*envelope.MinimumPerMember)
		minimum = &v
	}

	expectedIDs := make(map[int64]bool, len(members))
	var expected []MemberParticipation
	for _, m := range members {
		if isRecipient[m.ID] || !memberActiveOn(m, envelope.OpenedOn) {
			continue
		}
		expectedIDs[m.ID] = true
		contributed := contributedByMember[m.ID] // zero value if the member gave nothing

		var state ParticipationState
		switch {
		case contributed <= 0:
			state = ParticipationBelum
		case minimum != nil && contributed < *minimum:
			state = ParticipationKurang
		default:
			state = ParticipationSudah
		}

		expected = append(expected, MemberParticipation{
			Member: m, ContributedAmount: contributed, State: state,
		})
	}
	// expected is already in ListMembersByFund's order (by name, ignoring
	// case), since it was built walking that list.

	var unexpected []UnexpectedContribution
	for _, row := range contributionRows {
		if row.MemberID == nil || expectedIDs[*row.MemberID] {
			continue
		}
		m, ok := membersByID[*row.MemberID]
		if !ok {
			continue // the composite FK guarantees this member exists; defensive only
		}
		unexpected = append(unexpected, UnexpectedContribution{
			Member: m, ContributedAmount: money.FromDB(row.ContributedAmount),
		})
	}
	// The contribution rows come in no order; place each giver where
	// ListMembersByFund puts them, so both tables read in one order.
	rank := make(map[int64]int, len(members))
	for i, m := range members {
		rank[m.ID] = i
	}
	sort.Slice(unexpected, func(i, j int) bool {
		return rank[unexpected[i].Member.ID] < rank[unexpected[j].Member.ID]
	})

	return IncidentalParticipation{Expected: expected, Unexpected: unexpected}, nil
}

// memberActiveOn reports whether m was active on date (a "YYYY-MM-DD"
// calendar day): joined_on NULL or <= date, inactive_on NULL or > date
// (ADR-034). Unlike memberOwesPeriod (dues_status.go) this compares whole
// dates, not months - an envelope opens on a day, not a period, and ADR-034
// is explicit the expected list is "fixed by one date."
func memberActiveOn(m store.Member, date string) bool {
	if m.JoinedOn != nil && *m.JoinedOn > date {
		return false
	}
	if m.InactiveOn != nil && *m.InactiveOn <= date {
		return false
	}
	return true
}
