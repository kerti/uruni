package http

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// pdfStreams are the page content streams of a PDF fpdf wrote: every
// "stream ... endstream" body, inflated when it is zlib data (fpdf compresses
// by default) and raw otherwise. fpdf frames a stream with a bare "\n" on
// each side; matching an optional "\r" too would eat a compressed stream's
// last byte whenever that byte is 0x0D.
var pdfStreamRE = regexp.MustCompile(`(?s)stream\n(.*?)\nendstream`)

// pdfPages is pdfText split by page: fpdf writes one content stream per page,
// in order, and the font streams it also writes carry no text runs.
func pdfPages(t *testing.T, pdf []byte) [][]string {
	t.Helper()
	var pages [][]string
	for _, m := range pdfStreamRE.FindAllSubmatch(pdf, -1) {
		body := m[1]
		if zr, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
			inflated, err := io.ReadAll(zr)
			if err != nil {
				t.Fatalf("inflating a content stream: %v", err)
			}
			body = inflated
		}
		if runs := tjRuns(body); len(runs) > 0 {
			pages = append(pages, runs)
		}
	}
	return pages
}

// pdfText is the text a reader would see in the file, one string per drawn
// text run, in drawing order. It reads only what fpdf's UTF-8 path emits:
// "(<UTF-16BE, escaped>) Tj" inside the content streams. That is stdlib-only
// on purpose - a PDF-parsing dependency for one assertion helper is not worth
// its weight - and it is as strict as it needs to be: if the font were not
// embedded as Unicode text, the amounts below would not come back out.
func pdfText(t *testing.T, pdf []byte) []string {
	t.Helper()
	var runs []string
	for _, m := range pdfStreamRE.FindAllSubmatch(pdf, -1) {
		body := m[1]
		if zr, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
			inflated, err := io.ReadAll(zr)
			if err != nil {
				t.Fatalf("inflating a content stream: %v", err)
			}
			body = inflated
		}
		runs = append(runs, tjRuns(body)...)
	}
	return runs
}

// tjRuns finds each "(...) Tj" in a content stream and decodes its operand:
// PDF literal-string escapes first (\\, \(, \), \r), then UTF-16BE.
func tjRuns(content []byte) []string {
	var runs []string
	for i := 0; i < len(content); i++ {
		if content[i] != '(' {
			continue
		}
		var raw []byte
		j := i + 1
		for ; j < len(content) && content[j] != ')'; j++ {
			if content[j] == '\\' && j+1 < len(content) {
				j++
				if content[j] == 'r' {
					raw = append(raw, '\r')
					continue
				}
			}
			raw = append(raw, content[j])
		}
		rest := bytes.TrimLeft(content[min(j+1, len(content)):], " ")
		if !bytes.HasPrefix(rest, []byte("Tj")) || len(raw)%2 != 0 {
			i = j
			continue
		}
		u := make([]uint16, 0, len(raw)/2)
		for k := 0; k < len(raw); k += 2 {
			u = append(u, uint16(raw[k])<<8|uint16(raw[k+1]))
		}
		runs = append(runs, string(utf16.Decode(u)))
		i = j
	}
	return runs
}

func pdfHas(runs []string, want string) bool {
	for _, r := range runs {
		if r == want {
			return true
		}
	}
	return false
}

