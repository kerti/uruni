package http

// The monthly PDF (ADR-035, #378): GET /report/{slug}/pdf?month=YYYY-MM, the
// whole month with no filters, for the treasurer to print or drop in the group
// chat. It is a second renderer over the page's own shaping: buildReportPage
// has already turned the ledger's Report into display text - formatted rupiah
// (money.FormatIDR), Indonesian dates and labels - so nothing is computed or
// queried here, and a number cannot read one way on the page and another in
// the file.
//
// Fonts. fpdf's UTF-8 path embeds a TrueType font, so "Rp", the non-breaking
// space and Indonesian text come out as text a reader can select and search.
// fonts/ holds Noto Sans Regular and Bold (SIL OFL 1.1, licence beside them),
// subset to Basic Latin, Latin-1 and Latin Extended-A plus the handful of
// punctuation the copy uses (dashes, quotes, bullet, ellipsis, minus):
// about 23 KB each instead of the 600 KB full fonts. A character outside that
// set - a name in another script - has no glyph and prints as a blank box;
// everything the app's own copy and an Indonesian neighbour's name need is in,
// except the arrow of "Dipindah: A -> B": Noto Sans has none in any build
// (arrows live in Noto Sans Symbols, and fpdf cannot fall back to a second
// font mid-line), so pdfSafe writes it as "->" here; the page keeps the real
// one.

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"codeberg.org/go-pdf/fpdf"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

//go:embed fonts/NotoSans-Regular.ttf fonts/NotoSans-Bold.ttf fonts/OFL.txt
var reportFonts embed.FS

const (
	pdfFont = "NotoSans"

	pdfMarginX     = 18.0
	pdfMarginTop   = 16.0
	pdfPageBottom  = 277.0 // content stops here; the footer sits below
	pdfContentW    = 174.0 // A4 210mm less the two margins
	pdfColumnGap   = 3.0
	pdfSectionGap  = 7.0
	pdfBodySize    = 9.5
	pdfSmallSize   = 8.5
	pdfLineFactor  = 0.5 // line height in mm per point of type
	pdfRuleWidthMM = 0.2
)

// reportPDFHandler serves GET /report/{slug}/pdf. Same edge as the page: the
// same headers, the same 404 for an unknown slug, and a missing or malformed
// month is the current one. now is injectable for the same reason.
func reportPDFHandler(l *ledger.Ledger, q store.Querier, logger *slog.Logger, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)

		fund, ok := reportFundOr404(w, r, q, logger)
		if !ok {
			return
		}

		body, month, err := renderReportPDF(r.Context(), l, fund.ID, r.URL.Query().Get("month"), now())
		if err != nil {
			logger.Error("report: rendering the pdf", "fund_id", fund.ID, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		h := w.Header()
		h.Set("Content-Type", "application/pdf")
		// month is the ledger's validated "YYYY-MM", never the raw query value.
		h.Set("Content-Disposition", `attachment; filename="`+reportText.PDFFilename(month)+`"`)
		h.Set("Content-Length", strconv.Itoa(len(body)))
		//nolint:gosec // G705 is an HTML-injection rule; this body is application/pdf bytes drawn by fpdf, served as an attachment, never parsed as markup
		_, _ = w.Write(body)
	}
}

// renderReportPDF assembles the month exactly as the page does - same fallback
// for a bad month, same shaping - and draws it. It returns the month it
// actually rendered, for the filename.
func renderReportPDF(ctx context.Context, l *ledger.Ledger, fundID int64, month string, now time.Time) ([]byte, string, error) {
	report, err := monthlyReportOrCurrent(ctx, l, ledger.ReportParams{FundID: fundID, Month: month}, now)
	if err != nil {
		return nil, "", err
	}
	body, err := drawReportPDF(buildReportPage(report, false))
	return body, report.Month, err
}

// pdfLine is one line of text inside a cell; a cell stacks them.
type pdfLine struct {
	text  string
	bold  bool
	size  float64
	color string // a reportPalette name
}

type pdfCell struct {
	w     float64
	align string // "L" or "R"
	lines []pdfLine
}

// pdfDoc carries the page being drawn and the header to repeat when a table
// runs onto a new page.
type pdfDoc struct {
	*fpdf.Fpdf
	repeat func()
}

// pdfStandIns are the copy's characters the embedded font has no glyph for,
// each with the stand-in the PDF prints instead.
var pdfStandIns = strings.NewReplacer("\u2192", "->")

