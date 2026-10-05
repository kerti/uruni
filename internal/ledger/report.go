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
	"github.com/kerti/uruni/internal/tz"
)

const (
	reportMonthLayout = "2006-01"
	reportDayLayout   = "2006-01-02"

	// ReportDirectionIn is one of the two values the direction filter takes;
	// "" means no direction filter.
	ReportDirectionIn = "in"
	// ReportDirectionOut is the other direction filter value.
	ReportDirectionOut = "out"

	// ReportDuesUnpaid is the dues filter's value for DuesStatusUnpaid.
	ReportDuesUnpaid = "unpaid"
	// ReportDuesPartial is the dues filter's value for DuesStatusPartial.
	ReportDuesPartial = "partial"
	// ReportDuesPaid also matches DuesStatusPaidInAdvance: a member who has
	// paid ahead has, plainly, paid.
	ReportDuesPaid = "paid"
)

// ReportParams is what MonthlyReport needs. Every filter is optional and the
// zero value means "no filter".
type ReportParams struct {
	FundID int64

	// Month is "YYYY-MM". Empty means the current month in Asia/Jakarta. A
	// malformed month is ErrInvalidArgument - reading an invalid ?month= as
	// the current one is the HTTP edge's call (ADR-035), not this layer's.
	Month string

	// PurposeID filters transactions to one purpose tag (the stored tag, the
	// one every balance sums). A purpose move matches when it names the
	// purpose on either side.
	PurposeID *int64

	// MemberID filters transactions to one member: the dues or contribution
	// member, or the member a settled claim paid back. A purpose move names
	// no member, so a member filter hides moves.
	MemberID *int64

	// Direction is ReportDirectionIn, ReportDirectionOut or "". A purpose
	// move is neither, so a direction filter hides moves.
	Direction string

	// DuesStatus is ReportDuesUnpaid, ReportDuesPartial, ReportDuesPaid or
	// "". It filters the dues table only, never the transactions (ADR-035).
	DuesStatus string

	// Now is the current instant, injectable so a test can stand on either
	// side of a Jakarta month boundary; the zero value means time.Now(). It is
	// a field rather than a Ledger setting so the clock is an argument, the
	// way backup passes its own (WriteDump, EnsureBootDump) and
	// OutstandingDuesForMember takes its now.
	Now time.Time
}

// Report is one fund's public report for one month (ADR-035): the single
// struct the HTML page and the monthly PDF both render, so nothing is
// recomputed in a template. Every field is a fact, never a label - the display
// text ("Iuran 2026-06 - Jane") is built at render time from them, the way
// Riwayat builds it (#257).
//
// What it deliberately does not carry: a location's name or balance, a
// receipt's path or id, a transaction id, a dues arrears count, or a member's
// join or leave date. The treasurer collected those for their own records (rule 6).
type Report struct {
	FundName string

	// AsOf is the day ("YYYY-MM-DD") every figure is true as of (ADR-037): the
	// last day of a past month, or today in Asia/Jakarta for the running month,
	// which Running marks: the month as it ended, or as it stands.
	AsOf    string
	Running bool

	// Month is the month shown ("YYYY-MM"); Months is every month offered,
	// oldest first: the first transaction's month through the current one.
	Month  string
	Months []string

	// Balance is the fund's one pooled balance (FundBalance).
	Balance money.Amount

	// OwedToMembers is what the fund owed members for talangan as of AsOf
	// (#406, #408).
	OwedToMembers money.Amount

	// Reconciliation is nil when no count has ever been taken. A fund nobody
	// has counted is not a fund that matches (ReconciliationBanner's reasoning).
	Reconciliation *ReportReconciliation

	// PurposeBalances is Kas Utama first, then each open envelope (oldest
	// first), then each pass-through (by name). A closed envelope is gone from
	// it: closing rolled its leftover into Kas Utama.
	PurposeBalances []ReportPurposeBalance

	// Rows are the month's rows after filters, newest first. Rows dated the
	// same day are in no particular order.
	Rows []ReportRow

	// Walk is the month as a walk from the balance it started on to the
	// balance it ended on (ADR-038), over the rows after filters.
	Walk ReportWalk

	// Dues is every member's status for Month, after the dues filter.
	Dues []ReportDuesRow

	// Envelopes are every envelope open at some point during Month.
	Envelopes []ReportEnvelope
}

