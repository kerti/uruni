package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

const (
	reportMonthLayout = "2006-01"
	reportDayLayout   = "2006-01-02"

	// ReportDirectionIn is one of the two values the direction filter takes;
	// "" means no direction filter.
	ReportDirectionIn = DirectionIn
	// ReportDirectionOut is the other direction filter value.
	ReportDirectionOut = DirectionOut

	// ReportDuesUnpaid is the dues filter's value for DuesStatusUnpaid.
	ReportDuesUnpaid = "unpaid"
	// ReportDuesPartial is the dues filter's value for DuesStatusPartial.
	ReportDuesPartial = "partial"
	// ReportDuesPaid also matches DuesStatusPaidInAdvance: paying ahead is paid.
	ReportDuesPaid = "paid"
	// ReportDuesWaived is the dues filter's value for DuesStatusWaived (ADR-050).
	// Paid never includes a waived month.
	ReportDuesWaived = "waived"
)

// ReportParams is what MonthlyReport needs. Every filter is optional and the
// zero value means "no filter".
type ReportParams struct {
	FundID int64

	// Month is "YYYY-MM". Empty means the current month in the fund's time zone. A
	// malformed month is ErrInvalidArgument; reading an invalid ?month= as the
	// current one is the HTTP edge's call (ADR-035).
	Month string

	// From and To are "YYYY-MM-DD" days, whole and inclusive, in the fund's time
	// zone: a date range in place of a month (ADR-049). Both set means a range
	// and Month is ignored; both empty means the month; exactly one, or a
	// malformed or reversed pair, is ErrInvalidArgument. Clamping them to the
	// fund's first day and to today, and falling back to the month when they do
	// not parse, is the edge's call. A To of today or later makes a running
	// period, read like the running month: unbounded, as Beranda reads it.
	From string
	To   string

	// PurposeID filters transactions to one purpose tag (the stored tag every
	// balance sums). A purpose move matches when it names the purpose on either
	// side.
	PurposeID *int64

	// AccountID narrows MonthWalk to one location (Riwayat's Lokasi filter,
	// #784); MonthlyReport, the public report's reader, refuses it, since a
	// location is the treasurer's working detail and the page prints none
	// (ADR-035). Under it the walk is that location's: the ends are its
	// balance, a Pindah lokasi into or out of it is on the Moved line, and
	// another location's rows are not in it. With PurposeID too, the money of
	// that pos in that location.
	AccountID *int64

	// MemberID filters transactions to one member: the dues or contribution
	// member, or the member a settled claim paid back. A purpose move names
	// no member, so a member filter hides moves.
	MemberID *int64

	// Direction is ReportDirectionIn, ReportDirectionOut or "". A purpose
	// move is neither, so a direction filter hides moves.
	Direction Direction

	// DuesStatus is ReportDuesUnpaid, ReportDuesPartial, ReportDuesPaid,
	// ReportDuesWaived or "". It filters the dues table only, never the transactions (ADR-035).
	DuesStatus string
}

// Report is one fund's public report for one month (ADR-035): the single
// struct the HTML page and the monthly PDF both render, so nothing is
// recomputed in a template. Every field is a fact, never a label; display
// text is built at render time, as Riwayat does.
//
// It deliberately omits a location's name or balance, a receipt's path or id,
// a transaction id and a dues arrears count (rule 6). A member's join and leave
// dates are never stated either, but the dues grid's no-obligation cells show
// the months they fall in (ADR-044), as paging the one-month tables already did.
type Report struct {
	FundName string

	// AsOf is the day ("YYYY-MM-DD") every figure is true as of (ADR-037): the
	// last day of a past month or a range, or today in the fund's time zone for
	// the running month or a range ending today (marked by Running).
	AsOf    string
	Running bool

	// Today is the current day ("YYYY-MM-DD") in the fund's time zone, whatever
	// month is shown: what the dues grid is read as of (ADR-044).
	Today string

	// Month is the month shown ("YYYY-MM"); Months is every month offered,
	// oldest first: the first transaction's month through the current one. For a
	// date range (IsRange) Month is the month that holds To: the month the dues
	// list and the twelve-month grid read, which are monthly by nature (ADR-049).
	Month  string
	Months []string

	// From and To are the first and last day shown ("YYYY-MM-DD"), inclusive:
	// the month's first day and AsOf for a month, the range itself for a range.
	// IsRange says which the report was asked for.
	From    string
	To      string
	IsRange bool

	// Balance is the fund's one pooled balance (FundBalance).
	Balance money.Amount

	// OwedToMembers is what the fund owed members for talangan as of AsOf.
	OwedToMembers money.Amount

	// Reconciliation is nil when no count has ever been taken. A fund nobody has
	// counted is not a fund that matches.
	Reconciliation *ReportReconciliation

	// PurposeBalances is Kas Utama first, then each open envelope (oldest
	// first), then each pass-through (by name). A closed envelope is gone:
	// closing rolled its leftover into Kas Utama.
	PurposeBalances []ReportPurposeBalance

	// Rows are the month's rows after filters, newest first. Rows dated the
	// same day are in no particular order.
	Rows []ReportRow

	// Walk is the month as a walk from the balance it started on to the
	// balance it ended on (ADR-038), over the rows after filters.
	Walk ReportWalk

	// Dues is every member's status for Month, after the dues filter.
	Dues []ReportDuesRow

	// DuesGrid is the twelve months of dues status ending at Month (ADR-044),
	// as of today. It ignores every filter, the dues filter included: no filter
	// selects members by their grid. Empty (no periods) when nobody owes in any
	// of the twelve; the renderers draw it only from two periods up.
	DuesGrid DuesGrid

	// Envelopes are every envelope open at some point during Month.
	Envelopes []ReportEnvelope
}

// ReportReconciliation is the latest cek kas as the home banner reads it.
type ReportReconciliation struct {
	// Date is the count's day in the fund's time zone ("YYYY-MM-DD").
	Date string

	// Difference is the sum, by magnitude, of every remainder still open
	// (#498: remainder_amount, not the difference found) across the fund, as
	// ReconciliationBanner shows it, so a short location and an over one cannot
	// cancel into "cocok". Matched is Difference == 0.
	Matched    bool
	Difference money.Amount

	// NewEntries is whether anything was recorded after the count (as of the
	// bound, for a past month): "cocok" is a claim about the count, and this
	// says it may no longer be about now (#734).
	NewEntries bool
}

// ReportPurposeBalance is one purpose's running total.
type ReportPurposeBalance struct {
	PurposeID int64
	Name      string
	Kind      PurposeKind
	Balance   money.Amount
}

// ReportRow is one line of the month. Exactly one of Entry and Move is set.
type ReportRow struct {
	Date  string // "YYYY-MM-DD"
	Entry *ReportEntry
	Move  *ReportMove
}

// ReportLine is the walk line (ADR-038) a row lands on. It drives the list,
// the direction filter and the totals, so the three cannot disagree.
type ReportLine string