// pdfSafe drops what fpdf cannot carry: its UTF-8 font map ends at U+FFFF, so
// a rune above it - an emoji in a fund, member or envelope name - panics
// inside Output. The emoji presentation selector and the zero-width joiner go
// with it, being only the remains of such an emoji; the embedded font draws
// neither. Where something was dropped, the spaces that sat around it close
// up - "Kas RT 05 X - Laporan" reads "Kas RT 05 - Laporan", not with two
// spaces - and none is left at either end. Text with nothing to drop comes
// back untouched. Before any of that, a character the copy uses but the font
// lacks is written with one it has (pdfStandIns).
func pdfSafe(s string) string {
	s = pdfStandIns.Replace(s)
	dropped := false
	out := strings.Map(func(r rune) rune {
		if r > 0xFFFF || r == 0xFE0F || r == 0x200D {
			dropped = true
			return -1
		}
		return r
	}, s)
	if !dropped {
		return s
	}
	for strings.Contains(out, "  ") {
		out = strings.ReplaceAll(out, "  ", " ")
	}
	return strings.Trim(out, " ")
}

// CellFormat, SplitText and SetTitle are every way text reaches fpdf here;
// each passes it through pdfSafe first.
func (d *pdfDoc) CellFormat(w, h float64, txt, border string, ln int, align string, fill bool, link int, linkStr string) {
	d.Fpdf.CellFormat(w, h, pdfSafe(txt), border, ln, align, fill, link, linkStr)
}

func (d *pdfDoc) SplitText(txt string, w float64) []string {
	return d.Fpdf.SplitText(pdfSafe(txt), w)
}

func (d *pdfDoc) SetTitle(title string, isUTF8 bool) {
	d.Fpdf.SetTitle(pdfSafe(title), isUTF8)
}

func drawReportPDF(page reportPage) ([]byte, error) {
	regular, err := reportFonts.ReadFile("fonts/NotoSans-Regular.ttf")
	if err != nil {
		return nil, err
	}
	bold, err := reportFonts.ReadFile("fonts/NotoSans-Bold.ttf")
	if err != nil {
		return nil, err
	}

	d := &pdfDoc{Fpdf: fpdf.New("P", "mm", "A4", "")}
	d.AddUTF8FontFromBytes(pdfFont, "", regular)
	d.AddUTF8FontFromBytes(pdfFont, "B", bold)
	d.SetMargins(pdfMarginX, pdfMarginTop, pdfMarginX)
	d.SetCellMargin(0)
	d.SetAutoPageBreak(false, 0)
	d.SetLineWidth(pdfRuleWidthMM)
	d.SetTitle(reportText.PDFStatement(reportText.monthName(page.Month))+" - "+page.FundName, true)
	d.SetCreator("Uruni", true)
	d.SetLang("id")
	d.AliasNbPages("{nb}")
	d.SetFooterFunc(func() { d.footer(page.FundName) })

	d.AddPage()
	d.header(page)
	d.transactions(page)
	d.dues(page)
	d.envelopes(page)

	var buf bytes.Buffer
	if err := d.Output(&buf); err != nil {
		return nil, fmt.Errorf("writing the pdf: %w", err)
	}
	return buf.Bytes(), nil
}

// --- primitives -----------------------------------------------------------

func paletteRGB(name string) (int, int, int) {
	hex := strings.TrimPrefix(reportPalette[name], "#")
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil || len(hex) != 6 {
		return 0, 0, 0
	}
	return int(n >> 16 & 0xff), int(n >> 8 & 0xff), int(n & 0xff)
}

func (d *pdfDoc) ink(name string) {
	r, g, b := paletteRGB(name)
	d.SetTextColor(r, g, b)
}

func (d *pdfDoc) fill(name string) {
	r, g, b := paletteRGB(name)
	d.SetFillColor(r, g, b)
}

func (d *pdfDoc) rule(name string) {
	r, g, b := paletteRGB(name)
	d.SetDrawColor(r, g, b)
}

func (d *pdfDoc) face(bold bool, size float64) {
	style := ""
	if bold {
		style = "B"
	}
	d.SetFont(pdfFont, style, size)
}

func pdfLineHeight(size float64) float64 { return size * pdfLineFactor }

// text draws one line at the cursor's x and moves down.
func (d *pdfDoc) text(l pdfLine, w float64, align string) {
	d.face(l.bold, l.size)
	d.ink(l.color)
	d.SetX(pdfMarginX)
	d.CellFormat(w, pdfLineHeight(l.size), l.text, "", 1, align, false, 0, "")
}

// need starts a new page when h more millimetres will not fit, repeating the
// current table's header there.
func (d *pdfDoc) need(h float64) {
	if d.GetY()+h <= pdfPageBottom {
		return
	}
	d.AddPage()
	if d.repeat != nil {
		d.repeat()
	}
}

