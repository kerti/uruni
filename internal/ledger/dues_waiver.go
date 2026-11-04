package ledger

import (
	"context"
	"fmt"

	"github.com/kerti/uruni/internal/store"
)

// WaiveDuesParams is a member's months to waive, with one note shared by all of
// them (ADR-050): a waiver has no sum, so several months at once are several
// rows carrying the same words.
type WaiveDuesParams struct {
	FundID   int64
	MemberID int64
	Periods  []string // "YYYY-MM", each a month the member still owes
	Note     *string  // optional; blank is stored as nothing
}

// UnwaiveDuesParams names the one waived month to restore.
type UnwaiveDuesParams struct {
	FundID   int64
	MemberID int64
	Period   string // "YYYY-MM"
}

// WaiveDues waives a member's dues for each of Periods (ADR-050): the group's
// decision that those months stop being owed. It writes one dues_waiver row per
// month and nothing else - no ledger row, no balance, no reconciliation figure
// moves.
//
// All or nothing: every period is checked before the first row is written, and
// the rows go in one transaction, so a refusal on month N leaves none.
//
// A month may be waived exactly when OutstandingDuesForMember would offer it -
// inside the member's active window, with a tier and a rate in force, and still
// owed (unpaid or partly paid), from the first month up to duesMonthsAhead past
// the current one. The check is that very walk, so the payment form and the
// waiver can never disagree. A partly paid month can be waived: the payment
// stays paid and the waiver covers what was left. Otherwise:
//   - a month already waived is ErrDuesAlreadyWaived;
//   - a month the member does not owe (paid in full or ahead, outside the window,
//     no rate) is ErrDuesNotOwed - giving back money paid is a refund, a keluar;
//   - a malformed month is ErrInvalidArgument, one before the floor or past the
//     window ErrDateOutOfRange; an empty or repeating Periods is
//     ErrInvalidArgument;
//   - a member of another fund, or none, reads as sql.ErrNoRows.
//
// Note is trimmed and stored as NULL when blank. Returns the created waivers in
// the order given.
func (l *Ledger) WaiveDues(ctx context.Context, p WaiveDuesParams) ([]store.DuesWaiver, error) {
	if len(p.Periods) == 0 {
		return nil, invalidField("periods", "periods must not be empty")
	}
	window, err := l.windowFor(ctx, l.q, p.FundID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(p.Periods))
	for _, period := range p.Periods {
		if err := validateDuesPeriod("periods", period, window.dues()); err != nil {
			return nil, err
		}
		if seen[period] {
			return nil, invalidField("periods", "period %q is listed twice", period)
		}
		seen[period] = true
	}

	var created []store.DuesWaiver
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		// The member is read first, inside the fund, so another fund's id is a 404
		// before any period is judged.
		if _, err := q.GetMemberForFund(ctx, store.GetMemberForFundParams{ID: p.MemberID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching member: %w", err)
		}

		existing, err := q.ListDuesWaiversByMember(ctx, store.ListDuesWaiversByMemberParams{FundID: p.FundID, MemberID: p.MemberID})
		if err != nil {
			return fmt.Errorf("listing dues waivers: %w", err)
		}
		waived := foldWaivers(existing)[p.MemberID]

		owed, err := l.outstandingDuesWith(ctx, q, p.FundID, p.MemberID, window.dues())
		if err != nil {
			return err
		}
		stillOwed := make(map[string]bool, len(owed))
		for _, o := range owed {
			stillOwed[o.Period] = true
		}

		for _, period := range p.Periods {
			if _, ok := waived[period]; ok {
				return ErrDuesAlreadyWaived
			}
			if !stillOwed[period] {
				return ErrDuesNotOwed
			}
		}

		note := normalizeNote(p.Note)
		for _, period := range p.Periods {
			row, err := q.CreateDuesWaiver(ctx, store.CreateDuesWaiverParams{
				FundID: p.FundID, MemberID: p.MemberID, DuesPeriod: period, Note: note, CreatedAt: l.now().Unix(),
			})
			if err != nil {
				return fmt.Errorf("creating dues waiver: %w", err)
			}
			created = append(created, row)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("waiving dues: %w", err)
	}
	return created, nil
}

// UnwaiveDues takes a member's waiver for one month back (ADR-050): the month is
// owed again, at its rate, less anything already paid toward it. It deletes the
// waiver's row - a waiver is not a ledger row, so immutability (CLAUDE.md rule 3)
// does not reach it, as clearing a talangan's waived_on does not.
//
// A month with no waiver is ErrDuesNotWaived; a malformed month is
// ErrInvalidArgument; a member of another fund, or none, reads as sql.ErrNoRows.
// A waiver outside the member's window (after a later deactivation) can be
// undone like any other.
func (l *Ledger) UnwaiveDues(ctx context.Context, p UnwaiveDuesParams) error {
	if err := checkPeriodShape("period", p.Period); err != nil {
		return err
	}
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		if _, err := q.GetMemberForFund(ctx, store.GetMemberForFundParams{ID: p.MemberID, FundID: p.FundID}); err != nil {
			return fmt.Errorf("fetching member: %w", err)
		}
		n, err := q.DeleteDuesWaiver(ctx, store.DeleteDuesWaiverParams{FundID: p.FundID, MemberID: p.MemberID, DuesPeriod: p.Period})
		if err != nil {
			return fmt.Errorf("deleting dues waiver: %w", err)
		}
		if n == 0 {
			return ErrDuesNotWaived
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("unwaiving dues: %w", err)
	}
	return nil
}
