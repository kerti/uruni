package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// duesGridMonths is how many months the grid spans at most (ADR-044).
const duesGridMonths = 12

// DuesGridCell is one member's standing for one month of the grid: what
// DuesStatusForPeriod says, drawn as three states and the absence of a due.
type DuesGridCell string

const (
	// DuesCellNone is a month DuesStatusForPeriod returns no row for: before the
	// member joined, after they went inactive, no tier in force, or a tier with
	// no rate yet. It is the absence of a due, never "unpaid".
	DuesCellNone DuesGridCell = "none"
	// DuesCellUnpaid is a member who owes the month and paid nothing toward it.
	DuesCellUnpaid DuesGridCell = "unpaid"
	// DuesCellPartial is a member who paid something, but less than the month's rate.
	DuesCellPartial DuesGridCell = "partial"
	// DuesCellPaid is a member who paid at least the month's rate.
	// DuesStatusPaidInAdvance collapses into it: in a row of months, paying ahead
	// is already visible as the paid cells to its right.
	DuesCellPaid DuesGridCell = "paid"
)

// DuesGridRow is one member's cells, parallel to DuesGrid.Periods.
type DuesGridRow struct {
	MemberID int64
	Name     string
	Cells    []DuesGridCell
}

// DuesGrid is the twelve-month dues status table (ADR-044): Periods oldest
// first, and a row per member who owes in at least one of them, in the roster's
// order (by name). Both are empty when nobody owes in any of the twelve months.
// Carries no amounts, counts or totals; the grid is status only.
type DuesGrid struct {
	Periods []string // "YYYY-MM"
	Rows    []DuesGridRow
}

// DuesGrid returns the dues status of every member over the twelve months
// ending at endPeriod ("YYYY-MM"), cell by cell what DuesStatusForPeriod would
// say for that member and month, as of now (no as-of bound on the payments).
//
// endPeriod empty means the fund's current month; a later one is capped to it,
// both read in the fund's zone (ADR-041), so the grid never reaches past the
// present. The ledger clock is read only to find the current month.
//
// Leading months in which no member owes anything are dropped, so a young fund
// has fewer than twelve columns; only leading ones, so a gap in the middle stays.
// A member with no obligation in any shown month has no row.
//
// A fixed handful of reads however many members or months there are (#532):
// the fund, the members, the tier timelines, the rates, and the payments of the
// window, folded in memory with the rules DuesStatusForPeriod owns
// (memberOwesPeriod, pricing.priceAt, classifyDuesStatus). The paid-in-advance
// upgrade and its paidThrough walk are not needed: the grid draws both as paid.
// Like it, this uses l.q directly (ADR-027).
func (l *Ledger) DuesGrid(ctx context.Context, fundID int64, endPeriod string) (DuesGrid, error) {
	return l.duesGrid(ctx, fundID, endPeriod, l.now())
}

// duesGrid is DuesGrid at an instant its caller already read, so a report that
// reckons its own month from now finds the grid's current month at the same one.
func (l *Ledger) duesGrid(ctx context.Context, fundID int64, endPeriod string, now time.Time) (DuesGrid, error) {
	if endPeriod != "" {
		if err := checkPeriodShape("end period", endPeriod); err != nil {
			return DuesGrid{}, err
		}
	}

	fund, err := l.q.GetFund(ctx, fundID)
	if err != nil {
		return DuesGrid{}, fmt.Errorf("fetching fund: %w", err)
	}
	loc, err := FundLocation(fund)
	if err != nil {
		return DuesGrid{}, err
	}
	if current := CurrentDuesPeriod(now, loc); endPeriod == "" || endPeriod > current {
		endPeriod = current
	}
	endT, err := time.Parse(duesPeriodLayout, endPeriod)
	if err != nil {
		return DuesGrid{}, fmt.Errorf("parsing end period %q: %w", endPeriod, err)
	}
	periods := make([]string, duesGridMonths)
	for i := range periods {
		periods[i] = endT.AddDate(0, i-(duesGridMonths-1), 0).Format(duesPeriodLayout)
	}

	members, err := l.q.ListMembersByFund(ctx, fundID)
	if err != nil {
		return DuesGrid{}, fmt.Errorf("listing members: %w", err)
	}
	tierRows, err := l.q.ListMemberTiersByFund(ctx, fundID)
	if err != nil {
		return DuesGrid{}, fmt.Errorf("listing member tiers: %w", err)
	}
	rateRows, err := l.q.ListDuesRatesForPricing(ctx, fundID)
	if err != nil {
		return DuesGrid{}, fmt.Errorf("listing dues rates: %w", err)
	}
	prices := newPricing(tierRows, rateRows)

	from, to := periods[0], endPeriod
	paidRows, err := l.q.DuesPaidByPeriodRange(ctx, store.DuesPaidByPeriodRangeParams{FundID: fundID, FromPeriod: &from, ToPeriod: &to})
	if err != nil {
		return DuesGrid{}, fmt.Errorf("dues paid by period range: %w", err)
	}
	paid := make(map[int64]map[string]money.Amount)
	for _, row := range paidRows {
		if row.MemberID == nil {
			continue
		}
		byPeriod := paid[*row.MemberID]
		if byPeriod == nil {
			byPeriod = make(map[string]money.Amount)
			paid[*row.MemberID] = byPeriod
		}
		byPeriod[row.DuesPeriod] = money.FromDB(row.PaidAmount)
	}

	rows := make([]DuesGridRow, 0, len(members))
	first := duesGridMonths // index of the first month in which anyone owes
	for _, m := range members {
		cells := make([]DuesGridCell, len(periods))
		owes := false
		for i, period := range periods {
			cells[i] = duesGridCell(m, prices, paid[m.ID][period], period)
			if cells[i] != DuesCellNone {
				owes = true
				first = min(first, i)
			}
		}
		if owes {
			rows = append(rows, DuesGridRow{MemberID: m.ID, Name: m.Name, Cells: cells})
		}
	}

	// Drop the leading months nobody owes in. With no row at all first is still
	// len(periods), which empties both lists.
	for i := range rows {
		rows[i].Cells = rows[i].Cells[first:]
	}
	return DuesGrid{Periods: periods[first:], Rows: rows}, nil
}

// duesGridCell is DuesStatusForPeriod's per-member decision for one month,
// reduced to a grid cell: the same checks in the same order, over rows already
// in memory. paid is what the member paid toward the month (zero if nothing).
func duesGridCell(m store.Member, prices pricing, paid money.Amount, period string) DuesGridCell {
	if !memberOwesPeriod(m, period) {
		return DuesCellNone
	}
	_, rate, ok := prices.priceAt(m.ID, period)
	if !ok {
		return DuesCellNone
	}
	switch classifyDuesStatus(money.FromDB(rate), paid) {
	case DuesStatusPartial:
		return DuesCellPartial
	case DuesStatusUnpaid:
		return DuesCellUnpaid
	default: // Paid; PaidInAdvance is DuesStatusForPeriod's upgrade, not classify's
		return DuesCellPaid
	}
}
