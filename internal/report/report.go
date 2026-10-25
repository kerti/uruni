// Package report builds the public report (ADR-035, #373) and draws it, as
// HTML (html/template) and as a PDF (fpdf), from ledger.MonthlyReport. The page
// is the header and month selector, then the balance, per-purpose balances,
// what is owed to members, transactions, dues and envelopes, all from the same
// Report. Every figure and label is display text by the time it reaches a
// renderer, so the page and the file cannot read one number two ways.
//
// It deliberately knows nothing of HTTP: no request, no header, no status.
// Resolving the slug, the Content-Security-Policy nonce, the caching headers
// and the error mapping belong to internal/http, which hands this package a
// query and an io.Writer.
package report

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

//go:embed report.html
var templateSource string

var templates = template.Must(template.New("report").Parse(templateSource))

// Page is everything the "report" template reads. Every string is
// already display text (formatted rupiah, Indonesian dates), so the template
// computes nothing (ADR-035).
type Page struct {
	Title string
	Style template.CSS
	Text  Copy

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
	Check       *Check
	Purposes    []Purpose

	// Owed is the total owed to members who fronted an expense, as of the same
	// day as Balance (ADR-037); "" hides the line (#406).
	Owed string

	PDFHref string

	PrevMonth    string
	NextMonth    string
	PrevHref     string
	NextHref     string
	MonthOptions []MonthOption

	// The transactions section (#374): the filter form, totals for the filtered
	// set, and rows. The handler sets Filter; it alone knows the fund's members
	// and purposes.
	Filter Filter
	Totals []Total
	Rows   []Row
	Month  string
	NoRows bool

	// The dues section (#375). HasDues is whether any member owes for the month
	// before the dues filter, so a filter matching nobody still shows the
	// section and filter.
	HasDues bool
	Dues    []DuesRow

	// Grid is the twelve-month dues grid (ADR-044) that opens the dues section;
	// nil when fewer than two months owe anything. It ignores every filter.
	Grid *Grid

	// The envelopes section (#338): every envelope the ledger listed for the
	// month, narrowed by the purpose filter; hidden when empty.
	Envelopes []Envelope

	// Empty is a fund with nothing recorded at all, not a quiet month: it gets
	// the warm line instead of a bare Rp 0.
	Empty bool
}

// Check is the latest cek kas as the home banner reads it: matched, a
// discrepancy in terracotta, or never counted (neutral, never green).
type Check struct {
	Class   string // "matched", "discrepancy" or "never"
	Message string
	When    string
}

// Purpose is one pos with its balance, as the header lists it.
type Purpose struct {
	Name     string
	Balance  string
	Negative bool
}

// Total is one line of the month's walk (ADR-038), formatted: label and
// figure. Last marks the closing balance, which both renderers emphasise. The
// whole fund, or one pos, reads Saldo <day> down to Saldo <day>; under a
// member or direction filter only Total masuk and Total keluar, since a
// balance has no meaning for a slice.
type Total struct {
	Label string
	Value string
	Last  bool
}

