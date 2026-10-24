package http

// The public report (ADR-035, #373): GET /report/{slug}, server-rendered with
// html/template from ledger.MonthlyReport. No session, no resolveFund: the
// slug names its fund (ADR-030's carve-out) and is a read capability, never
// an authorization. The page is the header and month selector, then the
// balance, per-purpose balances, what is owed to members, transactions, dues
// and envelopes, all from the same Report.

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

//go:embed report.html
var reportTemplateSource string

var reportTemplates = template.Must(template.New("report").Parse(reportTemplateSource))

// reportPage is everything the "report" template reads. Every string is
// already display text (formatted rupiah, Indonesian dates), so the template
// computes nothing (ADR-035).
type reportPage struct {
	Title string
	Style template.CSS
	Text  reportCopy

	// Nonce is this response's CSP nonce, on the page's one <style> and one
	// <script> (renderReport sets it).
	Nonce string

	FundName string
	AsOf     string
	Balance  string
	// BalanceSize is a class that steps the hero figure down when wide (up to Rp
	// 999.999.999.999 and a minus, #540) so it never wraps or clips. "" at
	// everyday widths.
	BalanceSize string
	Check       *reportCheck
	Purposes    []reportPurpose

	// Owed is the total owed to members who fronted an expense, as of the same
	// day as Balance (ADR-037); "" hides the line (#406).
	Owed string

	PDFHref string

	PrevMonth    string
	NextMonth    string
	PrevHref     string
	NextHref     string
	MonthOptions []reportMonthOption

	// The transactions section (#374): the filter form, totals for the filtered
	// set, and rows. The handler sets Filter; it alone knows the fund's members
	// and purposes.
	Filter reportFilter
	Totals []reportTotal
	Rows   []reportRow
	Month  string
	NoRows bool

	// The dues section (#375). HasDues is whether any member owes for the month
	// before the dues filter, so a filter matching nobody still shows the
	// section and filter.
	HasDues bool
	Dues    []reportDuesRow

	// Grid is the twelve-month dues grid (ADR-044) that opens the dues section;
	// nil when fewer than two months owe anything. It ignores every filter.
	Grid *reportGrid

	// The envelopes section (#338): every envelope the ledger listed for the
	// month, narrowed by the purpose filter; hidden when empty.
	Envelopes []reportEnvelope

	// Empty is a fund with nothing recorded at all, not a quiet month: it gets
	// the warm line instead of a bare Rp 0.
	Empty bool
}

// reportCheck is the latest cek kas as the home banner reads it: matched, a
// discrepancy in terracotta, or never counted (neutral, never green).
type reportCheck struct {
	Class   string // "matched", "discrepancy" or "never"
	Message string
	When    string
}

type reportPurpose struct {
	Name     string
	Balance  string
	Negative bool
}

// reportTotal is one line of the month's walk (ADR-038), formatted: label and
// figure. Last marks the closing balance, which both renderers emphasise. The
// whole fund, or one pos, reads Saldo <day> down to Saldo <day>; under a
// member or direction filter only Total masuk and Total keluar, since a
// balance has no meaning for a slice.
type reportTotal struct {
	Label string
	Value string
	Last  bool
}

// reportRow is one line of the month. Class is "in", "out" or "move"; Label is
// "" for a plain row. Note is what she typed, under the label as Riwayat
// shows it (a settled claim's payout carries the claim's note; a move, the
// note both legs carry), "" for none. HasReceipt renders a plain marker;
// nothing about the receipt reaches the page.
type reportRow struct {
	Date       string
	Label      string
	Note       string
	Purpose    string
	Amount     string
	Class      string
	HasReceipt bool
	// Reversed marks the original of a pair: Sudah dibatalkan (ADR-042).
	Reversed bool
}

// reportNote is a row's note as the report prints it: trimmed, "" for none.
func reportNote(note *string) string {
	if note == nil {
		return ""
	}
	return strings.TrimSpace(*note)
}