// ReportReconciliation is the latest cek kas as the home banner reads it: its
// date, and whether the fund as a whole is square right now.
type ReportReconciliation struct {
	// Date is the count's day in Asia/Jakarta ("YYYY-MM-DD").
	Date string

	// Difference is the sum, by magnitude, of every difference still open
	// across the fund - the figure ReconciliationBanner shows - so a short
	// location and an over one cannot cancel each other into "cocok". Matched
	// is Difference == 0.
	Matched    bool
	Difference money.Amount
}

// ReportPurposeBalance is one purpose's running total.
type ReportPurposeBalance struct {
	PurposeID int64
	Name      string
	Kind      string // "main", "incidental" or "pass_through"
	Balance   money.Amount
}

// ReportRow is one line of the month. Exactly one of Entry and Move is set.
type ReportRow struct {
	Date  string // "YYYY-MM-DD"
	Entry *ReportEntry
	Move  *ReportMove
}

// ReportLine is the one line of the month's walk a row lands on (ADR-038).
// It is the single class that drives the list, the direction filter and the
// totals, so the three cannot disagree about a row.
type ReportLine string

const (
	// ReportLineOpening is an opening entry: Saldo awal.
	ReportLineOpening ReportLine = "opening"
	// ReportLineIn is income - and a reversal, which counts there as a minus.
	ReportLineIn ReportLine = "in"
	// ReportLineOut is spending.
	ReportLineOut ReportLine = "out"
	// ReportLineAdjustment is every adjustment that is not a reversal.
	ReportLineAdjustment ReportLine = "adjustment"
)

// ReportEntry is an ordinary ledger row, with the facts a label needs.
type ReportEntry struct {
	// Line is the walk line the row lands on; see classifyReportRow.
	Line ReportLine

	Kind        string // transaction.kind: opening, normal, dues, reimbursement or adjustment
	Direction   string // "in" or "out"
	Amount      money.Amount
	PurposeName string
	PurposeID   int64

	// MemberName is the dues member, the named contributor, or - on a
	// settled claim's payout - the member paid back; nil when the row names
	// none.
	MemberName *string

	// DuesPeriod is "YYYY-MM" on a dues row and on a dues reversal; nil on
	// every other row, a contribution's reversal included.
	DuesPeriod *string

	// IsReversal marks a reversal (kind adjustment): of a dues payment when
	// DuesPeriod is set, of a named contribution when it is not.
	IsReversal bool

	// IsReconciliationFix marks the entry that squared a reconciliation gap.
	IsReconciliationFix bool

	// Note is what the treasurer typed. On a settled claim's payout - whose
	// own note is always empty - it is the claim's note, the way Riwayat
	// shows it.
	Note *string

	// HasReceipt is whether a photo exists, never the photo.
	HasReceipt bool
}

// ReportMove is a reclass_purpose pair folded into one row: an envelope's
// leftover rolled into Kas Utama, a purpose correction, or money the
// treasurer moved between purposes (ADR-036). It moves nothing in or out of
// the fund, so it is outside the totals.
type ReportMove struct {
	Amount          money.Amount
	FromPurposeName string
	ToPurposeName   string
	FromPurposeID   int64
	ToPurposeID     int64

	// IsCorrection tells a purpose correction from an envelope's roll: only a
	// correction names the row it corrects (ADR-033).
	IsCorrection bool

	// IsAllocation tells money the treasurer moved between purposes
	// ("Pindah pos", transfer.reason 'allocation', ADR-036) from an
	// envelope's roll. Neither IsCorrection nor IsAllocation: a roll, which
	// is also what a pair from before the column reads as.
	IsAllocation bool
	Note         *string
}