// pdfScenario is the dues scenario's September 2026 plus an incoming and an
// outgoing row and an envelope with participation, so every section of the
// statement has something in it.
func newPDFScenario(t *testing.T) duesScenario {
	t.Helper()
	ctx := context.Background()
	s := newDuesScenario(t)
	q := store.New(s.db)
	note := "Beli gula dan teh"
	s.post(t, "out", 40_000, "2026-09-12")
	if _, err := s.l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: s.fund.ID, AccountID: s.cashID, PurposeID: s.mainID, Direction: "in", Amount: 1_250_000, OccurredOn: "2026-09-20", Note: &note,
	}); err != nil {
		t.Fatalf("PostTransaction() = %v", err)
	}
	members, err := q.ListMembersByFund(ctx, s.fund.ID)
	if err != nil {
		t.Fatalf("ListMembersByFund() = %v", err)
	}
	var ani, budi int64
	for _, m := range members {
		switch m.Name {
		case "Ani":
			ani = m.ID
		case "Budi":
			budi = m.ID
		}
	}
	minimum := money.Amount(20_000)
	env, err := s.l.OpenIncidental(ctx, ledger.OpenIncidentalParams{
		FundID: s.fund.ID, Occasion: "Halal bihalal", OpenedOn: "2026-09-01", MinimumPerMember: &minimum,
	})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v", err)
	}
	for _, g := range []struct {
		member int64
		amount money.Amount
	}{{ani, 25_000}, {budi, 5_000}} {
		member := g.member
		if _, err := s.l.PostTransaction(ctx, ledger.PostTransactionParams{
			FundID: s.fund.ID, AccountID: s.cashID, PurposeID: env.PurposeID, Direction: "in", Amount: g.amount, OccurredOn: "2026-09-05", MemberID: &member,
		}); err != nil {
			t.Fatalf("PostTransaction(contribution) = %v", err)
		}
	}
	return s
}

func TestReportPDFCarriesFundTotalsDuesAndEnvelopes(t *testing.T) {
	s := newPDFScenario(t)
	rec := s.get(t, "/report/"+s.fund.ReportSlug+"/pdf?month=2026-09")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /report/{slug}/pdf = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("body does not start with %%PDF-: %q", rec.Body.Bytes()[:min(16, rec.Body.Len())])
	}
	runs := pdfText(t, rec.Body.Bytes())

	for _, want := range []string{
		"Kas RT 05", // the fund
		reportText.PDFStatement("September 2026"), // the month
		reportText.TotalIn, reportText.TotalOut, reportText.TotalNet,
		money.FormatIDR(1_375_000),       // the balance, a formatted amount with its non-breaking space
		"+" + money.FormatIDR(1_250_000), // the same, as a row
		"-" + money.FormatIDR(40_000),    // an outgoing row
		"Beli gula dan teh",              // a row's note
		reportText.DuesLabel,             // the dues section
		"Ani", "Budi", "Cici", "Dedi",    // members
		reportText.DuesPaidStatus, reportText.DuesUnpaid, reportText.DuesPartial, // dues statuses
		reportText.DuesPaidInAdvance,
		reportText.DuesPaidThrough("Oktober 2026"), // Dedi's paid-through line
		reportText.EnvelopesLabel, "Halal bihalal", // the envelope
		reportText.EnvelopeCollected + ": " + money.FormatIDR(30_000) + " \u00b7 " + reportText.EnvelopeGiven(2, 6),
		reportText.ParticipationGiven, reportText.ParticipationNot, reportText.ParticipationUnder,
	} {
		if !pdfHas(runs, want) {
			t.Errorf("PDF text has no run %q; runs:\n%s", want, strings.Join(runs, "\n"))
		}
	}
	if !strings.Contains(strings.Join(runs, ""), "Rp\u00a0") {
		t.Error("PDF text has no \"Rp\" + U+00A0: the non-breaking space did not survive the font")
	}
	if got, want := rec.Header().Get("Content-Length"), fmt.Sprint(rec.Body.Len()); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	writeSamplePDF(t, rec.Body.Bytes())
}

// writeSamplePDF saves the rendered file when asked, for a human to look at.
func writeSamplePDF(t *testing.T, body []byte) {
	t.Helper()
	if path := os.Getenv("URUNI_SAMPLE_PDF"); path != "" {
		//nolint:gosec // G703: the path is the developer's own URUNI_SAMPLE_PDF environment variable, set by hand to look at a rendered sample; no request data reaches it
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("writing the sample PDF: %v", err)
		}
	}
}

func TestReportPDFHeaders(t *testing.T) {
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 250_000, "2026-09-15")

	rec := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf?month=2026-09")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for name, want := range map[string]string{
		"Content-Type":        "application/pdf",
		"Content-Disposition": `attachment; filename="laporan-kas-2026-09.pdf"`,
		"X-Robots-Tag":        "noindex, nofollow",
		"Referrer-Policy":     "no-referrer",
		"Cache-Control":       "no-store",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("Set-Cookie = %q, want none", got)
	}
}