// wrap expands a cell's lines to the physical lines its width allows.
func (d *pdfDoc) wrap(c pdfCell) []pdfLine {
	var out []pdfLine
	for _, l := range c.lines {
		d.face(l.bold, l.size)
		for _, part := range d.SplitText(l.text, c.w) {
			out = append(out, pdfLine{text: part, bold: l.bold, size: l.size, color: l.color})
		}
	}
	return out
}

// row draws cells side by side, as tall as the tallest, with a hairline under
// it. A row never splits across pages: it moves whole to the next one.
func (d *pdfDoc) row(cells []pdfCell, ruled bool) {
	laid := make([][]pdfLine, len(cells))
	height := 0.0
	for i, c := range cells {
		laid[i] = d.wrap(c)
		h := 0.0
		for _, l := range laid[i] {
			h += pdfLineHeight(l.size)
		}
		height = max(height, h)
	}
	const pad = 1.6
	d.need(height + 2*pad)

	top := d.GetY() + pad
	x := pdfMarginX
	for i, c := range cells {
		y := top
		for _, l := range laid[i] {
			d.face(l.bold, l.size)
			d.ink(l.color)
			d.SetXY(x, y)
			d.CellFormat(c.w-pdfColumnGap, pdfLineHeight(l.size), l.text, "", 0, c.align, false, 0, "")
			y += pdfLineHeight(l.size)
		}
		x += c.w
	}
	bottom := top + height + pad
	if ruled {
		d.rule("border")
		d.Line(pdfMarginX, bottom, pdfMarginX+pdfContentW, bottom)
	}
	d.SetXY(pdfMarginX, bottom)
}

// tableHead draws the column titles and remembers them for the next page.
func (d *pdfDoc) tableHead(cells []pdfCell) {
	draw := func() {
		d.row(cells, false)
		d.rule("input")
		d.Line(pdfMarginX, d.GetY(), pdfMarginX+pdfContentW, d.GetY())
	}
	d.repeat = draw
	draw()
}

func (d *pdfDoc) endTable() { d.repeat = nil }

func headCell(w float64, align, title string) pdfCell {
	return pdfCell{w: w, align: align, lines: []pdfLine{{text: title, bold: true, size: pdfSmallSize, color: "muted-foreground"}}}
}

func (d *pdfDoc) section(title string) {
	d.need(24)
	d.SetY(d.GetY() + pdfSectionGap)
	d.text(pdfLine{text: title, bold: true, size: 12, color: "primary"}, pdfContentW, "L")
	d.rule("primary")
	d.Line(pdfMarginX, d.GetY()+0.6, pdfMarginX+pdfContentW, d.GetY()+0.6)
	d.SetY(d.GetY() + 2)
}

// pair is a label on the left and a value flush right, on one line.
func (d *pdfDoc) pair(label, value string, bold bool, valueColor string) {
	d.row([]pdfCell{
		{w: pdfContentW * 0.6, align: "L", lines: []pdfLine{{text: label, bold: bold, size: pdfBodySize + 0.5, color: "foreground"}}},
		{w: pdfContentW * 0.4, align: "R", lines: []pdfLine{{text: value, bold: true, size: pdfBodySize + 0.5, color: valueColor}}},
	}, true)
}

func (d *pdfDoc) footer(fundName string) {
	d.SetY(-14)
	d.rule("border")
	d.Line(pdfMarginX, d.GetY(), pdfMarginX+pdfContentW, d.GetY())
	d.SetY(d.GetY() + 2)
	left := pdfLine{text: reportText.PDFFooterSource(fundName), size: pdfSmallSize, color: "muted-foreground"}
	right := pdfLine{text: reportText.PDFPage(strconv.Itoa(d.PageNo()), "{nb}"), size: pdfSmallSize, color: "muted-foreground"}
	y := d.GetY()
	d.face(false, pdfSmallSize)
	d.ink(left.color)
	d.SetXY(pdfMarginX, y)
	d.CellFormat(pdfContentW*0.7, pdfLineHeight(pdfSmallSize), left.text, "", 0, "L", false, 0, "")
	d.SetXY(pdfMarginX+pdfContentW*0.7, y)
	d.CellFormat(pdfContentW*0.3, pdfLineHeight(pdfSmallSize), right.text, "", 0, "R", false, 0, "")
}

// --- sections -------------------------------------------------------------