const (
	// ReportLineOpening is an opening entry: Saldo awal.
	ReportLineOpening ReportLine = "opening"
	// ReportLineIn is income, and the reversal of income (a reversal going out),
	// which counts there as a minus.
	ReportLineIn ReportLine = "in"
	// ReportLineOut is spending, and the reversal of spending (a reversal going
	// in), which counts there as a minus (ADR-042).
	ReportLineOut ReportLine = "out"
	// ReportLineAdjustment is every adjustment that is not a reversal.
	ReportLineAdjustment ReportLine = "adjustment"
)

// ReportEntry is an ordinary ledger row, with the facts a label needs.
type ReportEntry struct {
	// Line is the walk line the row lands on; see classifyReportRow.
	Line ReportLine

	Kind        TransactionKind
	Direction   Direction
	Amount      money.Amount
	PurposeName string
	PurposeID   int64

	// MemberName is the dues member, the named contributor, or the member paid
	// back on a settled claim's payout; nil when the row names none.
	MemberName *string

	// DuesPeriod is "YYYY-MM" on a dues row and a dues reversal; nil otherwise,
	// a contribution's reversal included.
	DuesPeriod *string

	// IsReversal marks a reversal (kind adjustment): of a dues payment when
	// DuesPeriod is set, of a named contribution when it names a member and has
	// no period, of an unnamed ordinary row when it names neither (ADR-042).
	IsReversal bool

	// Reversed is ledger_entry's flag: a reversal points at this row, so the
	// report marks it Sudah dibatalkan (ADR-042).
	Reversed bool

	// ReversesOccurredOn is, on a reversal, the date of the row it reverses
	// ("YYYY-MM-DD"); nil on every other row.
	ReversesOccurredOn *string

	// Note is what the treasurer typed. On a settled claim's payout, whose note
	// is empty, it is the claim's note, as Riwayat shows it.
	Note *string

	// HasReceipt is whether a photo exists, never the photo.
	HasReceipt bool

	// DuesPeriods is every month a dues row stands for, oldest first (ADR-048):
	// the listed row of a dues payment, or of a dues-reversal group, is ONE
	// entry for all its months, with Amount their sum. One month for a
	// single-month payment, nil on every other row. The walk counts the group's
	// rows one by one, whatever the list folds.
	DuesPeriods []string

	// PartlyReversed marks a dues payment some of whose months, and not all, are
	// reversed (data from before ADR-048 only): Sebagian dibatalkan. Reversed is
	// then false, and true only when every month is reversed.
	PartlyReversed bool
}

// ReportMove is a reclass_purpose pair folded into one row: an envelope's
// leftover rolled into Kas Utama, a purpose correction, or money moved
// between purposes (ADR-036). It moves nothing in or out of the fund, so it
// is outside the totals.
type ReportMove struct {
	Amount          money.Amount
	FromPurposeName string
	ToPurposeName   string
	FromPurposeID   int64
	ToPurposeID     int64

	// IsCorrection tells a purpose correction from an envelope's roll: only a
	// correction names the row it corrects (ADR-033).
	IsCorrection bool

	// IsAllocation tells money moved between purposes ("Pindah pos",
	// transfer.reason 'allocation', ADR-036) from an envelope's roll. Neither
	// flag set means a roll, which is also how a pair from before the column
	// reads.
	IsAllocation bool
	Note         *string
}

// ReportWalk is the month as ADR-038 reads it: a walk from the balance the
// month started on to the balance it ended on, in which every posted row
// lands on exactly one line.
//
//	Start + Openings + In - Out + Adjustments + Moved = End
//
// In is net of the reversals of income and Out of the reversals of spending
// (ADR-042), so either can read below zero, but only after a cek kas counted
// the original and its reversal landed in a later month; Adjustments and Moved
// are signed. Start and End are ledger sums (the bounded fund or purpose
// balance), never computed from the lines, so the equation is a property the
// tests assert, not something the report forces or checks at runtime (a
// public page does not fail on a neighbour).
//
// Full says whether the walk applies: with no filter (the whole fund), a
// purpose filter alone (that pos, whose Moved line is the net of its
// reclass_purpose legs), a location filter (that location, whose Moved line is
// the net of its between_accounts legs: a Pindah lokasi in is plus, out minus)
// or both (that pos's money in that location). Under a member or direction filter only In and Out
// are meaningful; Start and End stay zero, since a balance has no meaning for
// one member or direction.
type ReportWalk struct {
	Full bool

	// StartOn and EndOn date the two balances ("YYYY-MM-DD"): the last day of the
	// previous month, and the month's last day (today, for the running month).
	// Set with Full.
	StartOn string
	EndOn   string

	Start       money.Amount
	Openings    money.Amount
	In          money.Amount
	Out         money.Amount
	Adjustments money.Amount

	// Moved is a purpose or location filter's line: the signed net of the
	// transfer legs dated in the month that touch what is walked, summed from
	// the raw legs rather than the folded display rows, so it equals the change
	// in that balance whatever the display folds. For a purpose these are its
	// reclass_purpose legs; for a location, its between_accounts legs (a
	// reclass pair on a location has both legs there and nets to zero). Always
	// zero for the whole fund, where the two legs of a move cancel.
	Moved money.Amount
	End   money.Amount
}

// ReportDuesRow is one member's standing for the month: DuesStatusForPeriod's
// row with the member's tier by name, without their dates. The tier is the one
// in force in the month shown, the one that priced Owed (ADR-040), not today's.
// Names and tiers are public by the maintainer's call (2026-10-02); the slug is
// the guard.
type ReportDuesRow struct {
	MemberName  string
	TierName    string // the tier in force in the month; "" when it names none
	Owed        money.Amount
	Paid        money.Amount
	Status      DuesStatus
	PaidThrough string // set only on DuesStatusPaidInAdvance, see MemberDuesStatus
}

// ReportEnvelope is one envelope with its participation table (ADR-034).
type ReportEnvelope struct {
	PurposeID        int64
	Name             string
	TargetAmount     *money.Amount
	MinimumPerMember *money.Amount
	OpenedOn         string
	ClosedOn         *string // nil while open
	Balance          money.Amount

	// Collected is what the occasion took in, as the envelope screen's
	// "Terkumpul" reads it (GetIncidentalDetail), not Balance, which is zero on a
	// closed envelope once its leftover has rolled out.
	Collected money.Amount

	// Recipients are the members the envelope is for, by name; never expected
	// to give (ADR-034).
	Recipients []string
	Expected   []ReportParticipant
	Unexpected []ReportContributor
}

// ReportParticipant is one expected member's standing against the envelope.
type ReportParticipant struct {
	MemberName string
	Amount     money.Amount
	State      ParticipationState
}

// ReportContributor is someone who gave without being expected - never shown
// as "not yet" (ADR-034).
type ReportContributor struct {
	MemberName string
	Amount     money.Amount
}