func TestReportPDFUnknownSlugIs404NamingNoFund(t *testing.T) {
	f := newReportFixture(t, "Kas RT 05")

	rec := f.get(t, "/report/thisslugdoesnotexistanywhere00/pdf?month=2026-09")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /report/<unknown>/pdf = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want the page's HTML 404", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q, want none on a 404", got)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Kas RT 05") || !strings.Contains(body, reportText.NotFoundBody) {
		t.Errorf("404 body = %q, want the not-found copy and no fund name", body)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag = %q, want noindex, nofollow", got)
	}
}

// A month that is missing, malformed or outside the selector is the current
// one, as on the page - and the filename says which month the file holds.
func TestReportPDFMonthFallsBackAsThePageDoes(t *testing.T) {
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-08-10")
	base := "/report/" + f.fund.ReportSlug + "/pdf"

	for _, tc := range []struct{ query, file string }{
		{"", "laporan-kas-2026-10.pdf"},
		{"?month=garbage", "laporan-kas-2026-10.pdf"},
		{"?month=2019-01", "laporan-kas-2026-10.pdf"},
		{"?month=2026-13", "laporan-kas-2026-10.pdf"},
		{"?month=2026-08", "laporan-kas-2026-08.pdf"},
	} {
		rec := f.get(t, base+tc.query)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", tc.query, rec.Code)
			continue
		}
		want := `attachment; filename="` + tc.file + `"`
		if got := rec.Header().Get("Content-Disposition"); got != want {
			t.Errorf("GET %q: Content-Disposition = %q, want %q", tc.query, got, want)
		}
	}
}

// The statement is the full month: the page's filters do not narrow it.
func TestReportPDFIgnoresPageFilters(t *testing.T) {
	s := newPDFScenario(t)
	base := "/report/" + s.fund.ReportSlug + "/pdf?month=2026-09"
	plain := pdfText(t, s.get(t, base).Body.Bytes())
	filtered := pdfText(t, s.get(t, base+"&dir=out&dues=paid&purpose=999&member=1").Body.Bytes())
	if strings.Join(plain, "\n") != strings.Join(filtered, "\n") {
		t.Errorf("filters changed the PDF's text:\nplain:\n%s\n\nfiltered:\n%s", strings.Join(plain, "\n"), strings.Join(filtered, "\n"))
	}
}

func TestReportPDFOfAFundWithNothingRecorded(t *testing.T) {
	f := newReportFixture(t, "Kas RT 05")
	rec := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	runs := pdfText(t, rec.Body.Bytes())
	for _, want := range []string{"Kas RT 05", money.FormatIDR(0), reportText.PDFNoRows} {
		if !pdfHas(runs, want) {
			t.Errorf("PDF text has no run %q; runs:\n%s", want, strings.Join(runs, "\n"))
		}
	}
}

// A long month runs onto more pages: no row is lost or split, the table's
// header repeats, and the footer counts the pages.
func TestReportPDFPaginates(t *testing.T) {
	f := newReportFixture(t, "Kas RT 05")
	const rows = 120
	for i := 1; i <= rows; i++ {
		f.post(t, "in", money.Amount(1_000*i), fmt.Sprintf("2026-09-%02d", 1+i%28))
	}
	rec := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf?month=2026-09")
	runs := pdfText(t, rec.Body.Bytes())

	pages := strings.Count(rec.Body.String(), "/Type /Page\n")
	if pages < 2 {
		t.Fatalf("a %d-row month made %d page(s), want more than one", rows, pages)
	}
	for i := 1; i <= rows; i++ {
		if want := "+" + money.FormatIDR(money.Amount(1_000*i)); !pdfHas(runs, want) {
			t.Fatalf("row %d (%q) is missing from the PDF", i, want)
		}
	}
	heads := 0
	for _, r := range runs {
		if r == reportText.PDFColDate {
			heads++
		}
	}
	// Page 1 is the summary alone; the table runs over every page after it.
	if heads != pages-1 {
		t.Errorf("table header drawn %d times over %d pages, want once per page after the summary", heads, pages)
	}
	if want := reportText.PDFPage("2", fmt.Sprint(pages)); !pdfHas(runs, want) {
		t.Errorf("no footer %q; the page count did not resolve", want)
	}
}