// ReportWalk is the month as ADR-038 reads it: a walk from the balance the
// month started on to the balance it ended on, in which every posted row lands
// on exactly one line.
//
//	Start + Openings + In - Out + Adjustments + Moved = End
//
// In is net of reversals, so it can read below zero; Adjustments and Moved are
// signed. Start and End are ledger sums (the bounded fund or purpose balance),
// never computed from the lines, so the equation above is a property the tests
// assert and not something the report forces; it is not checked at runtime,
// because a public page does not fail on a neighbour.
//
// Full says whether the walk applies: with no filter (the whole fund) or with
// a purpose filter alone (that pos, whose Moved line is the net of its
// reclass_purpose legs). Under a member or direction filter only In and Out
// are meaningful (the lines of the rows that matched): Start and End stay
// zero, since a balance has no meaning for one member or one direction.
type ReportWalk struct {
	Full bool

	// StartOn and EndOn date the two balances ("YYYY-MM-DD"): the last day of
	// the previous month, and the month's last day (today, for the running
	// month). Set with Full.
	StartOn string
	EndOn   string

	Start       money.Amount
	Openings    money.Amount
	In          money.Amount
	Out         money.Amount
	Adjustments money.Amount

	// Moved is a purpose filter's own line: the signed net of that purpose's
	// reclass_purpose legs dated in the month (an envelope's roll, a Pindah pos,
	// a purpose correction), summed from the raw legs rather than from the
	// folded display rows, so it equals the change in the pos's balance whatever
	// the display folds. Always zero for the whole fund, where the two legs of a
	// move cancel.
	Moved money.Amount
	End   money.Amount
}

