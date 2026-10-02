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
	// a field rather than a Ledger setting because the report is the only
	// ledger read that reckons a calendar day, and backup already passes its
	// clock as an argument the same way (WriteDump, EnsureBootDump).
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
// join or leave date. The treasurer collected those for her own use (rule 6).
type Report struct {
	FundName string

	// AsOf is today in Asia/Jakarta ("YYYY-MM-DD"): the "per <date>" the
	// header's balances are true as of. The header is always current, whatever
	// month is selected - a past month's rows sit under today's balance.
	AsOf string

	// Month is the month shown ("YYYY-MM"); Months is every month offered,
	// oldest first: the first transaction's month through the current one.
	Month  string
	Months []string

	// Balance is the fund's one pooled balance (FundBalance).
	Balance money.Amount

	// Reconciliation is nil when no count has ever been taken. A fund nobody
	// has counted is not a fund that matches (ReconciliationBanner's reasoning).
	Reconciliation *ReportReconciliation

	// PurposeBalances is Kas Utama first, then each open envelope (oldest
	// first), then each pass-through (by name). A closed envelope is gone from
	// it: closing rolled its leftover into Kas Utama.
	PurposeBalances []ReportPurposeBalance

	// Rows are the month's rows after filters, newest first. Rows dated the
	// same day are in no particular order.
	Rows   []ReportRow
	Totals ReportTotals

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

// ReportEntry is an ordinary ledger row, with the facts a label needs.
type ReportEntry struct {
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
// leftover rolled into Kas Utama, or a purpose correction. It moves nothing
// in or out of the fund, so it is outside the totals.
type ReportMove struct {
	Amount          money.Amount
	FromPurposeName string
	ToPurposeName   string
	FromPurposeID   int64
	ToPurposeID     int64

	// IsCorrection tells a purpose correction from an envelope's roll: only a
	// correction names the row it corrects (ADR-033).
	IsCorrection bool
	Note         *string
}

// ReportTotals are money in, money out and net for the filtered entries.
// Moves are in none of them, and neither is a between-accounts transfer.
type ReportTotals struct {
	In  money.Amount
	Out money.Amount
	Net money.Amount
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
//   - A reclass_purpose pair is one ReportMove, outside the totals. It shows
//     when the purpose filter equals either side, and is hidden by a member or
//     direction filter, which it has neither of.
//   - Everything else is a ReportEntry and counts: in, out, net.
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
		Month:    month,
	}

	if r.Months, err = l.reportMonths(ctx, p.FundID, currentMonth); err != nil {
		return Report{}, err
	}
	if r.Balance, err = l.FundBalance(ctx, p.FundID); err != nil {
		return Report{}, err
	}
	if r.Reconciliation, err = l.reportReconciliation(ctx, p.FundID); err != nil {
		return Report{}, err
	}

	envelopes, err := l.q.ListIncidentalsByFund(ctx, p.FundID)
	if err != nil {
		return Report{}, fmt.Errorf("listing incidentals: %w", err)
	}
	if r.PurposeBalances, err = l.reportPurposeBalances(ctx, p.FundID, envelopes); err != nil {
		return Report{}, err
	}

	if r.Rows, r.Totals, err = l.reportRows(ctx, p, monthStart); err != nil {
		return Report{}, err
	}

	if r.Dues, err = l.reportDues(ctx, p.FundID, month, p.DuesStatus); err != nil {
		return Report{}, err
	}
	if r.Envelopes, err = l.reportEnvelopes(ctx, p.FundID, month, envelopes); err != nil {
		return Report{}, err
	}

	return r, nil
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
func (l *Ledger) reportMonths(ctx context.Context, fundID int64, currentMonth string) ([]string, error) {
	current, err := time.Parse(reportMonthLayout, currentMonth)
	if err != nil {
		return nil, fmt.Errorf("parsing current month %q: %w", currentMonth, err)
	}

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

// reportReconciliation reads the banner's three states: nil for never counted,
// otherwise the latest count's date and the fund-wide open difference.
func (l *Ledger) reportReconciliation(ctx context.Context, fundID int64) (*ReportReconciliation, error) {
	latest, err := l.q.LatestReconciliation(ctx, fundID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest reconciliation: %w", err)
	}

	open, err := l.q.ListOpenReconciliationLinesByFund(ctx, fundID)
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

// reportPurposeBalances is the header's per-purpose figures: Kas Utama, each
// open envelope, each pass-through. Ordered by kind, then by opened_on or name,
// never by id.
func (l *Ledger) reportPurposeBalances(ctx context.Context, fundID int64, envelopes []store.Incidental) ([]ReportPurposeBalance, error) {
	purposes, err := l.q.ListPurposesByFund(ctx, fundID)
	if err != nil {
		return nil, fmt.Errorf("listing purposes: %w", err)
	}
	envelopeByPurpose := make(map[int64]store.Incidental, len(envelopes))
	for _, e := range envelopes {
		envelopeByPurpose[e.PurposeID] = e
	}

	type ranked struct {
		purpose  store.Purpose
		openedOn string
	}
	var shown []ranked
	for _, pu := range purposes {
		e, isEnvelope := envelopeByPurpose[pu.ID]
		if isEnvelope && e.ClosedOn != nil {
			continue
		}
		shown = append(shown, ranked{purpose: pu, openedOn: e.OpenedOn})
	}

	kindRank := map[string]int{"main": 0, "incidental": 1, "pass_through": 2}
	sort.SliceStable(shown, func(i, j int) bool {
		a, b := shown[i], shown[j]
		if kindRank[a.purpose.Kind] != kindRank[b.purpose.Kind] {
			return kindRank[a.purpose.Kind] < kindRank[b.purpose.Kind]
		}
		if a.openedOn != b.openedOn {
			return a.openedOn < b.openedOn
		}
		return a.purpose.Name < b.purpose.Name
	})

	out := make([]ReportPurposeBalance, 0, len(shown))
	for _, s := range shown {
		bal, err := l.PurposeBalance(ctx, fundID, s.purpose.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ReportPurposeBalance{PurposeID: s.purpose.ID, Name: s.purpose.Name, Kind: s.purpose.Kind, Balance: bal})
	}
	return out, nil
}

// reportRows reads the month, folds transfer pairs, applies the filters, and
// totals the entries.
func (l *Ledger) reportRows(ctx context.Context, p ReportParams, monthStart time.Time) ([]ReportRow, ReportTotals, error) {
	rows, err := l.q.ListReportTransactions(ctx, store.ListReportTransactionsParams{
		FundID:   p.FundID,
		FromDate: monthStart.Format(reportDayLayout),
		ToDate:   monthStart.AddDate(0, 1, 0).Format(reportDayLayout),
	})
	if err != nil {
		return nil, ReportTotals{}, fmt.Errorf("listing report transactions: %w", err)
	}

	var (
		out      []ReportRow
		totals   ReportTotals
		moveSeen = make(map[int64]bool)
	)
	for _, row := range rows {
		if row.Kind == "transfer" {
			if row.TransferKind == nil || row.TransferID == nil {
				return nil, ReportTotals{}, fmt.Errorf("transfer row dated %s carries no transfer", row.OccurredOn)
			}
			switch *row.TransferKind {
			case "between_accounts":
				continue
			case "reclass_purpose":
				// Both legs describe the same pair; fold on the first.
				if moveSeen[*row.TransferID] {
					continue
				}
				moveSeen[*row.TransferID] = true

				move, err := reportMoveFrom(row)
				if err != nil {
					return nil, ReportTotals{}, err
				}
				if !moveMatches(p, move) {
					continue
				}
				out = append(out, ReportRow{Date: row.OccurredOn, Move: &move})
				continue
			default:
				return nil, ReportTotals{}, fmt.Errorf("unknown transfer kind %q", *row.TransferKind)
			}
		}

		entry := reportEntryFrom(row)
		if !entryMatches(p, row, entry) {
			continue
		}
		out = append(out, ReportRow{Date: row.OccurredOn, Entry: &entry})

		if entry.Direction == ReportDirectionIn {
			totals.In, err = totals.In.Add(entry.Amount)
		} else {
			totals.Out, err = totals.Out.Add(entry.Amount)
		}
		if err != nil {
			return nil, ReportTotals{}, fmt.Errorf("totalling report rows: %w", err)
		}
	}

	if totals.Net, err = totals.In.Sub(totals.Out); err != nil {
		return nil, ReportTotals{}, fmt.Errorf("netting report rows: %w", err)
	}
	return out, totals, nil
}

func reportEntryFrom(row store.ListReportTransactionsRow) ReportEntry {
	e := ReportEntry{
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
	return e
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

func entryMatches(p ReportParams, row store.ListReportTransactionsRow, e ReportEntry) bool {
	if p.PurposeID != nil && *p.PurposeID != e.PurposeID {
		return false
	}
	if p.Direction != "" && p.Direction != e.Direction {
		return false
	}
	if p.MemberID != nil {
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
func (l *Ledger) reportEnvelopes(ctx context.Context, fundID int64, month string, envelopes []store.Incidental) ([]ReportEnvelope, error) {
	purposes, err := l.q.ListPurposesByFund(ctx, fundID)
	if err != nil {
		return nil, fmt.Errorf("listing purposes: %w", err)
	}
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

		balance, err := l.PurposeBalance(ctx, fundID, e.PurposeID)
		if err != nil {
			return nil, err
		}
		part, err := l.GetIncidentalParticipation(ctx, fundID, e.PurposeID)
		if err != nil {
			return nil, err
		}

		env := ReportEnvelope{
			PurposeID: e.PurposeID, Name: nameByPurpose[e.PurposeID],
			OpenedOn: e.OpenedOn, ClosedOn: e.ClosedOn, Balance: balance,
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