// MonthlyReport assembles everything the public report shows for one fund
// and one month (ADR-035). It runs inside one transaction: it is a dozen
// queries, and a write landing between two could show a header balance one
// row newer than the rows beneath it, on the page whose job is to show that
// the numbers agree. Every helper reads through the snapshot's q.
//
// Which ledger rows appear, and where they count:
//   - A between_accounts transfer is left out: with locations hidden it
//     moves nothing a reader can see, and its two legs would double a total.
//   - A reclass_purpose pair is one ReportMove, outside the walk. It shows
//     when the purpose filter equals either side, and is hidden by a member
//     or direction filter.
//   - Everything else is a ReportEntry on exactly one line of the walk
//     (ADR-038, classifyReportRow): Saldo awal, Total masuk, Total keluar or
//     Penyesuaian.
//
// Filtering happens in Go over the month's rows, not in SQL, because a pair
// must be folded before a purpose filter can ask whether either side matches.
func (l *Ledger) MonthlyReport(ctx context.Context, p ReportParams) (Report, error) {
	var r Report
	err := l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		r, err = l.onQuerier(q).monthlyReport(ctx, p)
		return err
	})
	return r, err
}

func (l *Ledger) monthlyReport(ctx context.Context, p ReportParams) (Report, error) {
	if p.Direction != "" && p.Direction != ReportDirectionIn && p.Direction != ReportDirectionOut {
		return Report{}, fmt.Errorf("%w: direction %q is not \"in\" or \"out\"", ErrInvalidArgument, p.Direction)
	}
	switch p.DuesStatus {
	case "", ReportDuesUnpaid, ReportDuesPartial, ReportDuesPaid, ReportDuesWaived:
	default:
		return Report{}, fmt.Errorf("%w: dues status %q is not unpaid, partial, paid or waived", ErrInvalidArgument, p.DuesStatus)
	}

	if p.AccountID != nil {
		return Report{}, fmt.Errorf("%w: the public report has no location filter", ErrInvalidArgument)
	}

	// The fund is read first: its zone decides "today" and "this month" (ADR-041).
	fund, err := l.q.GetFund(ctx, p.FundID)
	if err != nil {
		return Report{}, fmt.Errorf("fetching fund: %w", err)
	}
	loc, err := FundLocation(fund)
	if err != nil {
		return Report{}, err
	}

	now := l.now()
	today := now.In(loc)
	todayDay := today.Format(reportDayLayout)

	period, err := reportPeriodFor(p, today)
	if err != nil {
		return Report{}, err
	}
	month := period.start.Format(reportMonthLayout)
	if period.isRange {
		// The dues list and the grid are monthly: they read the month that holds To.
		month = period.end.Format(reportMonthLayout)
	}

	r := Report{
		FundName: fund.Name,
		AsOf:     todayDay,
		Running:  period.running,
		Today:    todayDay,
		Month:    month,
		From:     period.startDay(),
		IsRange:  period.isRange,
	}
	b := period.bound(loc)
	if !period.running {
		r.AsOf = *b.through
	}
	r.To = r.AsOf

	if r.Months, err = l.reportMonths(ctx, p.FundID, today); err != nil {
		return Report{}, err
	}
	balance, err := l.q.ReportFundBalance(ctx, store.ReportFundBalanceParams{FundID: p.FundID, Through: b.through})
	if err != nil {
		return Report{}, fmt.Errorf("report fund balance: %w", err)
	}
	r.Balance = money.FromDB(balance)
	owed, err := l.q.ReportOwedToMembers(ctx, store.ReportOwedToMembersParams{FundID: p.FundID, Through: b.through})
	if err != nil {
		return Report{}, fmt.Errorf("report owed to members: %w", err)
	}
	r.OwedToMembers = money.FromDB(owed)
	if r.Reconciliation, err = l.reportReconciliation(ctx, p.FundID, b, loc); err != nil {
		return Report{}, err
	}

	envelopes, err := l.q.ListIncidentalsByFund(ctx, p.FundID)
	if err != nil {
		return Report{}, fmt.Errorf("listing incidentals: %w", err)
	}
	purposes, err := l.q.ListPurposesByFund(ctx, p.FundID)
	if err != nil {
		return Report{}, fmt.Errorf("listing purposes: %w", err)
	}
	if r.PurposeBalances, err = l.reportPurposeBalances(ctx, p.FundID, purposes, envelopes, b); err != nil {
		return Report{}, err
	}

	if r.Rows, r.Walk, err = l.reportRows(ctx, p, period); err != nil {
		return Report{}, err
	}
	if err := l.walkEnds(ctx, p, period.start, b, r.AsOf, &r.Walk); err != nil {
		return Report{}, err
	}

	if r.Dues, err = l.reportDues(ctx, p.FundID, month, p.DuesStatus); err != nil {
		return Report{}, err
	}
	// The running month passes its own month; DuesGrid caps at the current one.
	if r.DuesGrid, err = l.duesGrid(ctx, p.FundID, month, now); err != nil {
		return Report{}, err
	}
	if r.Envelopes, err = l.reportEnvelopes(ctx, p.FundID, period, purposes, envelopes, b); err != nil {
		return Report{}, err
	}

	return r, nil
}

// walkEnds sets the walk's two balances and their dates; periodStart is the
// period's first day, so the start balance is the one at the end of the day
// before it. The ends are ledger sums, and only the whole fund, one pos or one
// location (with or without a pos) has them (Full). A purpose or location that did not exist yet sums to
// nothing.
func (l *Ledger) walkEnds(ctx context.Context, p ReportParams, periodStart time.Time, b reportBound, asOf string, walk *ReportWalk) error {
	if !walk.Full {
		return nil
	}
	dayBefore := periodStart.AddDate(0, 0, -1).Format(reportDayLayout)
	walk.StartOn, walk.EndOn = dayBefore, asOf
	if p.AccountID != nil {
		var err error
		if walk.Start, err = l.reportAccountBalance(ctx, p, reportBound{through: &dayBefore}); err != nil {
			return err
		}
		if walk.End, err = l.reportAccountBalance(ctx, p, b); err != nil {
			return err
		}
		return nil
	}
	if p.PurposeID == nil {
		start, err := l.q.ReportFundBalance(ctx, store.ReportFundBalanceParams{FundID: p.FundID, Through: &dayBefore})
		if err != nil {
			return fmt.Errorf("report start balance: %w", err)
		}
		end, err := l.q.ReportFundBalance(ctx, store.ReportFundBalanceParams{FundID: p.FundID, Through: b.through})
		if err != nil {
			return fmt.Errorf("report end balance: %w", err)
		}
		walk.Start, walk.End = money.FromDB(start), money.FromDB(end)
		return nil
	}
	var err error
	if walk.Start, err = l.reportPurposeBalance(ctx, p.FundID, *p.PurposeID, reportBound{through: &dayBefore}); err != nil {
		return err
	}
	if walk.End, err = l.reportPurposeBalance(ctx, p.FundID, *p.PurposeID, b); err != nil {
		return err
	}
	return nil
}

// MonthWalk is the report's walk alone (ADR-038) for one month under the
// report's filters: Riwayat's month summary (#751) reads the same figures the
// public report prints, from the same derivation, so the two can never
// disagree. Month is required here; an empty or malformed one is
// ErrInvalidArgument.
func (l *Ledger) MonthWalk(ctx context.Context, p ReportParams) (ReportWalk, error) {
	monthStart, err := parseReportMonth(p.Month)
	if err != nil {
		return ReportWalk{}, err
	}
	fund, err := l.q.GetFund(ctx, p.FundID)
	if err != nil {
		return ReportWalk{}, fmt.Errorf("fetching fund: %w", err)
	}
	loc, err := FundLocation(fund)
	if err != nil {
		return ReportWalk{}, err
	}
	today := l.now().In(loc)
	period := monthPeriod(monthStart, today)
	asOf := today.Format(reportDayLayout)
	b := period.bound(loc)
	if !period.running {
		asOf = *b.through
	}

	_, walk, err := l.reportRows(ctx, p, period)
	if err != nil {
		return ReportWalk{}, err
	}
	if err := l.walkEnds(ctx, p, period.start, b, asOf, &walk); err != nil {
		return ReportWalk{}, err
	}
	return walk, nil
}

