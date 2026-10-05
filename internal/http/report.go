package http

// The public report (ADR-035, #373): GET /report/{slug}, server-rendered with
// html/template from ledger.MonthlyReport. No session, no resolveFund - the
// slug names its fund (ADR-030's carve-out), and is a read capability, never an
// authorization. This slice renders the header and the month selector; the
// transactions, dues and envelopes sections fill in beneath it in later
// slices, from the same Report.

import (
	"bytes"
	"context"
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
// already display text - formatted rupiah, Indonesian dates - so the template
// only places it, and nothing is computed there (ADR-035).
type reportPage struct {
	Title string
	Style template.CSS
	Text  reportCopy

	FundName string
	AsOf     string
	Balance  string
	Check    *reportCheck
	Purposes []reportPurpose

	// Owed is the total owed to members who fronted an expense, as of the
	// same day as Balance (ADR-037); "" when nothing is owed, which hides the
	// line (#406).
	Owed string

	PDFHref string

	PrevMonth    string
	NextMonth    string
	PrevHref     string
	NextHref     string
	MonthOptions []reportMonthOption

	// The transactions section (#374): the filter form, the totals for the
	// filtered set, and the rows. Filter is set by the handler, which alone
	// knows the fund's members and purposes.
	Filter reportFilter
	Totals reportTotals
	Rows   []reportRow
	Month  string
	NoRows bool

	// The dues section (#375). HasDues is whether any member owes for the
	// month at all, before the dues filter: it shows the section and the
	// filter, so a filter that matches nobody still reads as one.
	HasDues bool
	Dues    []reportDuesRow

	// The envelopes section (#338): every envelope the ledger listed for the
	// month, narrowed by the purpose filter. The section is hidden when empty.
	Envelopes []reportEnvelope

	// Empty is a fund with nothing recorded at all - not merely a quiet
	// month - which gets the warm line instead of a bare Rp 0.
	Empty bool
}

// reportCheck is the latest cek kas, as the home banner reads it: matched,
// a discrepancy in terracotta, or never counted (neutral, never green).
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

// reportTotals are the filtered set's money in, money out and net, already
// formatted. Moves and between-accounts transfers are in none of them.
type reportTotals struct {
	In  string
	Out string
	Net string
}

// reportRow is one line of the month. Class is "in", "out" or "move"; Label is
// "" for a plain row the treasurer recorded. Note is what she typed, under the
// label as Riwayat shows it (a settled claim's payout carries the claim's
// note; a move, the note both its legs carry), "" when there is none.
// HasReceipt renders a plain
// marker - nothing about the receipt itself reaches the page.
type reportRow struct {
	Date       string
	Label      string
	Note       string
	Purpose    string
	Amount     string
	Class      string
	HasReceipt bool
}

// reportNote is a row's note as the report prints it: trimmed, "" for none.
func reportNote(note *string) string {
	if note == nil {
		return ""
	}
	return strings.TrimSpace(*note)
}

// reportFilter is the GET form: every option of every select, with the
// current choice marked, so a filtered view reads back as what it is.
type reportFilter struct {
	Purposes   []reportOption
	Members    []reportOption
	Directions []reportOption
	Dues       []reportOption

	// Active is the filters in force, carried by the month steps and the
	// month form so changing month keeps what the visitor sifted for.
	Active url.Values
}

// reportDuesRow is one member's standing. Class is the ledger's status
// value, which the stylesheet colours; Status is the app's own wording.
type reportDuesRow struct {
	Name        string
	Tier        string
	Owed        string
	Paid        string
	Class       string
	Status      string
	PaidThrough string
}

// reportEnvelope is one envelope's <details>: the summary's facts, who it is
// for, and the participation table. Class on a person is the ledger's state
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
// pin the Jakarta month; production passes time.Now.
func reportHandler(l *ledger.Ledger, q store.Querier, logger *slog.Logger, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)

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
		// The link is to the month the page shows, which is the ledger's own
		// validated "YYYY-MM", never the raw query value.
		page.PDFHref = "/report/" + url.PathEscape(fund.ReportSlug) + "/pdf?month=" + url.QueryEscape(page.Month)
		renderReport(w, logger, http.StatusOK, "report", page)
	}
}

