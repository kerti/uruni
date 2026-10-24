package http

// The monthly PDF (ADR-035, #378): GET /report/{slug}/pdf?month=YYYY-MM, the
// whole month with no filters, to print or drop in the group chat. A second
// renderer over the page's shaping: buildReportPage has turned the ledger's
// Report into display text (money.FormatIDR rupiah, Indonesian dates and
// labels), so nothing is computed or queried here and a number cannot read
// one way on the page and another in the file.
//
// Fonts. fpdf's UTF-8 path embeds a TrueType font, so "Rp", the non-breaking
// space and Indonesian text are selectable, searchable text. fonts/ holds
// Noto Sans Regular and Bold (SIL OFL 1.1, licence beside them), subset to
// Basic Latin, Latin-1 and Latin Extended-A plus the copy's punctuation
// (dashes, quotes, bullet, ellipsis, minus): about 23 KB each instead of 600
// KB. A character outside the set (a name in another script) has no glyph and
// prints as a blank box. The one gap in the app's own copy is the arrow of
// "Dipindah: A -> B": no Noto Sans build has it (arrows live in Noto Sans
// Symbols, and fpdf cannot fall back mid-line), so pdfSafe writes "->" here;
// the page keeps the real one.

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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

// reportPDFHandler serves GET /report/{slug}/pdf with the page's edge: the
// same headers, the same 404 for an unknown slug, a missing or malformed month
// is the current one.
func reportPDFHandler(l *ledger.Ledger, q store.Querier, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setReportHeaders(w)

		logger := loggerFrom(r.Context(), logger)
		fund, ok := reportFundOr404(w, r, q, logger)
		if !ok {
			return
		}

		body, month, err := renderReportPDF(r.Context(), l, fund.ID, r.URL.Query().Get("month"))
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

// renderReportPDF assembles the month as the page does (same bad-month
// fallback, same shaping) and draws it. It returns the month it rendered, for
// the filename.
func renderReportPDF(ctx context.Context, l *ledger.Ledger, fundID int64, month string) ([]byte, string, error) {
	report, err := monthlyReportOrCurrent(ctx, l, ledger.ReportParams{FundID: fundID, Month: month})
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

// pdfStandIns are the copy's characters the embedded font lacks, with the
// stand-in the PDF prints.
var pdfStandIns = strings.NewReplacer("\u2192", "->")

// pdfSafe drops what fpdf cannot carry: its UTF-8 font map ends at U+FFFF, so
// a rune above it (an emoji in a fund, member or envelope name) panics inside
// Output. The emoji presentation selector and zero-width joiner go too, being
// remains of such an emoji that the font draws neither of. Where something was
// dropped, the spaces around it close up ("Kas RT 05 X - Laporan" reads "Kas
// RT 05 - Laporan"), none left at either end. Text with nothing to drop comes
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

// pdfFontFiles are the two embedded TrueType files, read once per process
// (#520) and shared by every PDF. What this saves is the per-request copy out
// of embed.FS, not the parsing: fpdf parses a font inside AddUTF8FontFromBytes
// into an unexported, per-document structure it also mutates while drawing
// (which glyphs were used), and offers no way to hand a parsed font to a second
// document. So each PDF still parses its two (small, subset) fonts; the bytes it
// parses are shared and only read, which is safe across concurrent requests.
// pdfFontReads counts the reads, for the test that proves it is one.
var pdfFontFiles = sync.OnceValues(func() (fonts [2][]byte, err error) {
	pdfFontReads.Add(1)
	for i, name := range []string{"fonts/NotoSans-Regular.ttf", "fonts/NotoSans-Bold.ttf"} {
		if fonts[i], err = reportFonts.ReadFile(name); err != nil {
			return [2][]byte{}, err
		}
	}
	return fonts, nil
})

var pdfFontReads atomic.Int32

func drawReportPDF(page reportPage) ([]byte, error) {
	fonts, err := pdfFontFiles()
	if err != nil {
		return nil, err
	}
	regular, bold := fonts[0], fonts[1]

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
	d.duesGrid(page)
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

func (d *pdfDoc) section(title string, ownPage bool) {
	if ownPage {
		d.AddPage()
	} else {
		d.need(24)
		d.SetY(d.GetY() + pdfSectionGap)
	}
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
	h := pdfLineHeight(pdfSmallSize)
	// The mark leads the line, centred on it, with clear space after it.
	d.mark(pdfMarginX, y+(h-pdfMarkSize)/2, pdfMarkSize)
	textX := pdfMarginX + pdfMarkSize + 2.5
	d.face(false, pdfSmallSize)
	d.ink(left.color)
	d.SetXY(textX, y)
	d.CellFormat(pdfContentW*0.7-(textX-pdfMarginX), h, left.text, "", 0, "L", false, 0, "")
	d.SetXY(pdfMarginX+pdfContentW*0.7, y)
	d.CellFormat(pdfContentW*0.3, h, right.text, "", 0, "R", false, 0, "")
}

// pdfMarkSize is the mark's height in the footer, in millimetres: above the
// 16px minimum Design-System.md sets, and no taller than the line.
const pdfMarkSize = 4.5

// mark draws Uruni's mark, the U-vessel with its Sage dot (Design-System.md,
// "Logo & mark"), as vectors from web/public/favicon.svg's geometry: the
// primary colourway for a light page, without the app icon's tile or the
// wordmark (Plus Jakarta Sans; the PDF embeds only Noto Sans). size is its
// height; (x, y) its top-left corner.
//
// favicon.svg, in its 64-unit box: the vessel is a 6-wide round-capped stroke
// down from (21,18) and (43,18) to y=31, joined by the lower half of a circle
// of radius 11 about (32,31); the dot is a radius-5 circle at (32,30). Stroke
// included, the vessel spans x 18..46 and y 15..45 - 28 by 30 units.
func (d *pdfDoc) mark(x, y, size float64) {
	u := size / 30
	px := func(ux float64) float64 { return x + (ux-18)*u }
	py := func(uy float64) float64 { return y + (uy-15)*u }

	d.rule("primary")
	d.SetLineWidth(6 * u)
	d.SetLineCapStyle("round")
	d.Line(px(21), py(18), px(21), py(31))
	d.Line(px(43), py(18), px(43), py(31))
	d.Arc(px(32), py(31), 11*u, 11*u, 0, 180, 360, "D")
	d.SetLineCapStyle("butt")
	d.SetLineWidth(pdfRuleWidthMM)

	d.fill("accent")
	d.Circle(px(32), py(30), 5*u, "F")
}

// --- sections -------------------------------------------------------------

// header is the page's: fund and month, the balance, the latest cek kas, the
// per-pos balances and what is owed to members, every figure as of the
// month's last day or today for the running month (ADR-037), as the date line
// under the title says.
func (d *pdfDoc) header(p reportPage) {
	d.text(pdfLine{text: p.FundName, bold: true, size: 22, color: "primary"}, pdfContentW, "L")
	d.text(pdfLine{text: reportText.PDFStatement(reportText.monthName(p.Month)), bold: true, size: 13, color: "foreground"}, pdfContentW, "L")
	d.text(pdfLine{text: p.AsOf, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
	d.section(reportText.PDFSummary, false)
	d.SetY(d.GetY() + 2)

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

	// What the fund owes members who fronted an expense, beneath the pos it
	// already sits inside; absent when nothing is owed, as on the page (#406).
	if p.Owed != "" {
		d.SetY(d.GetY() + 3)
		d.row([]pdfCell{
			{w: pdfContentW * 0.6, align: "L", lines: []pdfLine{{text: reportText.OwedLabel, size: pdfBodySize, color: "foreground"}}},
			{w: pdfContentW * 0.4, align: "R", lines: []pdfLine{{text: p.Owed, size: pdfBodySize, color: "foreground"}}},
		}, true)
	}
}

func (d *pdfDoc) transactions(p reportPage) {
	d.section(reportText.TransactionsLabel, true)
	for _, t := range p.Totals {
		color := "foreground"
		switch {
		case t.Last:
			color = "primary"
		case t.Label == reportText.TotalIn:
			color = "success"
		}
		d.pair(t.Label, t.Value, t.Last, color)
	}

	if len(p.Rows) == 0 {
		d.SetY(d.GetY() + 3)
		d.text(pdfLine{text: reportText.PDFNoRows, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
		return
	}
	// amountW fits "+Rp 999.999.999.999" (33mm in bold 9.5pt, #540) with room
	// to spare: a figure that did not would wrap at its space.
	const dateW, amountW = 32.0, 40.0
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
		// Her note, line breaks kept, between the label and the meta line.
		for _, line := range strings.Split(r.Note, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				note.lines = append(note.lines, pdfLine{text: line, size: pdfSmallSize + 0.5, color: "foreground"})
			}
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
		if r.Reversed {
			note.lines = append(note.lines, pdfLine{text: reportText.RowReversed, size: pdfSmallSize, color: "muted-foreground"})
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
	d.section(reportText.DuesLabel, true)
	const amountW, statusW = 38.0, 54.0
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

// The grid's geometry, in millimetres: a fixed column, so a fund with fewer
// months gets a narrower grid rather than wider shapes. The columns are
// right-aligned - the last month always ends at the content edge, under the
// section's rule - and the name column is what they leave.
const (
	pdfGridColW      = 9.0
	pdfGridShapeR    = 1.7
	pdfGridRowPad    = 1.8
	pdfGridLegendGap = 6.0
)

// duesGrid is the twelve-month status grid (ADR-044) on its own page after the
// dues table: heading, range and caption, then a row per member, the column
// heads repeating on a new page and the legend kept with the last rows. Status
// is a drawn shape (filled, half-filled, hollow, a dash), never a glyph: the
// font subset has none, and a shape reads in a greyscale print.
func (d *pdfDoc) duesGrid(p reportPage) {
	g := p.Grid
	if g == nil {
		return
	}
	d.section(g.Heading, true)
	d.text(pdfLine{text: g.Range, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
	if g.Caption != "" {
		d.text(pdfLine{text: g.Caption, size: pdfBodySize, color: "muted-foreground"}, pdfContentW, "L")
	}
	d.SetY(d.GetY() + 3)

	nameW := pdfContentW - float64(len(g.Heads))*pdfGridColW
	colX := func(i int) float64 { return pdfMarginX + nameW + float64(i)*pdfGridColW }

	head := func() {
		const yearH, monthH = 3.6, 4.2
		top := d.GetY()
		d.face(false, 7.5)
		d.ink("muted-foreground")
		x := 0
		for _, y := range g.Years {
			d.SetXY(colX(x)+0.8, top)
			d.CellFormat(pdfGridColW*float64(y.Span)-0.8, yearH, y.Year, "", 0, "L", false, 0, "")
			x += y.Span
		}
		d.face(true, 7.5)
		for i, h := range g.Heads {
			d.SetXY(colX(i), top+yearH)
			d.CellFormat(pdfGridColW, monthH, h.Short, "", 0, "C", false, 0, "")
		}
		d.rule("input")
		bottom := top + yearH + monthH + 0.6
		d.Line(pdfMarginX, bottom, pdfMarginX+pdfContentW, bottom)
		d.SetXY(pdfMarginX, bottom)
	}
	d.repeat = head
	head()

	legendH := pdfGridLegendGap + 2*pdfGridShapeR + 2
	for i, r := range g.Rows {
		lines := d.wrap(pdfCell{w: nameW - pdfColumnGap, lines: []pdfLine{{text: r.Name, size: pdfBodySize, color: "foreground"}}})
		textH := 0.0
		for _, l := range lines {
			textH += pdfLineHeight(l.size)
		}
		h := max(textH, 2*pdfGridShapeR) + 2*pdfGridRowPad
		if i == len(g.Rows)-1 {
			// The last row brings the legend with it to a new page, never leaving
			// it alone there.
			d.need(h + legendH)
		} else {
			d.need(h)
		}
		top := d.GetY()
		y := top + pdfGridRowPad + (max(textH, 2*pdfGridShapeR)-textH)/2
		for _, l := range lines {
			d.face(l.bold, l.size)
			d.ink(l.color)
			d.SetXY(pdfMarginX, y)
			d.CellFormat(nameW-pdfColumnGap, pdfLineHeight(l.size), l.text, "", 0, "L", false, 0, "")
			y += pdfLineHeight(l.size)
		}
		cy := top + h/2
		for j, c := range r.Cells {
			d.gridShape(c.Class, colX(j)+pdfGridColW/2, cy, pdfGridShapeR)
		}
		d.rule("border")
		d.Line(pdfMarginX, top+h, pdfMarginX+pdfContentW, top+h)
		d.SetXY(pdfMarginX, top+h)
	}
	d.endTable()

	// The legend, on one line: shape, then its word.
	d.SetY(d.GetY() + pdfGridLegendGap)
	y := d.GetY() + pdfGridShapeR
	x := pdfMarginX
	d.face(false, pdfSmallSize)
	d.ink("muted-foreground")
	for _, c := range g.Legend {
		d.gridShape(c.Class, x+pdfGridShapeR, y, pdfGridShapeR)
		x += 2*pdfGridShapeR + 1.8
		w := d.GetStringWidth(c.Label)
		d.SetXY(x, y-pdfLineHeight(pdfSmallSize)/2)
		d.CellFormat(w+1, pdfLineHeight(pdfSmallSize), c.Label, "", 0, "L", false, 0, "")
		x += w + 7
	}
	d.SetXY(pdfMarginX, y+pdfGridShapeR+1)
}

// gridShape draws one cell's mark centred on (cx, cy): a filled disc for
// Lunas, a ring with its left half filled for Bayar sebagian, a bare ring for
// Belum bayar, and a short dash for no obligation. Tones as the page's.
func (d *pdfDoc) gridShape(class string, cx, cy, r float64) {
	d.SetLineWidth(0.4)
	defer d.SetLineWidth(pdfRuleWidthMM)
	switch class {
	case string(ledger.DuesCellPaid):
		d.fill("success")
		d.Circle(cx, cy, r, "F")
	case string(ledger.DuesCellPartial):
		d.fill("attention")
		d.rule("attention")
		// The left half-disc as a polygon: fpdf has no pie slice.
		const steps = 24
		pts := make([]fpdf.PointType, 0, steps+1)
		for i := 0; i <= steps; i++ {
			a := math.Pi/2 + math.Pi*float64(i)/steps
			pts = append(pts, fpdf.PointType{X: cx + r*math.Cos(a), Y: cy - r*math.Sin(a)})
		}
		d.Polygon(pts, "F")
		d.Circle(cx, cy, r, "D")
	case string(ledger.DuesCellUnpaid):
		d.rule("muted-foreground")
		d.Circle(cx, cy, r, "D")
	default:
		d.rule("muted-foreground")
		d.SetLineCapStyle("round")
		d.Line(cx-r*0.6, cy, cx+r*0.6, cy)
		d.SetLineCapStyle("butt")
	}
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
	d.section(reportText.EnvelopesLabel, true)
	const amountW, statusW = 38.0, 52.0
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