// reportPeriod is the days one report covers, whole and inclusive in the fund's
// zone: a calendar month, or the date range ADR-049 adds. The month and the
// range share every step of the assembly, so a figure cannot be right for one
// and wrong for the other. start and end are dates at midnight UTC, used only
// for their calendar day.
type reportPeriod struct {
	start, end time.Time
	isRange    bool

	// running is whether the period reaches today: it then reads unbounded,
	// exactly as Beranda does (ADR-037).
	running bool
}

func (rp reportPeriod) startDay() string { return rp.start.Format(reportDayLayout) }
func (rp reportPeriod) endDay() string   { return rp.end.Format(reportDayLayout) }

// monthPeriod is the calendar month starting on monthStart, as seen on today (a
// time in the fund's zone).
func monthPeriod(monthStart, today time.Time) reportPeriod {
	return reportPeriod{
		start:   monthStart,
		end:     monthStart.AddDate(0, 1, -1),
		running: monthStart.Format(reportMonthLayout) >= today.Format(reportMonthLayout),
	}
}

// reportPeriodFor reads p's period: the range when From and To are set (Month
// is then ignored), the month otherwise (empty means the current one).
func reportPeriodFor(p ReportParams, today time.Time) (reportPeriod, error) {
	if p.From == "" && p.To == "" {
		month := p.Month
		if month == "" {
			month = today.Format(reportMonthLayout)
		}
		monthStart, err := parseReportMonth(month)
		if err != nil {
			return reportPeriod{}, err
		}
		return monthPeriod(monthStart, today), nil
	}
	if p.From == "" || p.To == "" {
		return reportPeriod{}, fmt.Errorf("%w: a date range needs both from and to", ErrInvalidArgument)
	}
	start, err := parseReportDay(p.From)
	if err != nil {
		return reportPeriod{}, err
	}
	end, err := parseReportDay(p.To)
	if err != nil {
		return reportPeriod{}, err
	}
	if end.Before(start) {
		return reportPeriod{}, fmt.Errorf("%w: range ends (%s) before it starts (%s)", ErrInvalidArgument, p.To, p.From)
	}
	if p.From > today.Format(reportDayLayout) {
		return reportPeriod{}, fmt.Errorf("%w: range starts (%s) after today", ErrInvalidArgument, p.From)
	}
	// A range reaching today is running, and never reads beyond it: a To ahead of
	// today means today (the edge clamps too; the ledger does not rely on it).
	running := p.To >= today.Format(reportDayLayout)
	if running {
		if end, err = parseReportDay(today.Format(reportDayLayout)); err != nil {
			return reportPeriod{}, err
		}
	}
	return reportPeriod{start: start, end: end, isRange: true, running: running}, nil
}

// bound is where the period's figures stop (ADR-037): the zero bound, reading
// everything, when it is running.
func (rp reportPeriod) bound(loc *time.Location) reportBound {
	if rp.running {
		return reportBound{}
	}
	return boundAtDayEnd(rp.end, loc)
}

// reportBound is where a past period's figures stop (ADR-037): through is its
// last day, compared against the ledger's day columns; before is the first
// instant of the day after it in the fund's time zone, compared against a
// count's performed_at. Both nil is the running period, which reads every row,
// the same figures Beranda shows.
type reportBound struct {
	through *string
	before  *int64
}

// boundAtDayEnd bounds a past period at the end of its last day.
func boundAtDayEnd(lastDay time.Time, loc *time.Location) reportBound {
	last := lastDay.Format(reportDayLayout)
	next := time.Date(lastDay.Year(), lastDay.Month(), lastDay.Day()+1, 0, 0, 0, 0, loc).Unix()
	return reportBound{through: &last, before: &next}
}

// parseReportMonth returns the first day of a "YYYY-MM" month, refusing
// anything time.Parse would normalise into a different string.
func parseReportMonth(month string) (time.Time, error) {
	t, err := time.Parse(reportMonthLayout, month)
	if err != nil || t.Format(reportMonthLayout) != month {
		return time.Time{}, fmt.Errorf("%w: month %q is not a valid \"YYYY-MM\" month", ErrInvalidArgument, month)
	}
	return t, nil
}

// parseReportDay returns the "YYYY-MM-DD" day at midnight UTC, refusing
// anything time.Parse would normalise into a different string.
func parseReportDay(day string) (time.Time, error) {
	t, err := time.Parse(reportDayLayout, day)
	if err != nil || t.Format(reportDayLayout) != day {
		return time.Time{}, fmt.Errorf("%w: day %q is not a valid \"YYYY-MM-DD\" day", ErrInvalidArgument, day)
	}
	return t, nil
}

// ReportMonths is the months the public report offers, oldest first - the
// same list MonthlyReport carries as Months, read in the fund's zone - for the
// app's PDF month picker, which offers exactly what the report does.
func (l *Ledger) ReportMonths(ctx context.Context, fundID int64) ([]string, error) {
	fund, err := l.q.GetFund(ctx, fundID)
	if err != nil {
		return nil, fmt.Errorf("fetching fund: %w", err)
	}
	loc, err := FundLocation(fund)
	if err != nil {
		return nil, err
	}
	return l.reportMonths(ctx, fundID, l.now().In(loc))
}

// ReportDays is the span a date range may cover, as "YYYY-MM-DD" days in the
// fund's zone: the fund's first transaction day and today. A fund with no
// transaction, or whose first one is dated after today, starts today. The
// report's edge clamps a requested range into it (ADR-049).
func (l *Ledger) ReportDays(ctx context.Context, fundID int64) (first, today string, err error) {
	fund, err := l.q.GetFund(ctx, fundID)
	if err != nil {
		return "", "", fmt.Errorf("fetching fund: %w", err)
	}
	loc, err := FundLocation(fund)
	if err != nil {
		return "", "", err
	}
	today = l.now().In(loc).Format(reportDayLayout)
	first = today
	firstDate, err := l.q.FirstTransactionDateByFund(ctx, fundID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return "", "", fmt.Errorf("first transaction date: %w", err)
	case firstDate < first:
		first = firstDate
	}
	return first, today, nil
}

// reportMonths lists the months the report offers, oldest first: the first
// transaction's month through the current month in the fund's zone. An empty fund, or one
// whose first row is dated after today, offers the current month alone.
func (l *Ledger) reportMonths(ctx context.Context, fundID int64, today time.Time) ([]string, error) {
	current := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)

	first := current
	firstDate, err := l.q.FirstTransactionDateByFund(ctx, fundID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// An empty fund: nothing before the current month.
	case err != nil:
		return nil, fmt.Errorf("first transaction date: %w", err)
	default:
		// occurred_on is a CHECKed "YYYY-MM-DD"; its first seven characters
		// are the month.
		t, err := time.Parse(reportMonthLayout, firstDate[:len(reportMonthLayout)])
		if err != nil {
			return nil, fmt.Errorf("parsing first transaction date %q: %w", firstDate, err)
		}
		if t.Before(first) {
			first = t
		}
	}

	var months []string
	for m := first; !m.After(current); m = m.AddDate(0, 1, 0) {
		months = append(months, m.Format(reportMonthLayout))
	}
	return months, nil
}