// reportFundOr404 is the lookup both report routes open with: the fund the
// slug names, or the page's own short 404 (naming no fund) and ok == false.
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
// no filter keeps them all; an envelope purpose keeps that one (none, if it
// was not open that month); any other purpose - Kas Utama, a pass-through -
// keeps none, since the filter asks for that purpose alone and no envelope is
// it. The ledger assembles the month unfiltered, so this only picks from it.
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
// month nav rather than the top of the page.
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
// options. A purpose or member that is not this fund's, and a direction that
// is neither "in" nor "out", is no filter at all: a stale or hand-edited URL
// shows the whole month rather than an error page. Options are ordered by
// name, never by id; Kas Utama leads the purposes, as everywhere else.
func readReportFilter(query url.Values, fundID int64, purposes []store.Purpose, members []store.Member) (ledger.ReportParams, reportFilter) {
	params := ledger.ReportParams{FundID: fundID, Month: query.Get("month")}
	filter := reportFilter{Active: url.Values{}}

	slices.SortFunc(purposes, func(a, b store.Purpose) int {
		if (a.Kind == "main") != (b.Kind == "main") {
			if a.Kind == "main" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
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

	slices.SortFunc(members, func(a, b store.Member) int { return strings.Compare(a.Name, b.Name) })
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

// monthlyReportOrCurrent reads ?month= as ADR-035 says to: a missing,
// malformed or out-of-range month is the current one, never an error page.
// A malformed month is the ledger's ErrInvalidArgument; an out-of-range one
// assembles fine but names a month the selector does not offer, so it is
// re-read as the current month too.
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

// reportAsOf is the date line every figure on the page answers to (ADR-037):
// a past month's last day, or today marked as the running month.
func reportAsOf(r ledger.Report) string {
	if r.Running {
		return reportText.AsOfRunning(reportText.longDate(r.AsOf))
	}
	return reportText.AsOf(reportText.longDate(r.AsOf))
}

func buildReportPage(r ledger.Report, empty bool) reportPage {
	page := reportPage{
		Title:    reportText.Title(r.FundName),
		Style:    reportStyle(),
		Text:     reportText,
		FundName: r.FundName,
		AsOf:     reportAsOf(r),
		Balance:  money.FormatIDR(r.Balance),
		Empty:    empty,
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
	page.Totals = reportTotals{
		In:  money.FormatIDR(r.Totals.In),
		Out: money.FormatIDR(r.Totals.Out),
		Net: money.FormatIDR(r.Totals.Net),
	}
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
				Amount: sign + money.FormatIDR(e.Amount), Class: e.Direction, HasReceipt: e.HasReceipt,
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
		// As the app does: a known end reads as a plain "Lunas" with the
		// month on its own line.
		if d.Status == ledger.DuesStatusPaidInAdvance && d.PaidThrough != "" {
			row.Class = string(ledger.DuesStatusPaid)
			row.Status = reportText.DuesPaidStatus
			row.PaidThrough = reportText.DuesPaidThrough(reportText.monthName(d.PaidThrough))
		}
		page.Dues = append(page.Dues, row)
	}

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
// referrer leaking the slug onward, not cached by anything in between, and no
// cookie - this route never touches the session manager, which is mounted on
// /api alone.
func setReportHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

// renderReport executes into a buffer first, so a template error is a clean
// 500 rather than half a page under a 200 that has already gone out.
func renderReport(w http.ResponseWriter, logger *slog.Logger, status int, name string, page reportPage) {
	var buf bytes.Buffer
	if err := reportTemplates.ExecuteTemplate(&buf, name, page); err != nil {
		logger.Error("report: rendering", "template", name, "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