// Row is one line of the month. Class is "in", "out" or "move"; Label is
// "" for a plain row. Note is what she typed, under the label as Riwayat
// shows it (a settled claim's payout carries the claim's note; a move, the
// note both legs carry), "" for none. HasReceipt renders a plain marker;
// nothing about the receipt reaches the page.
type Row struct {
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

// noteText is a row's note as the report prints it: trimmed, "" for none.
func noteText(note *string) string {
	if note == nil {
		return ""
	}
	return strings.TrimSpace(*note)
}

// Filter is the GET form: every option of every select, the current
// choice marked, so a filtered view reads back as what it is.
type Filter struct {
	Purposes   []Option
	Members    []Option
	Directions []Option
	Dues       []Option

	// Active is the filters in force, carried by the month steps and month form
	// so changing month keeps them.
	Active url.Values
}

// DuesRow is one member's standing. Class is the ledger's status value,
// which the stylesheet colours; Status is the app's wording.
type DuesRow struct {
	Name        string
	Tier        string
	Owed        string
	Paid        string
	Class       string
	Status      string
	PaidThrough string
}

// Grid is the twelve-month dues grid, already display text: the page and
// the PDF draw the same cells. Status only; no count, total, amount or order
// beyond the ledger's (by name).
type Grid struct {
	Heading string
	Range   string // "Oktober 2025 - September 2026"
	Caption string // "Status per 24 Oktober 2026" on a past month; "" on the running one
	Years   []GridYear
	Heads   []GridHead
	Rows    []GridRow
	Legend  []GridCell
}

// GridYear is the year above its first column and each January.
type GridYear struct {
	Year string
	Span int
}

// GridHead is a column head: the short name drawn, the full month read
// aloud. Start marks the first column of a year.
type GridHead struct {
	Short string
	Full  string
	Year  string
	Start bool
}

// GridRow is one member of the dues grid with a cell per month.
type GridRow struct {
	Name  string
	Cells []GridCell
}

// GridCell is one shape. Class is the ledger's cell value ("paid",
// "partial", "unpaid", "none"), which picks the shape and the tone; Label is
// the month and status as text for assistive technology (the legend's is the
// bare word). Start marks the first column of a year.
type GridCell struct {
	Class string
	Label string
	Start bool
}

// buildGrid shapes the ledger's grid, nil when fewer than two months owe
// anything: a one-column grid repeats the table.
func buildGrid(r ledger.Report) *Grid {
	g := r.DuesGrid
	if len(g.Periods) < 2 {
		return nil
	}
	out := &Grid{
		Heading: Text.GridHeading,
		Range:   Text.GridRange(Text.monthName(g.Periods[0]), Text.monthName(g.Periods[len(g.Periods)-1])),
	}
	if !r.Running {
		out.Caption = Text.GridAsOf(Text.longDate(r.Today))
	}
	fulls := make([]string, len(g.Periods))
	starts := make([]bool, len(g.Periods))
	for i, period := range g.Periods {
		fulls[i] = Text.monthName(period)
		year, month := period[:4], period[5:]
		starts[i] = i == 0 || month == "01"
		if starts[i] {
			out.Years = append(out.Years, GridYear{Year: year, Span: 0})
		}
		out.Years[len(out.Years)-1].Span++
		m, _ := strconv.Atoi(month)
		out.Heads = append(out.Heads, GridHead{Short: Text.MonthHeads[m-1], Full: fulls[i], Year: year, Start: starts[i]})
	}
	for _, row := range g.Rows {
		cells := make([]GridCell, len(row.Cells))
		for i, c := range row.Cells {
			cells[i] = GridCell{Class: string(c), Label: Text.GridCellLabel(fulls[i], Text.gridWord(c)), Start: starts[i]}
		}
		out.Rows = append(out.Rows, GridRow{Name: row.Name, Cells: cells})
	}
	for _, c := range []ledger.DuesGridCell{ledger.DuesCellPaid, ledger.DuesCellPartial, ledger.DuesCellUnpaid, ledger.DuesCellNone} {
		out.Legend = append(out.Legend, GridCell{Class: string(c), Label: Text.gridWord(c)})
	}
	return out
}

// Envelope is one envelope's <details>: the summary's facts, who it is
// for, and the participation table. A person's Class is the ledger's state
// ("sudah", "belum", "kurang"), which the stylesheet colours.
type Envelope struct {
	Name       string
	Collected  string
	Given      string // "8 dari 12 sudah menyumbang"; "" when nobody is expected
	Status     string
	Closed     bool
	Recipients string // "Untuk: A, B"; "" when none
	Expected   []Person
	Others     []Person
	NoneExpect bool
}

// Person is one member in an envelope, with what they gave and their participation.
type Person struct {
	Name   string
	Amount string // "" when the member has given nothing
	Class  string
	Status string
}

// Option is one choice in a filter select.
type Option struct {
	Value    string
	Label    string
	Selected bool
}

// MonthOption is one month in the selector.
type MonthOption struct {
	Value    string
	Label    string
	Selected bool
}

// Build reads the filter from query (the report URL's parameters), the month
// and the fund's emptiness, and builds the page from them. An unknown filter
// value or a bad month is no filter or the current month, never an error. The
// running month is the ledger clock's, read in the fund's zone.
func Build(ctx context.Context, l *ledger.Ledger, q store.Querier, fundID int64, query url.Values) (Page, error) {
	purposes, err := q.ListPurposesByFund(ctx, fundID)
	if err != nil {
		return Page{}, err
	}
	members, err := q.ListMembersByFund(ctx, fundID)
	if err != nil {
		return Page{}, err
	}
	params, filter := readFilter(query, fundID, purposes, members)
	report, err := monthlyOrCurrent(ctx, l, params)
	if err != nil {
		return Page{}, err
	}
	empty, err := fundIsEmpty(ctx, q, fundID)
	if err != nil {
		return Page{}, err
	}
	report.Envelopes = envelopesForPurpose(report.Envelopes, params.PurposeID)
	page := NewPage(report, empty)
	page.Filter = filter
	// A dues filter that matches nobody leaves no rows, which must not read
	// as "nobody owes": ask the unfiltered question only then.
	page.HasDues = len(page.Dues) > 0
	if !page.HasDues && params.DuesStatus != "" {
		all, err := l.DuesStatusForPeriod(ctx, fundID, report.Month)
		if err != nil {
			return Page{}, err
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

// readFilter turns the query string into ledger params and the form's
// options. A purpose or member not this fund's, and a direction that is not
// "in" or "out", is no filter: a stale or hand-edited URL shows the whole
// month, not an error page. Options keep the store's order (Kas Utama first,
// then by name; members by name), as every other list.
func readFilter(query url.Values, fundID int64, purposes []store.Purpose, members []store.Member) (ledger.ReportParams, Filter) {
	params := ledger.ReportParams{FundID: fundID, Month: query.Get("month")}
	filter := Filter{Active: url.Values{}}

	wantPurpose := query.Get("purpose")
	for _, p := range purposes {
		id := strconv.FormatInt(p.ID, 10)
		selected := id == wantPurpose
		if selected {
			params.PurposeID = &p.ID
			filter.Active.Set("purpose", id)
		}
		filter.Purposes = append(filter.Purposes, Option{Value: id, Label: p.Name, Selected: selected})
	}

	wantMember := query.Get("member")
	for _, m := range members {
		id := strconv.FormatInt(m.ID, 10)
		selected := id == wantMember
		if selected {
			params.MemberID = &m.ID
			filter.Active.Set("member", id)
		}
		filter.Members = append(filter.Members, Option{Value: id, Label: m.Name, Selected: selected})
	}

	dir := query.Get("dir")
	if dir == string(ledger.ReportDirectionIn) || dir == string(ledger.ReportDirectionOut) {
		params.Direction = ledger.Direction(dir)
		filter.Active.Set("dir", dir)
	}
	filter.Directions = []Option{
		{Value: string(ledger.ReportDirectionIn), Label: Text.DirectionIn, Selected: params.Direction == ledger.ReportDirectionIn},
		{Value: string(ledger.ReportDirectionOut), Label: Text.DirectionOut, Selected: params.Direction == ledger.ReportDirectionOut},
	}

	wantDues := query.Get("dues")
	switch wantDues {
	case ledger.ReportDuesUnpaid, ledger.ReportDuesPartial, ledger.ReportDuesPaid:
		params.DuesStatus = wantDues
		filter.Active.Set("dues", wantDues)
	}
	filter.Dues = []Option{
		{Value: ledger.ReportDuesUnpaid, Label: Text.DuesUnpaid, Selected: params.DuesStatus == ledger.ReportDuesUnpaid},
		{Value: ledger.ReportDuesPartial, Label: Text.DuesPartial, Selected: params.DuesStatus == ledger.ReportDuesPartial},
		{Value: ledger.ReportDuesPaid, Label: Text.DuesPaidStatus, Selected: params.DuesStatus == ledger.ReportDuesPaid},
	}
	return params, filter
}

// monthlyOrCurrent reads ?month= per ADR-035: a missing, malformed or
// out-of-range month is the current one, never an error page. Malformed is
// the ledger's ErrInvalidArgument; out-of-range assembles fine but names a
// month the selector does not offer, so it is re-read as the current month.
func monthlyOrCurrent(ctx context.Context, l *ledger.Ledger, params ledger.ReportParams) (ledger.Report, error) {
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

// asOf is the date line every figure answers to (ADR-037): a past
// month's last day, or today marked as the running month.
func asOf(r ledger.Report) string {
	if r.Running {
		return Text.AsOfRunning(Text.longDate(r.AsOf))
	}
	return Text.AsOf(Text.longDate(r.AsOf))
}

// walkLines is the walk's lines in ADR-038's order. Saldo awal, Penyesuaian
// and Dipindah appear only when non-zero.
func walkLines(w ledger.ReportWalk) []Total {
	var lines []Total
	add := func(label string, v money.Amount, last bool) {
		lines = append(lines, Total{Label: label, Value: money.FormatIDR(v), Last: last})
	}
	if w.Full {
		add(Text.WalkStart(Text.longDate(w.StartOn)), w.Start, false)
		if w.Openings != 0 {
			add(Text.RowOpening, w.Openings, false)
		}
	}
	add(Text.TotalIn, w.In, false)
	add(Text.TotalOut, w.Out, false)
	if w.Full {
		if w.Adjustments != 0 {
			add(Text.RowAdjustment, w.Adjustments, false)
		}
		if w.Moved != 0 {
			add(Text.WalkMoved, w.Moved, false)
		}
		add(Text.WalkEnd(Text.longDate(w.EndOn)), w.End, true)
	}
	return lines
}

// NewPage shapes a ledger.Report into the page's display text: formatted
// rupiah, Indonesian dates and labels. empty says the fund holds no
// transaction at all. It reads nothing and computes no figure the ledger did
// not.
func NewPage(r ledger.Report, empty bool) Page {
	page := Page{
		Title:       Text.Title(r.FundName),
		Style:       style(),
		Text:        Text,
		FundName:    r.FundName,
		AsOf:        asOf(r),
		Balance:     money.FormatIDR(r.Balance),
		BalanceSize: heroSizeClass(money.FormatIDR(r.Balance)),
		Empty:       empty,
	}

	switch rec := r.Reconciliation; {
	case rec == nil:
		page.Check = &Check{Class: "never", Message: Text.NeverChecked}
	case rec.Matched:
		page.Check = &Check{Class: "matched", Message: Text.Matched, When: Text.LastChecked(Text.longDate(rec.Date))}
	default:
		page.Check = &Check{
			Class:   "discrepancy",
			Message: Text.Discrepancy(money.FormatIDR(rec.Difference)),
			When:    Text.LastChecked(Text.longDate(rec.Date)),
		}
	}

	if r.OwedToMembers > 0 {
		page.Owed = money.FormatIDR(r.OwedToMembers)
	}

	for _, p := range r.PurposeBalances {
		page.Purposes = append(page.Purposes, Purpose{
			Name:     p.Name,
			Balance:  money.FormatIDR(p.Balance),
			Negative: p.Balance < 0,
		})
	}

	page.Month = r.Month
	page.Totals = walkLines(r.Walk)
	for _, row := range r.Rows {
		date := Text.longDate(row.Date)
		switch {
		case row.Entry != nil:
			e := row.Entry
			sign := "+"
			if e.Direction == ledger.ReportDirectionOut {
				sign = "-"
			}
			page.Rows = append(page.Rows, Row{
				Date: date, Label: Text.entryLabel(*e), Note: noteText(e.Note), Purpose: e.PurposeName,
				Amount: sign + money.FormatIDR(e.Amount), Class: string(e.Direction), HasReceipt: e.HasReceipt, Reversed: e.Reversed,
			})
		case row.Move != nil:
			page.Rows = append(page.Rows, Row{
				Date: date, Label: Text.moveLabel(*row.Move), Note: noteText(row.Move.Note),
				Amount: money.FormatIDR(row.Move.Amount), Class: "move",
			})
		}
	}
	page.NoRows = len(page.Rows) == 0

	for _, d := range r.Dues {
		row := DuesRow{
			Name: d.MemberName, Tier: d.TierName, Owed: money.FormatIDR(d.Owed), Paid: money.FormatIDR(d.Paid),
			Class: string(d.Status), Status: Text.duesStatus(d.Status),
		}
		// As the app does: a known end reads as a plain "Lunas" with the month on
		// its own line.
		if d.Status == ledger.DuesStatusPaidInAdvance && d.PaidThrough != "" {
			row.Class = string(ledger.DuesStatusPaid)
			row.Status = Text.DuesPaidStatus
			row.PaidThrough = Text.DuesPaidThrough(Text.monthName(d.PaidThrough))
		}
		page.Dues = append(page.Dues, row)
	}

	page.Grid = buildGrid(r)

	for _, e := range r.Envelopes {
		env := Envelope{
			Name: e.Name, Collected: money.FormatIDR(e.Collected),
			Status: Text.EnvelopeOpen, Closed: e.ClosedOn != nil,
			NoneExpect: len(e.Expected) == 0,
		}
		if env.Closed {
			env.Status = Text.EnvelopeClosed
		}
		if len(e.Recipients) > 0 {
			env.Recipients = Text.EnvelopeRecipients(strings.Join(e.Recipients, ", "))
		}
		given := 0
		for _, p := range e.Expected {
			person := Person{Name: p.MemberName, Class: string(p.State), Status: Text.participationStatus(p.State)}
			if p.State != ledger.ParticipationBelum {
				given++
				person.Amount = money.FormatIDR(p.Amount)
			}
			env.Expected = append(env.Expected, person)
		}
		if len(e.Expected) > 0 {
			env.Given = Text.EnvelopeGiven(given, len(e.Expected))
		}
		for _, u := range e.Unexpected {
			env.Others = append(env.Others, Person{Name: u.MemberName, Amount: money.FormatIDR(u.Amount)})
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
		page.MonthOptions = append(page.MonthOptions, MonthOption{
			Value:    m,
			Label:    Text.monthName(m),
			Selected: m == r.Month,
		})
	}

	return page
}

// RenderHTML writes the report page under nonce, which the response's
// Content-Security-Policy names: the page's one script and style carry it. It
// executes into a buffer first, so a template error leaves w untouched rather
// than holding half a page.
func RenderHTML(w io.Writer, page Page, nonce string) error {
	return render(w, "report", page, nonce)
}

// RenderNotFound writes the calm page for a slug that names no fund. It names
// no fund and offers nothing to follow.
func RenderNotFound(w io.Writer, nonce string) error {
	return render(w, "notfound", Page{Title: Text.NotFoundTitle, Style: style(), Text: Text}, nonce)
}

// RenderRateLimited writes the page a throttled client reads in place of the
// report, in the app's words.
func RenderRateLimited(w io.Writer, nonce string) error {
	return render(w, "ratelimited", Page{Title: Text.RateLimitedTitle, Style: style(), Text: Text}, nonce)
}

func render(w io.Writer, name string, page Page, nonce string) error {
	page.Nonce = nonce
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, page); err != nil {
		return fmt.Errorf("rendering the %s template: %w", name, err)
	}
	_, err := buf.WriteTo(w)
	return err
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