// reportReconciliation reads the banner's three states as of the bound: nil
// for never counted by then, otherwise the latest count's date and the
// fund-wide remainder still open at the bound.
func (l *Ledger) reportReconciliation(ctx context.Context, fundID int64, b reportBound, loc *time.Location) (*ReportReconciliation, error) {
	latest, err := l.q.ReportLatestReconciliation(ctx, store.ReportLatestReconciliationParams{FundID: fundID, Before: b.before})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest reconciliation: %w", err)
	}

	open, err := l.q.ReportOpenReconciliationLines(ctx, store.ReportOpenReconciliationLinesParams{FundID: fundID, Before: b.before})
	if err != nil {
		return nil, fmt.Errorf("listing open reconciliation lines: %w", err)
	}
	var difference money.Amount
	for _, line := range open {
		d := money.FromDB(line.RemainderAmount)
		if d < 0 {
			d = -d
		}
		if difference, err = difference.Add(d); err != nil {
			return nil, fmt.Errorf("summing open differences: %w", err)
		}
	}

	newEntries, err := l.q.FundHasTransactionsAfter(ctx, store.FundHasTransactionsAfterParams{
		FundID: fundID, AfterID: latest.ThroughTransactionID, Through: b.through, ReconciliationID: latest.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("checking for entries after the count: %w", err)
	}

	return &ReportReconciliation{
		Date:       time.Unix(latest.PerformedAt, 0).In(loc).Format(reportDayLayout),
		Matched:    difference == 0,
		Difference: difference,
		NewEntries: newEntries,
	}, nil
}

// reportPurposeBalances is the header's per-purpose figures as of the bound:
// Kas Utama, each envelope open then, each pass-through that existed then,
// and any other purpose still holding money then, so the lines always sum to
// Saldo kas (a purpose created or opened after the month with rows dated
// inside it, which back-filling produces, #408). A closed envelope holds
// nothing (ADR-031), so it stays hidden. In ListPurposesByFund's order.
func (l *Ledger) reportPurposeBalances(ctx context.Context, fundID int64, purposes []store.Purpose, envelopes []store.Incidental, b reportBound) ([]ReportPurposeBalance, error) {
	envelopeByPurpose := make(map[int64]store.Incidental, len(envelopes))
	for _, e := range envelopes {
		envelopeByPurpose[e.PurposeID] = e
	}

	type ranked struct {
		purpose store.Purpose
		balance money.Amount
	}
	var shown []ranked
	for _, pu := range purposes {
		e, isEnvelope := envelopeByPurpose[pu.ID]
		existed := true
		switch {
		case isEnvelope:
			existed = envelopeOpenAt(e, b.through)
		case pu.Kind == string(PurposeKindPassThrough) && b.before != nil:
			// A pass-through has no opening date; it existed once created.
			existed = pu.CreatedAt < *b.before
		}
		bal, err := l.reportPurposeBalance(ctx, fundID, pu.ID, b)
		if err != nil {
			return nil, err
		}
		if !existed && bal == 0 {
			continue
		}
		shown = append(shown, ranked{purpose: pu, balance: bal})
	}

	out := make([]ReportPurposeBalance, 0, len(shown))
	for _, s := range shown {
		out = append(out, ReportPurposeBalance{PurposeID: s.purpose.ID, Name: s.purpose.Name, Kind: PurposeKind(s.purpose.Kind), Balance: s.balance})
	}
	return out, nil
}

func (l *Ledger) reportPurposeBalance(ctx context.Context, fundID, purposeID int64, b reportBound) (money.Amount, error) {
	v, err := l.q.ReportPurposeBalance(ctx, store.ReportPurposeBalanceParams{FundID: fundID, PurposeID: purposeID, Through: b.through})
	if err != nil {
		return 0, fmt.Errorf("report purpose balance: %w", err)
	}
	return money.FromDB(v), nil
}

// reportAccountBalance is p's one location's balance at b, narrowed to p's pos
// when it has one: the walk's ends under the Lokasi filter, summed from the
// ledger like every other balance.
func (l *Ledger) reportAccountBalance(ctx context.Context, p ReportParams, b reportBound) (money.Amount, error) {
	v, err := l.q.ReportAccountBalance(ctx, store.ReportAccountBalanceParams{
		FundID: p.FundID, AccountID: *p.AccountID, PurposeID: p.PurposeID, Through: b.through,
	})
	if err != nil {
		return 0, fmt.Errorf("report location balance: %w", err)
	}
	return money.FromDB(v), nil
}

// envelopeOpenAt is whether an envelope was open at the end of the day
// through, nil meaning now. Closing rolls the leftover out on closed_on, so an
// envelope closed on that very day is already gone from it.
func envelopeOpenAt(e store.Incidental, through *string) bool {
	if through == nil {
		return e.ClosedOn == nil
	}
	return e.OpenedOn <= *through && (e.ClosedOn == nil || *e.ClosedOn > *through)
}

// MonthInOut is the running month's In and Out for the whole fund, as Beranda
// shows them (ADR-038): the figures the report's running month carries as
// Walk.In and Walk.Out, read through the same reportRows over the same
// unbounded, unfiltered month. The month is read from the ledger clock in the fund's time zone. In
// is net of the reversals of income and Out of the reversals of spending, so
// either can be negative (ADR-042); Out is otherwise a magnitude.
func (l *Ledger) MonthInOut(ctx context.Context, fundID int64) (in, out money.Amount, err error) {
	err = l.withTx(ctx, func(ctx context.Context, q store.Querier) error {
		var err error
		in, out, err = l.onQuerier(q).monthInOut(ctx, fundID, l.now())
		return err
	})
	return in, out, err
}

// monthInOut is MonthInOut's read over l.q, whatever that is: a transaction the
// caller already opened (Balances reads it beside the rest of the home answer),
// or the pool.
func (l *Ledger) monthInOut(ctx context.Context, fundID int64, now time.Time) (in, out money.Amount, err error) {
	fund, err := l.q.GetFund(ctx, fundID)
	if err != nil {
		return 0, 0, fmt.Errorf("fetching fund: %w", err)
	}
	loc, err := FundLocation(fund)
	if err != nil {
		return 0, 0, err
	}
	monthStart, err := parseReportMonth(now.In(loc).Format(reportMonthLayout))
	if err != nil {
		return 0, 0, err
	}
	_, walk, err := l.reportRows(ctx, ReportParams{FundID: fundID}, monthPeriod(monthStart, now.In(loc)))
	if err != nil {
		return 0, 0, err
	}
	return walk.In, walk.Out, nil
}