// header is the page's own: fund and month, today's balance, the latest cek
// kas and the per-pos balances. The balance and the check are as of today
// whichever month follows (Report.AsOf), exactly as the page says.
func (d *pdfDoc) header(p reportPage) {
	d.text(pdfLine{text: p.FundName, bold: true, size: 22, color: "primary"}, pdfContentW, "L")
	d.text(pdfLine{text: reportText.PDFStatement(reportText.monthName(p.Month)), bold: true, size: 13, color: "foreground"}, pdfContentW, "L")
	d.text(pdfLine{text: p.AsOf, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
	d.SetY(d.GetY() + 5)

	// The balance, in a quiet panel: ink-light for print, Forest for the figure.
	y := d.GetY()
	d.fill("muted")
	d.RoundedRect(pdfMarginX, y, pdfContentW, 15, 2, "1234", "F")
	d.face(false, 10)
	d.ink("muted-foreground")
	d.SetXY(pdfMarginX+5, y+4.6)
	d.CellFormat(70, 6, reportText.BalanceLabel, "", 0, "L", false, 0, "")
	d.face(true, 16)
	d.ink("primary")
	d.SetXY(pdfMarginX+70, y+3.6)
	d.CellFormat(pdfContentW-75, 8, p.Balance, "", 0, "R", false, 0, "")
	d.SetY(y + 15 + 3)

	if c := p.Check; c != nil {
		color := map[string]string{"matched": "success", "discrepancy": "attention"}[c.Class]
		if color == "" {
			color = "muted-foreground"
		}
		line := c.Message
		if c.When != "" {
			line += " " + c.When + "."
		}
		d.text(pdfLine{text: line, size: pdfBodySize, color: color}, pdfContentW, "L")
	}

	if len(p.Purposes) > 0 {
		d.SetY(d.GetY() + 3)
		d.text(pdfLine{text: reportText.PurposesLabel, bold: true, size: pdfSmallSize, color: "muted-foreground"}, pdfContentW, "L")
		for _, pu := range p.Purposes {
			color := "foreground"
			if pu.Negative {
				color = "attention"
			}
			d.row([]pdfCell{
				{w: pdfContentW * 0.6, align: "L", lines: []pdfLine{{text: pu.Name, size: pdfBodySize, color: "foreground"}}},
				{w: pdfContentW * 0.4, align: "R", lines: []pdfLine{{text: pu.Balance, size: pdfBodySize, color: color}}},
			}, true)
		}
	}
}

func (d *pdfDoc) transactions(p reportPage) {
	d.section(reportText.TransactionsLabel)
	d.pair(reportText.TotalIn, p.Totals.In, false, "success")
	d.pair(reportText.TotalOut, p.Totals.Out, false, "foreground")
	d.pair(reportText.TotalNet, p.Totals.Net, true, "primary")

	if len(p.Rows) == 0 {
		d.SetY(d.GetY() + 3)
		d.text(pdfLine{text: reportText.PDFNoRows, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
		return
	}
	const dateW, amountW = 34.0, 36.0
	noteW := pdfContentW - dateW - amountW
	d.SetY(d.GetY() + 4)
	d.tableHead([]pdfCell{
		headCell(dateW, "L", reportText.PDFColDate),
		headCell(noteW, "L", reportText.PDFColNote),
		headCell(amountW, "R", reportText.PDFColAmount),
	})
	for _, r := range p.Rows {
		note := pdfCell{w: noteW, align: "L"}
		if r.Label != "" {
			note.lines = append(note.lines, pdfLine{text: r.Label, size: pdfBodySize, color: "foreground"})
		}
		var meta []string
		if r.Purpose != "" {
			meta = append(meta, r.Purpose)
		}
		if r.HasReceipt {
			meta = append(meta, reportText.HasReceipt)
		}
		if len(meta) > 0 {
			note.lines = append(note.lines, pdfLine{text: strings.Join(meta, " \u00b7 "), size: pdfSmallSize, color: "muted-foreground"})
		}
		if len(note.lines) == 0 {
			note.lines = []pdfLine{{text: "", size: pdfBodySize, color: "foreground"}}
		}
		color := map[string]string{"in": "success", "move": "muted-foreground"}[r.Class]
		if color == "" {
			color = "foreground"
		}
		d.row([]pdfCell{
			{w: dateW, align: "L", lines: []pdfLine{{text: r.Date, size: pdfSmallSize + 0.5, color: "muted-foreground"}}},
			note,
			{w: amountW, align: "R", lines: []pdfLine{{text: r.Amount, bold: true, size: pdfBodySize, color: color}}},
		}, true)
	}
	d.endTable()
}

func (d *pdfDoc) dues(p reportPage) {
	if len(p.Dues) == 0 {
		return
	}
	d.section(reportText.DuesLabel)
	const amountW, statusW = 30.0, 54.0
	nameW := pdfContentW - 2*amountW - statusW
	d.tableHead([]pdfCell{
		headCell(nameW, "L", reportText.PDFColMember),
		headCell(amountW, "R", reportText.DuesOwed),
		headCell(amountW, "R", reportText.DuesPaid),
		headCell(statusW, "L", reportText.PDFColStatus),
	})
	for _, r := range p.Dues {
		status := pdfCell{w: statusW, align: "L", lines: []pdfLine{{text: r.Status, bold: true, size: pdfBodySize, color: duesColor(r.Class)}}}
		if r.PaidThrough != "" {
			status.lines = append(status.lines, pdfLine{text: r.PaidThrough, size: pdfSmallSize, color: "muted-foreground"})
		}
		d.row([]pdfCell{
			{w: nameW, align: "L", lines: []pdfLine{
				{text: r.Name, size: pdfBodySize, color: "foreground"},
				{text: reportText.DuesTier + " " + r.Tier, size: pdfSmallSize, color: "muted-foreground"},
			}},
			{w: amountW, align: "R", lines: []pdfLine{{text: r.Owed, size: pdfBodySize, color: "foreground"}}},
			{w: amountW, align: "R", lines: []pdfLine{{text: r.Paid, size: pdfBodySize, color: "foreground"}}},
			status,
		}, true)
	}
	d.endTable()
}

// duesColor is the page's badge colouring: paid is green, partial terracotta,
// unpaid neutral - never alarm-red.
func duesColor(class string) string {
	switch class {
	case string(ledger.DuesStatusPaid), string(ledger.DuesStatusPaidInAdvance):
		return "success"
	case string(ledger.DuesStatusPartial):
		return "attention"
	default:
		return "muted-foreground"
	}
}

func participationColor(class string) string {
	switch class {
	case string(ledger.ParticipationSudah):
		return "success"
	case string(ledger.ParticipationKurang):
		return "attention"
	default:
		return "muted-foreground"
	}
}

func (d *pdfDoc) envelopes(p reportPage) {
	if len(p.Envelopes) == 0 {
		return
	}
	d.section(reportText.EnvelopesLabel)
	const amountW, statusW = 36.0, 56.0
	nameW := pdfContentW - amountW - statusW
	for _, e := range p.Envelopes {
		// The heading and its first rows stay together.
		d.need(34)
		d.SetY(d.GetY() + 3)
		statusColor := "success"
		if e.Closed {
			statusColor = "muted-foreground"
		}
		d.row([]pdfCell{
			{w: pdfContentW - 40, align: "L", lines: []pdfLine{{text: e.Name, bold: true, size: 11, color: "foreground"}}},
			{w: 40, align: "R", lines: []pdfLine{{text: e.Status, bold: true, size: pdfBodySize, color: statusColor}}},
		}, false)
		facts := reportText.EnvelopeCollected + ": " + e.Collected
		if e.Given != "" {
			facts += " \u00b7 " + e.Given
		}
		d.text(pdfLine{text: facts, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
		if e.Recipients != "" {
			d.text(pdfLine{text: e.Recipients, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
		}
		d.SetY(d.GetY() + 1)

		if e.NoneExpect {
			d.text(pdfLine{text: reportText.EnvelopeNoneExpect, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
		} else {
			d.tableHead([]pdfCell{
				headCell(nameW, "L", reportText.PDFColMember),
				headCell(statusW, "L", reportText.PDFColStatus),
				headCell(amountW, "R", reportText.PDFColAmount),
			})
			for _, pe := range e.Expected {
				d.row([]pdfCell{
					{w: nameW, align: "L", lines: []pdfLine{{text: pe.Name, size: pdfBodySize, color: "foreground"}}},
					{w: statusW, align: "L", lines: []pdfLine{{text: pe.Status, size: pdfBodySize, color: participationColor(pe.Class)}}},
					{w: amountW, align: "R", lines: []pdfLine{{text: pe.Amount, size: pdfBodySize, color: "foreground"}}},
				}, true)
			}
			d.endTable()
		}
		if len(e.Others) > 0 {
			d.need(20)
			d.SetY(d.GetY() + 3)
			d.text(pdfLine{text: reportText.EnvelopeOthers, bold: true, size: pdfSmallSize, color: "muted-foreground"}, pdfContentW, "L")
			for _, o := range e.Others {
				d.row([]pdfCell{
					{w: nameW + statusW, align: "L", lines: []pdfLine{{text: o.Name, size: pdfBodySize, color: "foreground"}}},
					{w: amountW, align: "R", lines: []pdfLine{{text: o.Amount, size: pdfBodySize, color: "foreground"}}},
				}, true)
			}
		}
	}
}