// reportFilter is the GET form: every option of every select, the current
// choice marked, so a filtered view reads back as what it is.
type reportFilter struct {
	Purposes   []reportOption
	Members    []reportOption
	Directions []reportOption
	Dues       []reportOption

	// Active is the filters in force, carried by the month steps and month form
	// so changing month keeps them.
	Active url.Values
}

// reportDuesRow is one member's standing. Class is the ledger's status value,
// which the stylesheet colours; Status is the app's wording.
type reportDuesRow struct {
	Name        string
	Tier        string
	Owed        string
	Paid        string
	Class       string
	Status      string
	PaidThrough string
}

// reportGrid is the twelve-month dues grid, already display text: the page and
// the PDF draw the same cells. Status only; no count, total, amount or order
// beyond the ledger's (by name).
type reportGrid struct {
	Heading string
	Range   string // "Oktober 2025 - September 2026"
	Caption string // "Status per 24 Oktober 2026" on a past month; "" on the running one
	Years   []reportGridYear
	Heads   []reportGridHead
	Rows    []reportGridRow
	Legend  []reportGridCell
}

// reportGridYear is the year above its first column and each January.
type reportGridYear struct {
	Year string
	Span int
}

// reportGridHead is a column head: the short name drawn, the full month read
// aloud. Start marks the first column of a year.
type reportGridHead struct {
	Short string
	Full  string
	Year  string
	Start bool
}

type reportGridRow struct {
	Name  string
	Cells []reportGridCell
}

// reportGridCell is one shape. Class is the ledger's cell value ("paid",
// "partial", "unpaid", "none"), which picks the shape and the tone; Label is
// the month and status as text for assistive technology (the legend's is the
// bare word). Start marks the first column of a year.
type reportGridCell struct {
	Class string
	Label string
	Start bool
}

// buildReportGrid shapes the ledger's grid, nil when fewer than two months owe
// anything: a one-column grid repeats the table.
func buildReportGrid(r ledger.Report) *reportGrid {
	g := r.DuesGrid
	if len(g.Periods) < 2 {
		return nil
	}
	out := &reportGrid{
		Heading: reportText.GridHeading,
		Range:   reportText.GridRange(reportText.monthName(g.Periods[0]), reportText.monthName(g.Periods[len(g.Periods)-1])),
	}
	if !r.Running {
		out.Caption = reportText.GridAsOf(reportText.longDate(r.Today))
	}
	fulls := make([]string, len(g.Periods))
	starts := make([]bool, len(g.Periods))
	for i, period := range g.Periods {
		fulls[i] = reportText.monthName(period)
		year, month := period[:4], period[5:]
		starts[i] = i == 0 || month == "01"
		if starts[i] {
			out.Years = append(out.Years, reportGridYear{Year: year, Span: 0})
		}
		out.Years[len(out.Years)-1].Span++
		m, _ := strconv.Atoi(month)
		out.Heads = append(out.Heads, reportGridHead{Short: reportText.MonthHeads[m-1], Full: fulls[i], Year: year, Start: starts[i]})
	}
	for _, row := range g.Rows {
		cells := make([]reportGridCell, len(row.Cells))
		for i, c := range row.Cells {
			cells[i] = reportGridCell{Class: string(c), Label: reportText.GridCellLabel(fulls[i], reportText.gridWord(c)), Start: starts[i]}
		}
		out.Rows = append(out.Rows, reportGridRow{Name: row.Name, Cells: cells})
	}
	for _, c := range []ledger.DuesGridCell{ledger.DuesCellPaid, ledger.DuesCellPartial, ledger.DuesCellUnpaid, ledger.DuesCellNone} {
		out.Legend = append(out.Legend, reportGridCell{Class: string(c), Label: reportText.gridWord(c)})
	}
	return out
}

// reportEnvelope is one envelope's <details>: the summary's facts, who it is
// for, and the participation table. A person's Class is the ledger's state
// ("sudah", "belum", "kurang"), which the stylesheet colours.
type reportEnvelope struct {
	Name       string
	Collected  string
	Given      string // "8 dari 12 sudah menyumbang"; "" when nobody is expected
	Status     string
	Closed     bool
	Recipients string // "Untuk: A, B"; "" when none
	Expected   []reportPerson
	Others     []reportPerson
	NoneExpect bool
}