// reportRows reads the period, folds transfer pairs, applies the filters, and
// lands each entry on its line of the walk. A running period reads with no
// upper bound, matching its unbounded balance (ADR-037, ADR-038): a row dated
// after today still belongs to the walk that ends on that balance.
func (l *Ledger) reportRows(ctx context.Context, p ReportParams, period reportPeriod) ([]ReportRow, ReportWalk, error) {
	var to *string
	if !period.running {
		// ToDate is exclusive: the day after the period's last.
		next := period.end.AddDate(0, 0, 1).Format(reportDayLayout)
		to = &next
	}
	rows, err := l.q.ListReportTransactions(ctx, store.ListReportTransactionsParams{
		FundID:   p.FundID,
		FromDate: period.startDay(),
		ToDate:   to,
	})
	if err != nil {
		return nil, ReportWalk{}, fmt.Errorf("listing report transactions: %w", err)
	}

	corrections, err := foldCorrections(rows)
	if err != nil {
		return nil, ReportWalk{}, err
	}

	var (
		out           []ReportRow
		walk          = ReportWalk{Full: p.MemberID == nil && p.Direction == ""}
		moveSeen      = make(map[int64]bool)
		correctedSeen = make(map[int64]bool)
		// groups folds a dues payment's rows, and a reversal group's, into the one
		// listed row they are (ADR-048); see duesGroupKey.
		groups = make(map[string]*duesGroup)
	)
	for _, row := range rows {
		if row.Kind == string(TransactionKindTransfer) {
			if row.TransferKind == nil || row.TransferID == nil {
				return nil, ReportWalk{}, fmt.Errorf("transfer row dated %s carries no transfer", row.OccurredOn)
			}
			switch *row.TransferKind {
			case string(TransferKindBetweenAccounts):
				// With locations hidden the pair is invisible, but under a Lokasi
				// filter a leg on that location changes its balance, so it is a
				// Pindah lokasi on the Moved line: into the location plus, out minus.
				// A pair between two other locations has no leg here.
				if walk.Full && p.AccountID != nil && legMoves(p, row) {
					if err := walk.addMoved(row); err != nil {
						return nil, ReportWalk{}, err
					}
				}
				continue
			case string(TransferKindReclassPurpose):
				// Dipindah is the raw leg, whatever the display folds: a pair of
				// corrections that folds to nothing still posted legs. Under a
				// Lokasi filter alone both legs sit on the location and net to zero,
				// so Moved stays 0 and the walk foots; with a pos too, the legs
				// touching that pos count as they do without a location.
				if walk.Full && (p.PurposeID != nil || p.AccountID != nil) && legMoves(p, row) {
					if err := walk.addMoved(row); err != nil {
						return nil, ReportWalk{}, err
					}
				}

				// Both legs describe the same pair; fold on the first.
				if moveSeen[*row.TransferID] {
					continue
				}
				moveSeen[*row.TransferID] = true

				// Every correction of one row reads as a single net move (#280): emitted
				// once, at the first leg met, and not at all when the corrections cancel.
				if c := row.TransferCorrectsTransactionID; c != nil {
					if correctedSeen[*c] {
						continue
					}
					correctedSeen[*c] = true
					net, ok := corrections[*c]
					if !ok || !moveMatches(p, net) {
						continue
					}
					out = append(out, ReportRow{Date: row.OccurredOn, Move: &net})
					continue
				}

				move, err := reportMoveFrom(row)
				if err != nil {
					return nil, ReportWalk{}, err
				}
				if !moveMatches(p, move) {
					continue
				}
				out = append(out, ReportRow{Date: row.OccurredOn, Move: &move})
				continue
			default:
				return nil, ReportWalk{}, fmt.Errorf("unknown transfer kind %q", *row.TransferKind)
			}
		}

		entry, err := reportEntryFrom(row)
		if err != nil {
			return nil, ReportWalk{}, err
		}
		if !entryMatches(p, row, entry) {
			continue
		}

		// The walk counts every row on its own; only the list folds a group.
		if err := walk.add(entry); err != nil {
			return nil, ReportWalk{}, fmt.Errorf("totalling report rows: %w", err)
		}

		key := duesGroupKey(row)
		if g, ok := groups[key]; ok && key != "" {
			if err := g.add(entry, row.Reversed != 0); err != nil {
				return nil, ReportWalk{}, fmt.Errorf("folding dues payment rows: %w", err)
			}
			continue
		}
		if key != "" {
			g := &duesGroup{entry: &entry}
			if err := g.add(entry, row.Reversed != 0); err != nil {
				return nil, ReportWalk{}, fmt.Errorf("folding dues payment rows: %w", err)
			}
			groups[key] = g
		}
		out = append(out, ReportRow{Date: row.OccurredOn, Entry: &entry})
	}
	for _, g := range groups {
		g.finish()
	}
	return out, walk, nil
}

// duesGroupKey is the key a dues row folds under in the report's list (ADR-048),
// "" for every row that stands alone: "p<payment>" for a payment's month and
// "r<payment>:<date>" for a reversal of one, the reversed row's payment and the
// reversal's date, the same key ListTransactionsPage groups by.
func duesGroupKey(row store.ListReportTransactionsRow) string {
	switch {
	case row.Kind == string(TransactionKindDues) && row.DuesPaymentID != nil:
		return fmt.Sprintf("p%d", *row.DuesPaymentID)
	case row.ReversesDuesPaymentID != nil:
		return fmt.Sprintf("r%d:%s", *row.ReversesDuesPaymentID, row.OccurredOn)
	}
	return ""
}

// duesGroup accumulates one listed row for a payment's (or a reversal group's)
// rows: Amount is the sum, DuesPeriods the months, HasReceipt any photo, and
// Reversed / PartlyReversed what the months' own flags say once all are in.
type duesGroup struct {
	entry    *ReportEntry
	first    bool
	months   int
	reversed int
}

func (g *duesGroup) add(e ReportEntry, reversed bool) error {
	g.months++
	if reversed {
		g.reversed++
	}
	if e.DuesPeriod != nil {
		g.entry.DuesPeriods = append(g.entry.DuesPeriods, *e.DuesPeriod)
	}
	if !g.first {
		// The first row is the entry itself, already counted in its own amount.
		g.first = true
		return nil
	}
	sum, err := g.entry.Amount.Add(e.Amount)
	if err != nil {
		return err
	}
	g.entry.Amount = sum
	g.entry.HasReceipt = g.entry.HasReceipt || e.HasReceipt
	return nil
}

func (g *duesGroup) finish() {
	sort.Strings(g.entry.DuesPeriods)
	if len(g.entry.DuesPeriods) > 0 {
		g.entry.DuesPeriod = &g.entry.DuesPeriods[0]
	}
	if g.entry.IsReversal {
		return
	}
	g.entry.Reversed = g.reversed == g.months
	g.entry.PartlyReversed = g.reversed > 0 && g.reversed < g.months
}

// legMoves is whether one transfer leg touches the balance the filters walk: it
// is in the filtered location and/or on the filtered pos, whichever are set.
func legMoves(p ReportParams, row store.ListReportTransactionsRow) bool {
	if p.AccountID != nil && row.AccountID != *p.AccountID {
		return false
	}
	return p.PurposeID == nil || row.PurposeID == *p.PurposeID
}