// ReportDuesRow is one member's standing for the month - DuesStatusForPeriod's
// row with the member's tier by name, and without their dates. Names and tiers
// are public by the maintainer's call (2026-10-02): the slug is the guard.
type ReportDuesRow struct {
	MemberName  string
	TierName    string // "" when the member has no tier
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

	// Collected is what the occasion itself took in, as the envelope screen's
	// "Terkumpul" reads it (GetIncidentalDetail) - not Balance, which is zero
	// on a closed envelope once its leftover has rolled out.
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

// MonthlyReport assembles everything the public report shows for one fund and
// one month (ADR-035, #372). Unlike the ledger's other reads it runs inside one
// transaction: it is a dozen queries, and a write landing between two of them
// could show a header balance one row newer than the rows beneath it - on the
// one page whose job is to show that the numbers agree. Every helper it calls
// reads through the snapshot's q, so nothing reaches for a second connection.
//
// Which ledger rows appear, and where they count:
//   - A between_accounts transfer is left out entirely: with locations hidden
//     it moves nothing a reader can see, and its two legs would double a total.
//   - A reclass_purpose pair is one ReportMove, outside the walk. It shows
//     when the purpose filter equals either side, and is hidden by a member or
//     direction filter, which it has neither of.
//   - Everything else is a ReportEntry on exactly one line of the walk
//     (ADR-038, classifyReportRow): Saldo awal, Total masuk, Total keluar or
//     Penyesuaian.
//
// Filtering happens in Go over the month's rows, not in SQL, because a pair
// must be folded before a purpose filter can ask whether either side matches.
func (l *Ledger) MonthlyReport(ctx context.Context, p ReportParams) (Report, error) {
	var r Report
	err := l.withTx(ctx, func(q store.Querier) error {
		var err error
		r, err = (&Ledger{db: l.db, q: q}).monthlyReport(ctx, p)
		return err
	})
	return r, err
}

func (l *Ledger) monthlyReport(ctx context.Context, p ReportParams) (Report, error) {
	if p.Direction != "" && p.Direction != ReportDirectionIn && p.Direction != ReportDirectionOut {
		return Report{}, fmt.Errorf("%w: direction %q is not \"in\" or \"out\"", ErrInvalidArgument, p.Direction)
	}
	switch p.DuesStatus {
	case "", ReportDuesUnpaid, ReportDuesPartial, ReportDuesPaid:
	default:
		return Report{}, fmt.Errorf("%w: dues status %q is not unpaid, partial or paid", ErrInvalidArgument, p.DuesStatus)
	}

	now := p.Now
	if now.IsZero() {
		now = time.Now()
	}
	today := now.In(tz.Jakarta)
	currentMonth := today.Format(reportMonthLayout)

	month := p.Month
	if month == "" {
		month = currentMonth
	}
	monthStart, err := parseReportMonth(month)
	if err != nil {
		return Report{}, err
	}

	fund, err := l.q.GetFund(ctx, p.FundID)
	if err != nil {
		return Report{}, fmt.Errorf("fetching fund: %w", err)
	}

	r := Report{
		FundName: fund.Name,
		AsOf:     today.Format(reportDayLayout),
		Running:  month >= currentMonth,
		Month:    month,
	}
	b := reportBound{}
	if !r.Running {
		b = boundAtMonthEnd(monthStart)
		r.AsOf = *b.through
	}

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
	if r.Reconciliation, err = l.reportReconciliation(ctx, p.FundID, b); err != nil {
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

	if r.Rows, r.Walk, err = l.reportRows(ctx, p, monthStart, r.Running); err != nil {
		return Report{}, err
	}
	// The ends of the walk are ledger sums, and only the whole fund or one pos
	// has them. A purpose's are the same bounded figures as its Saldo per pos
	// line; a purpose that did not exist yet simply sums to nothing.
	if r.Walk.Full {
		dayBefore := monthStart.AddDate(0, 0, -1).Format(reportDayLayout)
		r.Walk.StartOn, r.Walk.EndOn = dayBefore, r.AsOf
		if p.PurposeID == nil {
			start, err := l.q.ReportFundBalance(ctx, store.ReportFundBalanceParams{FundID: p.FundID, Through: &dayBefore})
			if err != nil {
				return Report{}, fmt.Errorf("report start balance: %w", err)
			}
			r.Walk.Start, r.Walk.End = money.FromDB(start), r.Balance
		} else {
			if r.Walk.Start, err = l.reportPurposeBalance(ctx, p.FundID, *p.PurposeID, reportBound{through: &dayBefore}); err != nil {
				return Report{}, err
			}
			if r.Walk.End, err = l.reportPurposeBalance(ctx, p.FundID, *p.PurposeID, b); err != nil {
				return Report{}, err
			}
		}
	}

	if r.Dues, err = l.reportDues(ctx, p.FundID, month, p.DuesStatus); err != nil {
		return Report{}, err
	}
	if r.Envelopes, err = l.reportEnvelopes(ctx, p.FundID, month, purposes, envelopes, b); err != nil {
		return Report{}, err
	}

	return r, nil
}

// reportBound is where a past month's figures stop (ADR-037): through is its
// last day, compared against the ledger's day columns; before is the first
// instant of the next month in Jakarta, compared against a count's
// performed_at. Both nil is the running month, which reads every row - the
// same figures Beranda shows.
type reportBound struct {
	through *string
	before  *int64
}

func boundAtMonthEnd(monthStart time.Time) reportBound {
	last := monthStart.AddDate(0, 1, -1).Format(reportDayLayout)
	next := time.Date(monthStart.Year(), monthStart.Month()+1, 1, 0, 0, 0, 0, tz.Jakarta).Unix()
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

// reportMonths lists the months the report offers, oldest first: the first
// transaction's month through the current Jakarta month. An empty fund, or one
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
// fund-wide difference still open at the bound.
func (l *Ledger) reportReconciliation(ctx context.Context, fundID int64, b reportBound) (*ReportReconciliation, error) {
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
		d := money.FromDB(line.DifferenceAmount)
		if d < 0 {
			d = -d
		}
		if difference, err = difference.Add(d); err != nil {
			return nil, fmt.Errorf("summing open differences: %w", err)
		}
	}

	return &ReportReconciliation{
		Date:       time.Unix(latest.PerformedAt, 0).In(tz.Jakarta).Format(reportDayLayout),
		Matched:    difference == 0,
		Difference: difference,
	}, nil
}

// reportPurposeBalances is the header's per-purpose figures as of the bound:
// Kas Utama, each envelope open then, each pass-through that existed then -
// and any other purpose still holding money then, so the lines always sum to
// Saldo kas. That last case is a purpose created or opened after the month
// with rows dated inside it, which back-filling produces (#408). A closed
// envelope holds nothing (ADR-031), so it stays hidden. In ListPurposesByFund's
// order: Kas Utama first, then by name.
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
		case pu.Kind == "pass_through" && b.before != nil:
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
		out = append(out, ReportPurposeBalance{PurposeID: s.purpose.ID, Name: s.purpose.Name, Kind: s.purpose.Kind, Balance: s.balance})
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
// shows them (ADR-038): the very figures the report's running month carries as
// Walk.In and Walk.Out, because it reads them through the same reportRows
// over the same unbounded, unfiltered month. The month is read in Asia/Jakarta
// from now. In is net of reversals and so can be negative; Out is a magnitude.
func (l *Ledger) MonthInOut(ctx context.Context, fundID int64, now time.Time) (in, out money.Amount, err error) {
	if now.IsZero() {
		now = time.Now()
	}
	monthStart, err := parseReportMonth(now.In(tz.Jakarta).Format(reportMonthLayout))
	if err != nil {
		return 0, 0, err
	}
	err = l.withTx(ctx, func(q store.Querier) error {
		_, walk, err := (&Ledger{db: l.db, q: q}).reportRows(ctx, ReportParams{FundID: fundID}, monthStart, true)
		if err != nil {
			return err
		}
		in, out = walk.In, walk.Out
		return nil
	})
	return in, out, err
}

// reportRows reads the month, folds transfer pairs, applies the filters, and
// lands each entry on its line of the walk. The running month reads with no
// upper bound, matching its unbounded balance (ADR-037, ADR-038): a row dated
// after today still belongs to the walk that ends on that balance.
func (l *Ledger) reportRows(ctx context.Context, p ReportParams, monthStart time.Time, running bool) ([]ReportRow, ReportWalk, error) {
	var to *string
	if !running {
		next := monthStart.AddDate(0, 1, 0).Format(reportDayLayout)
		to = &next
	}
	rows, err := l.q.ListReportTransactions(ctx, store.ListReportTransactionsParams{
		FundID:   p.FundID,
		FromDate: monthStart.Format(reportDayLayout),
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
	)
	for _, row := range rows {
		if row.Kind == "transfer" {
			if row.TransferKind == nil || row.TransferID == nil {
				return nil, ReportWalk{}, fmt.Errorf("transfer row dated %s carries no transfer", row.OccurredOn)
			}
			switch *row.TransferKind {
			case "between_accounts":
				continue
			case "reclass_purpose":
				// Dipindah is the raw leg, whatever the display folds: a pair
				// of corrections that folds to nothing still posted legs, and
				// this purpose's balance moved by each.
				if walk.Full && p.PurposeID != nil && row.PurposeID == *p.PurposeID {
					leg := ReportEntry{Direction: row.Direction, Amount: money.FromDB(row.Amount)}
					signed, err := leg.signed()
					if err != nil {
						return nil, ReportWalk{}, err
					}
					if walk.Moved, err = walk.Moved.Add(signed); err != nil {
						return nil, ReportWalk{}, fmt.Errorf("totalling moved legs: %w", err)
					}
				}

				// Both legs describe the same pair; fold on the first.
				if moveSeen[*row.TransferID] {
					continue
				}
				moveSeen[*row.TransferID] = true

				// Every correction of one row reads as a single net move
				// (#280): emitted once, at the first leg met, and not at all
				// when the corrections cancel out.
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
		out = append(out, ReportRow{Date: row.OccurredOn, Entry: &entry})

		if err := walk.add(entry); err != nil {
			return nil, ReportWalk{}, fmt.Errorf("totalling report rows: %w", err)
		}
	}
	return out, walk, nil
}

// signed is the amount as the ledger's balance reads it: in is plus, out minus.
func (e ReportEntry) signed() (money.Amount, error) {
	if e.Direction == ReportDirectionIn {
		return e.Amount, nil
	}
	return money.Amount(0).Sub(e.Amount)
}

// add lands one entry on its line of the walk. A reversal is stored out and
// lands on In as a minus; an adjustment is signed by its own direction.
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
		// Out is a magnitude: spending, shown as a positive figure and
		// subtracted by the walk.
		w.Out, err = w.Out.Add(e.Amount)
	case ReportLineAdjustment:
		w.Adjustments, err = w.Adjustments.Add(signed)
	default:
		return fmt.Errorf("entry has no walk line %q", e.Line)
	}
	return err
}

// classifyReportRow is ADR-038's one switch: the line a posted row lands on.
// Total by construction - a kind it does not know is an error, not a guess,
// so a trust report says the ledger is broken rather than drop a row.
// Transfers never reach it; the caller folds or skips them first.
func classifyReportRow(kind, direction string, reverses bool) (ReportLine, error) {
	switch kind {
	case "opening":
		return ReportLineOpening, nil
	case "normal", "dues", "reimbursement":
		if direction == ReportDirectionIn {
			return ReportLineIn, nil
		}
		return ReportLineOut, nil
	case "adjustment":
		if reverses {
			return ReportLineIn, nil
		}
		return ReportLineAdjustment, nil
	}
	return "", fmt.Errorf("transaction kind %q has no line in the walk", kind)
}

func reportEntryFrom(row store.ListReportTransactionsRow) (ReportEntry, error) {
	line, err := classifyReportRow(row.Kind, row.Direction, row.ReversesTransactionID != nil)
	if err != nil {
		return ReportEntry{}, err
	}
	e := ReportEntry{
		Line:                line,
		Kind:                row.Kind,
		Direction:           row.Direction,
		Amount:              money.FromDB(row.Amount),
		PurposeName:         row.PurposeName,
		PurposeID:           row.PurposeID,
		MemberName:          row.MemberName,
		DuesPeriod:          row.DuesPeriod,
		IsReversal:          row.ReversesTransactionID != nil,
		IsReconciliationFix: row.IsReconciliationFix != 0,
		Note:                row.Note,
		HasReceipt:          row.HasReceipt != 0,
	}
	// A settlement's own row names no member and holds no note: both come from
	// the claim it settles. At most one of the two member columns is ever set.
	if e.MemberName == nil {
		e.MemberName = row.SettlementMemberName
	}
	if row.Kind == "reimbursement" {
		e.Note = row.ClaimNote
	}
	return e, nil
}

// foldCorrections nets every purpose correction of the same row into one
// move, keyed by the corrected row's id (#280). A row corrected Perpisahan ->
// Kas Bidang -> Kas Utama -> Kas Bidang reads as Perpisahan -> Kas Bidang; one
// corrected and then corrected back is absent. Display only: every leg stays
// posted, and the balances above are sums of them all.
//
// Each correction moves the whole row from wherever it is now, so the
// corrections of one row form a path. Its ends are the purpose left once more
// than entered (the start) and the one entered once more than left (the end),
// which needs no ordering - ids are not an order to rely on. Every correction
// carries the row's own date, so they all fall in that row's month together.
func foldCorrections(rows []store.ListReportTransactionsRow) (map[int64]ReportMove, error) {
	type path struct {
		move  ReportMove
		net   map[int64]int // purpose id -> times entered minus times left
		names map[int64]string
		notes map[string]bool
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
			pa = &path{move: m, net: map[int64]int{}, names: map[int64]string{}, notes: map[string]bool{}}
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
// reversal filters as Uang masuk, and an opening or a Penyesuaian is neither
// masuk nor keluar, so a direction or member filter hides it, as it hides a move.
// A purpose filter matches the row's own purpose; an opening is always Kas Utama's.
func entryMatches(p ReportParams, row store.ListReportTransactionsRow, e ReportEntry) bool {
	if p.PurposeID != nil && *p.PurposeID != e.PurposeID {
		return false
	}
	if p.Direction != "" && string(e.Line) != p.Direction {
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

// reportEnvelopes lists every envelope that was open at some point during
// month - opened by its end, not closed before its start - each with its
// recipients and participation table. For the current month that is every
// open envelope plus any closed in it (ADR-035); for a past month it is the
// envelopes as they stood then, so an occasion is readable in every month it
// ran, not only the one it closed in. Oldest first, then by name.
func (l *Ledger) reportEnvelopes(ctx context.Context, fundID int64, month string, purposes []store.Purpose, envelopes []store.Incidental, b reportBound) ([]ReportEnvelope, error) {
	nameByPurpose := make(map[int64]string, len(purposes))
	for _, pu := range purposes {
		nameByPurpose[pu.ID] = pu.Name
	}

	var out []ReportEnvelope
	for _, e := range envelopes {
		// opened_on and closed_on are CHECKed "YYYY-MM-DD", so a prefix is a
		// month. An envelope opened after the month did not exist yet in it.
		if e.OpenedOn[:len(reportMonthLayout)] > month {
			continue
		}
		// Closed before the month began: it was not open in it.
		if e.ClosedOn != nil && (*e.ClosedOn)[:len(reportMonthLayout)] < month {
			continue
		}

		// Every figure on the card is as of the bound (ADR-037), so a past
		// month's card agrees with the same envelope's Saldo per pos line.
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
		// Closed after a past month ended: it was still open at its end.
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