type reportPerson struct {
	Name   string
	Amount string // "" when the member has given nothing
	Class  string
	Status string
}

type reportOption struct {
	Value    string
	Label    string
	Selected bool
}

type reportMonthOption struct {
	Value    string
	Label    string
	Selected bool
}

// reportHandler serves GET /report/{slug}. now is injectable so a test can
// pin the month in the fund's zone; production passes time.Now.
func reportHandler(l *ledger.Ledger, q store.Querier, logger *slog.Logger, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)

		logger := loggerFrom(r.Context(), logger)
		fund, ok := reportFundOr404(w, r, q, logger)
		if !ok {
			return
		}

		page, err := assembleReportPage(r.Context(), l, q, fund.ID, r.URL.Query(), now())
		if err != nil {
			logger.Error("report: assembling the month", "fund_id", fund.ID, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		// The link is to the month the page shows, the ledger's validated "YYYY-MM",
		// never the raw query value.
		page.PDFHref = "/report/" + url.PathEscape(fund.ReportSlug) + "/pdf?month=" + url.QueryEscape(page.Month)
		renderReport(w, logger, http.StatusOK, "report", page)
	}
}

// reportFundOr404 is the lookup both report routes open with: the fund the
// slug names, or a short 404 naming no fund and ok == false.
func reportFundOr404(w http.ResponseWriter, r *http.Request, q store.Querier, logger *slog.Logger) (store.Fund, bool) {
	fund, err := q.GetFundByReportSlug(r.Context(), chi.URLParam(r, "slug"))
	switch {
	case err == nil:
		return fund, true
	case errors.Is(err, sql.ErrNoRows):
		renderReport(w, logger, http.StatusNotFound, "notfound", reportPage{
			Title: reportText.NotFoundTitle,
			Style: reportStyle(),
			Text:  reportText,
		})
	default:
		logger.Error("report: looking up the slug", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
	return store.Fund{}, false
}

// assembleReportPage reads the filter, the month and the fund's emptiness,
// and builds the page from them.
func assembleReportPage(ctx context.Context, l *ledger.Ledger, q store.Querier, fundID int64, query url.Values, now time.Time) (reportPage, error) {
	purposes, err := q.ListPurposesByFund(ctx, fundID)
	if err != nil {
		return reportPage{}, err
	}
	members, err := q.ListMembersByFund(ctx, fundID)
	if err != nil {
		return reportPage{}, err
	}
	params, filter := readReportFilter(query, fundID, purposes, members)
	report, err := monthlyReportOrCurrent(ctx, l, params, now)
	if err != nil {
		return reportPage{}, err
	}
	empty, err := fundIsEmpty(ctx, q, fundID)
	if err != nil {
		return reportPage{}, err
	}
	report.Envelopes = envelopesForPurpose(report.Envelopes, params.PurposeID)
	page := buildReportPage(report, empty)
	page.Filter = filter
	// A dues filter that matches nobody leaves no rows, which must not read
	// as "nobody owes": ask the unfiltered question only then.
	page.HasDues = len(page.Dues) > 0
	if !page.HasDues && params.DuesStatus != "" {
		all, err := l.DuesStatusForPeriod(ctx, fundID, report.Month)
		if err != nil {
			return reportPage{}, err
		}
		page.HasDues = len(all) > 0
	}
	if page.PrevMonth != "" {
		page.PrevHref = monthHref(page.PrevMonth, filter.Active)
	}
	if page.NextMonth != "" {
		page.NextHref = monthHref(page.NextMonth, filter.Active)
	}
	return page, nil
}

// envelopesForPurpose narrows the month's envelopes to the purpose filter's:
// no filter keeps all; an envelope purpose keeps that one (none if not open
// that month); any other purpose (Kas Utama, a pass-through) keeps none,
// since no envelope is it. The ledger assembles the month unfiltered, so this
// only picks from it.
func envelopesForPurpose(envs []ledger.ReportEnvelope, purposeID *int64) []ledger.ReportEnvelope {
	if purposeID == nil {
		return envs
	}
	var out []ledger.ReportEnvelope
	for _, e := range envs {
		if e.PurposeID == *purposeID {
			out = append(out, e)
		}
	}
	return out
}

// monthHref steps to another month with the same filters, landing on the
// month nav.
func monthHref(month string, active url.Values) string {
	href := "?month=" + url.QueryEscape(month)
	if len(active) > 0 {
		href += "&" + active.Encode()
	}
	return href + "#months"
}

// fundIsEmpty is a fund with no transaction at all.
func fundIsEmpty(ctx context.Context, q store.Querier, fundID int64) (bool, error) {
	_, err := q.FirstTransactionDateByFund(ctx, fundID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

// readReportFilter turns the query string into ledger params and the form's
// options. A purpose or member not this fund's, and a direction that is not
// "in" or "out", is no filter: a stale or hand-edited URL shows the whole
// month, not an error page. Options keep the store's order (Kas Utama first,
// then by name; members by name), as every other list.
func readReportFilter(query url.Values, fundID int64, purposes []store.Purpose, members []store.Member) (ledger.ReportParams, reportFilter) {
	params := ledger.ReportParams{FundID: fundID, Month: query.Get("month")}
	filter := reportFilter{Active: url.Values{}}

	wantPurpose := query.Get("purpose")
	for _, p := range purposes {
		id := strconv.FormatInt(p.ID, 10)
		selected := id == wantPurpose
		if selected {
			params.PurposeID = &p.ID
			filter.Active.Set("purpose", id)
		}
		filter.Purposes = append(filter.Purposes, reportOption{Value: id, Label: p.Name, Selected: selected})
	}

	wantMember := query.Get("member")
	for _, m := range members {
		id := strconv.FormatInt(m.ID, 10)
		selected := id == wantMember
		if selected {
			params.MemberID = &m.ID
			filter.Active.Set("member", id)
		}
		filter.Members = append(filter.Members, reportOption{Value: id, Label: m.Name, Selected: selected})
	}

	dir := query.Get("dir")
	if dir == ledger.ReportDirectionIn || dir == ledger.ReportDirectionOut {
		params.Direction = dir
		filter.Active.Set("dir", dir)
	}
	filter.Directions = []reportOption{
		{Value: ledger.ReportDirectionIn, Label: reportText.DirectionIn, Selected: params.Direction == ledger.ReportDirectionIn},
		{Value: ledger.ReportDirectionOut, Label: reportText.DirectionOut, Selected: params.Direction == ledger.ReportDirectionOut},
	}

	wantDues := query.Get("dues")
	switch wantDues {
	case ledger.ReportDuesUnpaid, ledger.ReportDuesPartial, ledger.ReportDuesPaid:
		params.DuesStatus = wantDues
		filter.Active.Set("dues", wantDues)
	}
	filter.Dues = []reportOption{
		{Value: ledger.ReportDuesUnpaid, Label: reportText.DuesUnpaid, Selected: params.DuesStatus == ledger.ReportDuesUnpaid},
		{Value: ledger.ReportDuesPartial, Label: reportText.DuesPartial, Selected: params.DuesStatus == ledger.ReportDuesPartial},
		{Value: ledger.ReportDuesPaid, Label: reportText.DuesPaidStatus, Selected: params.DuesStatus == ledger.ReportDuesPaid},
	}
	return params, filter
}

// monthlyReportOrCurrent reads ?month= per ADR-035: a missing, malformed or
// out-of-range month is the current one, never an error page. Malformed is
// the ledger's ErrInvalidArgument; out-of-range assembles fine but names a
// month the selector does not offer, so it is re-read as the current month.
func monthlyReportOrCurrent(ctx context.Context, l *ledger.Ledger, params ledger.ReportParams, now time.Time) (ledger.Report, error) {
	params.Now = now
	report, err := l.MonthlyReport(ctx, params)
	if err == nil && slices.Contains(report.Months, report.Month) {
		return report, nil
	}
	if err != nil && !errors.Is(err, ledger.ErrInvalidArgument) {
		return ledger.Report{}, err
	}
	params.Month = ""
	return l.MonthlyReport(ctx, params)
}

// reportAsOf is the date line every figure answers to (ADR-037): a past
// month's last day, or today marked as the running month.
func reportAsOf(r ledger.Report) string {
	if r.Running {
		return reportText.AsOfRunning(reportText.longDate(r.AsOf))
	}
	return reportText.AsOf(reportText.longDate(r.AsOf))
}

// walkLines is the walk's lines in ADR-038's order. Saldo awal, Penyesuaian
// and Dipindah appear only when non-zero.
func walkLines(w ledger.ReportWalk) []reportTotal {
	var lines []reportTotal
	add := func(label string, v money.Amount, last bool) {
		lines = append(lines, reportTotal{Label: label, Value: money.FormatIDR(v), Last: last})
	}
	if w.Full {
		add(reportText.WalkStart(reportText.longDate(w.StartOn)), w.Start, false)
		if w.Openings != 0 {
			add(reportText.RowOpening, w.Openings, false)
		}
	}
	add(reportText.TotalIn, w.In, false)
	add(reportText.TotalOut, w.Out, false)
	if w.Full {
		if w.Adjustments != 0 {
			add(reportText.RowAdjustment, w.Adjustments, false)
		}
		if w.Moved != 0 {
			add(reportText.WalkMoved, w.Moved, false)
		}
		add(reportText.WalkEnd(reportText.longDate(w.EndOn)), w.End, true)
	}
	return lines
}

func buildReportPage(r ledger.Report, empty bool) reportPage {
	page := reportPage{
		Title:       reportText.Title(r.FundName),
		Style:       reportStyle(),
		Text:        reportText,
		FundName:    r.FundName,
		AsOf:        reportAsOf(r),
		Balance:     money.FormatIDR(r.Balance),
		BalanceSize: heroSizeClass(money.FormatIDR(r.Balance)),
		Empty:       empty,
	}

	switch rec := r.Reconciliation; {
	case rec == nil:
		page.Check = &reportCheck{Class: "never", Message: reportText.NeverChecked}
	case rec.Matched:
		page.Check = &reportCheck{Class: "matched", Message: reportText.Matched, When: reportText.LastChecked(reportText.longDate(rec.Date))}
	default:
		page.Check = &reportCheck{
			Class:   "discrepancy",
			Message: reportText.Discrepancy(money.FormatIDR(rec.Difference)),
			When:    reportText.LastChecked(reportText.longDate(rec.Date)),
		}
	}

	if r.OwedToMembers > 0 {
		page.Owed = money.FormatIDR(r.OwedToMembers)
	}

	for _, p := range r.PurposeBalances {
		page.Purposes = append(page.Purposes, reportPurpose{
			Name:     p.Name,
			Balance:  money.FormatIDR(p.Balance),
			Negative: p.Balance < 0,
		})
	}

	page.Month = r.Month
	page.Totals = walkLines(r.Walk)
	for _, row := range r.Rows {
		date := reportText.longDate(row.Date)
		switch {
		case row.Entry != nil:
			e := row.Entry
			sign := "+"
			if e.Direction == ledger.ReportDirectionOut {
				sign = "-"
			}
			page.Rows = append(page.Rows, reportRow{
				Date: date, Label: reportText.entryLabel(*e), Note: reportNote(e.Note), Purpose: e.PurposeName,
				Amount: sign + money.FormatIDR(e.Amount), Class: e.Direction, HasReceipt: e.HasReceipt, Reversed: e.Reversed,
			})
		case row.Move != nil:
			page.Rows = append(page.Rows, reportRow{
				Date: date, Label: reportText.moveLabel(*row.Move), Note: reportNote(row.Move.Note),
				Amount: money.FormatIDR(row.Move.Amount), Class: "move",
			})
		}
	}
	page.NoRows = len(page.Rows) == 0

	for _, d := range r.Dues {
		row := reportDuesRow{
			Name: d.MemberName, Tier: d.TierName, Owed: money.FormatIDR(d.Owed), Paid: money.FormatIDR(d.Paid),
			Class: string(d.Status), Status: reportText.duesStatus(d.Status),
		}
		// As the app does: a known end reads as a plain "Lunas" with the month on
		// its own line.
		if d.Status == ledger.DuesStatusPaidInAdvance && d.PaidThrough != "" {
			row.Class = string(ledger.DuesStatusPaid)
			row.Status = reportText.DuesPaidStatus
			row.PaidThrough = reportText.DuesPaidThrough(reportText.monthName(d.PaidThrough))
		}
		page.Dues = append(page.Dues, row)
	}

	page.Grid = buildReportGrid(r)

	for _, e := range r.Envelopes {
		env := reportEnvelope{
			Name: e.Name, Collected: money.FormatIDR(e.Collected),
			Status: reportText.EnvelopeOpen, Closed: e.ClosedOn != nil,
			NoneExpect: len(e.Expected) == 0,
		}
		if env.Closed {
			env.Status = reportText.EnvelopeClosed
		}
		if len(e.Recipients) > 0 {
			env.Recipients = reportText.EnvelopeRecipients(strings.Join(e.Recipients, ", "))
		}
		given := 0
		for _, p := range e.Expected {
			person := reportPerson{Name: p.MemberName, Class: string(p.State), Status: reportText.participationStatus(p.State)}
			if p.State != ledger.ParticipationBelum {
				given++
				person.Amount = money.FormatIDR(p.Amount)
			}
			env.Expected = append(env.Expected, person)
		}
		if len(e.Expected) > 0 {
			env.Given = reportText.EnvelopeGiven(given, len(e.Expected))
		}
		for _, u := range e.Unexpected {
			env.Others = append(env.Others, reportPerson{Name: u.MemberName, Amount: money.FormatIDR(u.Amount)})
		}
		page.Envelopes = append(page.Envelopes, env)
	}

	i := slices.Index(r.Months, r.Month)
	if i > 0 {
		page.PrevMonth = r.Months[i-1]
	}
	if i >= 0 && i < len(r.Months)-1 {
		page.NextMonth = r.Months[i+1]
	}
	// Newest first: the month a visitor most likely wants is at the top.
	for j := len(r.Months) - 1; j >= 0; j-- {
		m := r.Months[j]
		page.MonthOptions = append(page.MonthOptions, reportMonthOption{
			Value:    m,
			Label:    reportText.monthName(m),
			Selected: m == r.Month,
		})
	}

	return page
}

// setReportHeaders is ADR-035's request edge: not indexed, not followed, no
// referrer leaking the slug onward, not cached in between, and no cookie
// (this route never touches the session manager, mounted on /api alone).
func setReportHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

// reportCSP is the report's Content-Security-Policy, replacing the app's
// (securityHeaders). The page is one self-contained response: its only script
// and style are inline and carry this response's nonce, so nothing else (an
// injected tag, an attribute handler, a file from anywhere) runs or applies.
// img-src covers the browser's favicon request; form-action keeps the filter
// forms on this origin.
func reportCSP(nonce string) string {
	return "default-src 'none'; script-src 'nonce-" + nonce + "'; style-src 'nonce-" + nonce + "'; " +
		"img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
}

// renderReport executes into a buffer first, so a template error is a clean
// 500 rather than half a page under a 200 already sent. Each render draws a
// fresh nonce: the page is no-store, so no copy outlives the header naming
// its nonce.
func renderReport(w http.ResponseWriter, logger *slog.Logger, status int, name string, page reportPage) {
	page.Nonce = rand.Text()
	var buf bytes.Buffer
	if err := reportTemplates.ExecuteTemplate(&buf, name, page); err != nil {
		logger.Error("report: rendering", "template", name, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", reportCSP(page.Nonce))
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// heroSizeClass names the report hero's type step for a formatted figure, the
// same steps as Beranda's hero, by length in characters.
func heroSizeClass(figure string) string {
	switch n := len([]rune(figure)); {
	case n <= 15:
		return ""
	case n <= 17:
		return "s32"
	case n <= 20:
		return "s28"
	default:
		return "s24"
	}
}