// addMoved lands one raw transfer leg on the Moved line, signed as the ledger's
// balance reads it.
func (w *ReportWalk) addMoved(row store.ListReportTransactionsRow) error {
	leg := ReportEntry{Direction: Direction(row.Direction), Amount: money.FromDB(row.Amount)}
	signed, err := leg.signed()
	if err != nil {
		return err
	}
	if w.Moved, err = w.Moved.Add(signed); err != nil {
		return fmt.Errorf("totalling moved legs: %w", err)
	}
	return nil
}

// signed is the amount as the ledger's balance reads it: in is plus, out minus.
func (e ReportEntry) signed() (money.Amount, error) {
	if e.Direction == ReportDirectionIn {
		return e.Amount, nil
	}
	return money.Amount(0).Sub(e.Amount)
}

// add lands one entry on its line of the walk. A reversal lands on its
// original's line as a minus (ADR-042): income reversed subtracts from In,
// spending reversed from Out, a magnitude. An adjustment is signed by its
// direction.
func (w *ReportWalk) add(e ReportEntry) error {
	signed, err := e.signed()
	if err != nil {
		return err
	}
	switch e.Line {
	case ReportLineOpening:
		w.Openings, err = w.Openings.Add(signed)
	case ReportLineIn:
		w.In, err = w.In.Add(signed)
	case ReportLineOut:
		// Out is a magnitude: spending, shown positive and subtracted by the walk.
		// The reversal of spending takes its amount back off it.
		if e.IsReversal {
			w.Out, err = w.Out.Sub(e.Amount)
		} else {
			w.Out, err = w.Out.Add(e.Amount)
		}
	case ReportLineAdjustment:
		w.Adjustments, err = w.Adjustments.Add(signed)
	default:
		return fmt.Errorf("entry has no walk line %q", e.Line)
	}
	return err
}

// classifyReportRow is ADR-038's one switch: the line a posted row lands on.
// Total by construction: an unknown kind is an error, so a trust report says
// the ledger is broken rather than drop a row. Transfers never reach it.
func classifyReportRow(kind TransactionKind, direction Direction, reverses bool) (ReportLine, error) {
	switch kind {
	case TransactionKindOpening:
		return ReportLineOpening, nil
	case TransactionKindNormal, TransactionKindDues, TransactionKindReimbursement:
		if direction == ReportDirectionIn {
			return ReportLineIn, nil
		}
		return ReportLineOut, nil
	case TransactionKindAdjustment:
		if reverses {
			// The line follows the reversal's own direction, so no join to the
			// original is needed: out reverses income, in reverses spending.
			if direction == ReportDirectionIn {
				return ReportLineOut, nil
			}
			return ReportLineIn, nil
		}
		return ReportLineAdjustment, nil
	}
	return "", fmt.Errorf("transaction kind %q has no line in the walk", kind)
}

func reportEntryFrom(row store.ListReportTransactionsRow) (ReportEntry, error) {
	line, err := classifyReportRow(TransactionKind(row.Kind), Direction(row.Direction), row.ReversesTransactionID != nil)
	if err != nil {
		return ReportEntry{}, err
	}
	e := ReportEntry{
		Line:        line,
		Kind:        TransactionKind(row.Kind),
		Direction:   Direction(row.Direction),
		Amount:      money.FromDB(row.Amount),
		PurposeName: row.PurposeName,
		PurposeID:   row.PurposeID,
		MemberName:  row.MemberName,
		DuesPeriod:  row.DuesPeriod,
		IsReversal:  row.ReversesTransactionID != nil,
		Reversed:    row.Reversed != 0,
		Note:        row.Note,
		HasReceipt:  row.HasReceipt != 0,

		ReversesOccurredOn: row.ReversesOccurredOn,
	}
	// A settlement's row names no member and holds no note: both come from the
	// claim it settles. At most one of the two member columns is set.
	if e.MemberName == nil {
		e.MemberName = row.SettlementMemberName
	}
	if row.Kind == string(TransactionKindReimbursement) {
		e.Note = row.ClaimNote
	}
	return e, nil
}

// foldCorrections nets every purpose correction of the same row into one
// move, keyed by the corrected row's id (#280). A row corrected Perpisahan ->
// Seragam -> Kas Utama -> Seragam reads as Perpisahan -> Seragam;
// one corrected and then corrected back is absent. Display only: every leg
// stays posted, and the balances are sums of them all.
//
// Each correction moves the whole row from wherever it is now, so the
// corrections of one row form a path. Its ends are the purpose left once more
// than entered and the one entered once more than left, which needs no
// ordering (ids are not an order to rely on). Every correction carries the
// row's date, so they all fall in that row's month.
//
// A pair is named by its own legs (from is the 'out' leg, to the 'in' leg), and
// PostPurposeCorrection puts the target on the leg moving the way the row did,
// so for an 'out' row the pair's money runs from the target back to where the
// row began. The path is worked out in the pair's terms and the result is then
// stated as what happened to the row, From where it began To where it is now,
// the same for both directions (#654).
func foldCorrections(rows []store.ListReportTransactionsRow) (map[int64]ReportMove, error) {
	type path struct {
		move     ReportMove
		net      map[int64]int // purpose id -> times entered minus times left
		names    map[int64]string
		notes    map[string]bool
		outgoing bool // the corrected row is an 'out'
	}
	paths := make(map[int64]*path)
	seen := make(map[int64]bool)
	for _, row := range rows {
		c := row.TransferCorrectsTransactionID
		if c == nil || row.TransferID == nil || seen[*row.TransferID] {
			continue
		}
		seen[*row.TransferID] = true
		m, err := reportMoveFrom(row)
		if err != nil {
			return nil, err
		}
		pa, ok := paths[*c]
		if !ok {
			if row.TransferCorrectsDirection == nil {
				return nil, fmt.Errorf("correction of transaction %d carries no direction", *c)
			}
			pa = &path{
				move: m, net: map[int64]int{}, names: map[int64]string{}, notes: map[string]bool{},
				outgoing: *row.TransferCorrectsDirection == string(DirectionOut),
			}
			paths[*c] = pa
		}
		pa.net[m.FromPurposeID]--
		pa.net[m.ToPurposeID]++
		pa.names[m.FromPurposeID] = m.FromPurposeName
		pa.names[m.ToPurposeID] = m.ToPurposeName
		if m.Note != nil {
			pa.notes[*m.Note] = true
		}
	}

	out := make(map[int64]ReportMove, len(paths))
	for corrected, pa := range paths {
		var from, to int64
		for id, n := range pa.net {
			switch {
			case n == -1:
				from = id
			case n == 1:
				to = id
			case n != 0:
				return nil, fmt.Errorf("corrections of transaction %d do not form a path", corrected)
			}
		}
		if from == 0 && to == 0 {
			continue // corrected back to where it started
		}
		if from == 0 || to == 0 {
			return nil, fmt.Errorf("corrections of transaction %d do not form a path", corrected)
		}
		if pa.outgoing {
			from, to = to, from
		}
		m := pa.move
		m.FromPurposeID, m.FromPurposeName = from, pa.names[from]
		m.ToPurposeID, m.ToPurposeName = to, pa.names[to]
		m.Note = nil
		if len(pa.notes) == 1 {
			for n := range pa.notes {
				m.Note = &n
			}
		}
		out[corrected] = m
	}
	return out, nil
}