func TestReportPageLinksToThePDFForTheMonthShown(t *testing.T) {
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-08-10")

	body := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-08").Body.String()
	want := `href="/report/` + f.fund.ReportSlug + `/pdf?month=2026-08"`
	if !strings.Contains(body, want) || !strings.Contains(body, reportText.PDFDownload) {
		t.Errorf("page does not link %q as %q", want, reportText.PDFDownload)
	}
	// Filters shape the page, not the file: the link carries the month alone.
	body = f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-08&dir=in").Body.String()
	if !strings.Contains(body, want) {
		t.Error("a filtered page's PDF link carries more than the month")
	}
	if notFound := f.get(t, "/report/nope"); strings.Contains(notFound.Body.String(), reportText.PDFDownload) {
		t.Error("the 404 page offers a PDF")
	}
}

func TestReportFontLicenceShipsWithTheFonts(t *testing.T) {
	b, err := reportFonts.ReadFile("fonts/OFL.txt")
	if err != nil {
		t.Fatalf("OFL.txt is not embedded beside the fonts: %v", err)
	}
	if !bytes.Contains(b, []byte("SIL OPEN FONT LICENSE Version 1.1")) {
		t.Error("OFL.txt does not look like the SIL Open Font License 1.1")
	}
}

// fpdf's font map stops at U+FFFF: a rune above it - an emoji a treasurer
// typed into the fund's name, a member's or an envelope's - panicked inside
// Output and the statement was a 500. Such runes are dropped; the rest of the
// text stays.
func TestReportPDFSurvivesRunesBeyondTheBMP(t *testing.T) {
	f := newReportFixture(t, "Kas RT 05 \U0001F3E0")
	if _, err := f.l.OpenIncidental(context.Background(), ledger.OpenIncidentalParams{
		FundID: f.fund.ID, Occasion: "Halal bihalal \U0001F319\uFE0F", OpenedOn: "2026-09-01",
	}); err != nil {
		t.Fatalf("OpenIncidental() = %v", err)
	}
	rec := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf?month=2026-09")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	writeSamplePDF(t, rec.Body.Bytes())
	text := strings.Join(pdfText(t, rec.Body.Bytes()), "\n")
	runs := pdfText(t, rec.Body.Bytes())
	for _, want := range []string{"Kas RT 05", "Halal bihalal"} {
		if !pdfHas(runs, want) {
			t.Errorf("PDF text has no run %q; runs:\n%s", want, text)
		}
	}
	// The space before a dropped emoji closes up with the one after it.
	if !strings.Contains(text, "Kas RT 05 \u00b7 ") {
		t.Errorf("footer does not read %q; runs:\n%s", "Kas RT 05 \u00b7 ", text)
	}
	if strings.Contains(text, "  ") {
		t.Errorf("a dropped emoji left a double space; runs:\n%s", text)
	}
}