func reportMoveFrom(row store.ListReportTransactionsRow) (ReportMove, error) {
	if row.TransferFromPurposeID == nil || row.TransferToPurposeID == nil ||
		row.TransferFromPurposeName == nil || row.TransferToPurposeName == nil {
		// postTransferPairTx always writes both legs, so this is a broken
		// ledger, and a trust report should say so rather than drop a row.
		return ReportMove{}, errors.New("purpose move is missing a leg")
	}
	return ReportMove{
		Amount:          money.FromDB(row.Amount),
		FromPurposeName: *row.TransferFromPurposeName,
		ToPurposeName:   *row.TransferToPurposeName,
		FromPurposeID:   *row.TransferFromPurposeID,
		ToPurposeID:     *row.TransferToPurposeID,
		IsCorrection:    row.TransferCorrectsTransactionID != nil,
		IsAllocation:    row.TransferReason != nil && *row.TransferReason == reasonAllocation,
		Note:            row.Note,
	}, nil
}

// moveMatches: a move has no member and no direction, so either filter hides
// it; a purpose filter matches either side.
func moveMatches(p ReportParams, m ReportMove) bool {
	if p.MemberID != nil || p.Direction != "" {
		return false
	}
	if p.PurposeID != nil && *p.PurposeID != m.FromPurposeID && *p.PurposeID != m.ToPurposeID {
		return false
	}
	return true
}

// entryMatches applies the filters to one entry by its walk line (ADR-038): a
// reversal filters as its own line (Uang keluar for the reversal of an out), and
// an opening or a Penyesuaian is neither masuk nor keluar, so a direction or
// member filter hides it, as it hides a move. A purpose filter matches the row's
// purpose; an opening is Kas Utama's.
func entryMatches(p ReportParams, row store.ListReportTransactionsRow, e ReportEntry) bool {
	if p.AccountID != nil && *p.AccountID != row.AccountID {
		return false
	}
	if p.PurposeID != nil && *p.PurposeID != e.PurposeID {
		return false
	}
	if p.Direction != "" && string(e.Line) != string(p.Direction) {
		return false
	}
	if p.MemberID != nil {
		if e.Line == ReportLineOpening || e.Line == ReportLineAdjustment {
			return false
		}
		memberID := row.MemberID
		if memberID == nil {
			memberID = row.SettlementMemberID
		}
		if memberID == nil || *memberID != *p.MemberID {
			return false
		}
	}
	return true
}

func (l *Ledger) reportDues(ctx context.Context, fundID int64, month, status string) ([]ReportDuesRow, error) {
	statuses, err := l.DuesStatusForPeriod(ctx, fundID, month)
	if err != nil {
		return nil, err
	}
	tiers, err := l.q.ListDuesTiersByFund(ctx, fundID)
	if err != nil {
		return nil, fmt.Errorf("listing dues tiers: %w", err)
	}
	tierName := make(map[int64]string, len(tiers))
	for _, t := range tiers {
		tierName[t.ID] = t.Name
	}

	out := make([]ReportDuesRow, 0, len(statuses))
	for _, s := range statuses {
		if !duesStatusMatches(status, s.Status) {
			continue
		}
		var tier string
		if s.Member.TierID != nil {
			tier = tierName[*s.Member.TierID]
		}
		out = append(out, ReportDuesRow{
			MemberName: s.Member.Name, TierName: tier, Owed: s.OwedAmount, Paid: s.PaidAmount,
			Status: s.Status, PaidThrough: s.PaidThrough,
		})
	}
	return out, nil
}

func duesStatusMatches(filter string, s DuesStatus) bool {
	switch filter {
	case "":
		return true
	case ReportDuesPaid:
		return s == DuesStatusPaid || s == DuesStatusPaidInAdvance
	default:
		return string(s) == filter
	}
}

// reportEnvelopes lists every envelope open at some point during the period
// (opened by its last day, not closed before its first), each with its
// recipients and participation table. For a period reaching today that is every
// open envelope plus any closed in it (ADR-035); for a past one, the envelopes
// as they stood then. Oldest first, then by name.
func (l *Ledger) reportEnvelopes(ctx context.Context, fundID int64, period reportPeriod, purposes []store.Purpose, envelopes []store.Incidental, b reportBound) ([]ReportEnvelope, error) {
	nameByPurpose := make(map[int64]string, len(purposes))
	for _, pu := range purposes {
		nameByPurpose[pu.ID] = pu.Name
	}

	var out []ReportEnvelope
	for _, e := range envelopes {
		// opened_on and closed_on are CHECKed "YYYY-MM-DD", so they compare as
		// days. An envelope opened after the period did not exist yet in it.
		if e.OpenedOn > period.endDay() {
			continue
		}
		// Closed before the period began: it was not open in it.
		if e.ClosedOn != nil && *e.ClosedOn < period.startDay() {
			continue
		}

		// Every figure on the card is as of the bound (ADR-037), so a past month's
		// card agrees with the same envelope's Saldo per pos line.
		balance, err := l.reportPurposeBalance(ctx, fundID, e.PurposeID, b)
		if err != nil {
			return nil, err
		}
		part, err := l.incidentalParticipation(ctx, fundID, e.PurposeID, b.through)
		if err != nil {
			return nil, err
		}
		totals, err := l.q.IncidentalActivityTotals(ctx, store.IncidentalActivityTotalsParams{FundID: fundID, PurposeID: e.PurposeID, Through: b.through})
		if err != nil {
			return nil, fmt.Errorf("computing incidental activity totals: %w", err)
		}

		env := ReportEnvelope{
			PurposeID: e.PurposeID, Name: nameByPurpose[e.PurposeID],
			OpenedOn: e.OpenedOn, ClosedOn: e.ClosedOn, Balance: balance,
			Collected: money.FromDB(totals.CollectedAmount),
		}
		// Closed after a past period ended: it was still open at its end.
		if b.through != nil && e.ClosedOn != nil && *e.ClosedOn > *b.through {
			env.ClosedOn = nil
		}
		if e.TargetAmount != nil {
			v := money.FromDB(*e.TargetAmount)
			env.TargetAmount = &v
		}
		if e.MinimumPerMember != nil {
			v := money.FromDB(*e.MinimumPerMember)
			env.MinimumPerMember = &v
		}
		recipients, err := l.q.ListIncidentalRecipients(ctx, e.PurposeID)
		if err != nil {
			return nil, fmt.Errorf("listing recipients: %w", err)
		}
		for _, rc := range recipients {
			env.Recipients = append(env.Recipients, rc.MemberName)
		}
		for _, m := range part.Expected {
			env.Expected = append(env.Expected, ReportParticipant{MemberName: m.Member.Name, Amount: m.ContributedAmount, State: m.State})
		}
		for _, u := range part.Unexpected {
			env.Unexpected = append(env.Unexpected, ReportContributor{MemberName: u.Member.Name, Amount: u.ContributedAmount})
		}
		out = append(out, env)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OpenedOn != out[j].OpenedOn {
			return out[i].OpenedOn < out[j].OpenedOn
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