func TestPDFSafe(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Kas RT 05", "Kas RT 05"},
		{"Kas RT 05 \U0001F3E0", "Kas RT 05"},
		{"\U0001F3E0 Kas RT 05", "Kas RT 05"},
		{"Kas \U0001F3E0 RT 05", "Kas RT 05"},
		{"Kas \U0001F3E0\U0001F333 RT 05", "Kas RT 05"},
		{"Halal bihalal \U0001F319\uFE0F", "Halal bihalal"},
		{"Keluarga \U0001F468\u200D\U0001F469\u200D\U0001F467 Budi", "Keluarga Budi"},
		{"\U0001F3E0", ""},
		// Nothing dropped: the text comes back exactly, spaces and all.
		{"Rp\u00a050.000  tunai ", "Rp\u00a050.000  tunai "},
		{"Caf\u00e9 \u2615", "Caf\u00e9 \u2615"},
		// The font has no arrow: the PDF writes it in ASCII.
		{"Dipindah: Kas Utama \u2192 Duka", "Dipindah: Kas Utama -> Duka"},
		{"Dipindah: Kas \U0001F3E0 \u2192 Duka", "Dipindah: Kas -> Duka"},
	} {
		if got := pdfSafe(tc.in); got != tc.want {
			t.Errorf("pdfSafe(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every character the PDF draws has a glyph in the embedded font: a missing
// one prints as a blank box, which is how "Dipindah: A \u2192 B" came out
// before the arrow got its stand-in. The scenario carries purpose moves, dues
// and every label shape the list knows.
func TestReportPDFDrawsOnlyCharactersTheFontHas(t *testing.T) {
	s := newTxnScenario(t)
	rec := s.get(t, "/report/"+s.fund.ReportSlug+"/pdf?month=2026-09")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	runs := pdfText(t, rec.Body.Bytes())
	if !pdfHas(runs, "Dipindah: Kas Utama -> Duka") {
		t.Errorf("PDF text has no purpose move with its stand-in arrow; runs:\n%s", strings.Join(runs, "\n"))
	}

	for _, name := range []string{"fonts/NotoSans-Regular.ttf", "fonts/NotoSans-Bold.ttf"} {
		raw, err := reportFonts.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		has := fontRunes(t, raw)
		for _, run := range runs {
			for _, r := range run {
				if !has[r] {
					t.Errorf("%s has no glyph for U+%04X in %q", name, r, run)
				}
			}
		}
	}
}

// fontRunes is every character a TrueType font maps to a real glyph, read
// from its Unicode BMP cmap (format 4) - the one table fpdf's UTF-8 path reads
// too. Stdlib only, like pdfText: a font-parsing dependency for one test is
// not worth its weight.
func fontRunes(t *testing.T, font []byte) map[rune]bool {
	t.Helper()
	u16 := func(at int) int { return int(binary.BigEndian.Uint16(font[at:])) }
	u32 := func(at int) int { return int(binary.BigEndian.Uint32(font[at:])) }

	cmap := -1
	for i := range u16(4) {
		rec := 12 + 16*i
		if string(font[rec:rec+4]) == "cmap" {
			cmap = u32(rec + 8)
		}
	}
	if cmap < 0 {
		t.Fatal("font has no cmap table")
	}
	sub := -1
	for i := range u16(cmap + 2) {
		rec := cmap + 4 + 8*i
		platform, encoding := u16(rec), u16(rec+2)
		if (platform == 3 && encoding == 1) || (platform == 0 && encoding == 3) {
			sub = cmap + u32(rec+4)
		}
	}
	if sub < 0 || u16(sub) != 4 {
		t.Fatal("font has no format-4 Unicode BMP cmap")
	}

	segs := u16(sub+6) / 2
	ends, starts := sub+14, sub+16+2*segs
	deltas, offsets := starts+2*segs, starts+4*segs
	has := map[rune]bool{}
	for i := range segs {
		start, end := u16(starts+2*i), u16(ends+2*i)
		delta, offset := u16(deltas+2*i), u16(offsets+2*i)
		for c := start; c <= end && c != 0xFFFF; c++ {
			g := (c + delta) & 0xFFFF
			if offset != 0 {
				g = u16(offsets + 2*i + offset + 2*(c-start))
				if g != 0 {
					g = (g + delta) & 0xFFFF
				}
			}
			if g != 0 {
				//nolint:gosec // G115: c is a format-4 code, read from 16 bits and below 0xFFFF; it fits a rune
				has[rune(c)] = true
			}
		}
	}
	return has
}


// The statement opens with Ringkasan on its own page, and every later section
// - Transaksi, Iuran, Amplop - starts a page of its own, its heading first.
func TestReportPDFStartsEachSectionOnItsOwnPage(t *testing.T) {
	s := newPDFScenario(t)
	pages := pdfPages(t, s.get(t, "/report/"+s.fund.ReportSlug+"/pdf?month=2026-09").Body.Bytes())

	sections := []string{reportText.PDFSummary, reportText.TransactionsLabel, reportText.DuesLabel, reportText.EnvelopesLabel}
	if len(pages) != len(sections) {
		t.Fatalf("pages = %d, want %d (one per section, each fits one page)", len(pages), len(sections))
	}
	for i, want := range sections {
		if !pdfHas(pages[i], want) {
			t.Errorf("page %d has no heading %q; runs:\n%s", i+1, want, strings.Join(pages[i], "\n"))
		}
		for j, other := range sections {
			if j != i && pdfHas(pages[i], other) {
				t.Errorf("page %d also carries the heading %q", i+1, other)
			}
		}
	}
}
