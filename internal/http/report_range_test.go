package http

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/report"
)

// The report's date range (ADR-049), through the public routes. The txnScenario
// holds rows on 20 Aug and from 3 to 20 Sep, so the first transaction day is
// 2026-08-20 and today (reportNow) is 2026-10-02.

// dateField is the value of the date input named name; ok is false when the
// input carries no value attribute.
func dateField(t *testing.T, body, name string) (value string, ok bool) {
	t.Helper()
	tag := regexp.MustCompile(`<input type="date" id="` + name + `"[^>]*>`).FindString(body)
	if tag == "" {
		t.Fatalf("no %q date input in body", name)
	}
	m := regexp.MustCompile(` value="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// periodForm is the period form's markup alone.
func periodForm(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `id="period-form"`)
	if i < 0 {
		t.Fatal("no period form on the page")
	}
	return body[i : i+strings.Index(body[i:], "</form>")]
}

// isRangePage is whether the period select reads Rentang tanggal.
func isRangePage(body string) bool {
	return strings.Contains(body, `<option value="range" selected>`)
}

func TestReportRangeShowsTheRangeAsTheWalkAndTheHeader(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	slug := "/report/" + s.fund.ReportSlug
	rec := s.get(t, slug+"?from=2026-08-20&to=2026-09-10")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	rangeText := report.Text.RangeText("20 Agustus 2026", "10 September 2026")
	if rangeText != "20 Agustus 2026 \u2013 10 September 2026" {
		t.Fatalf("range text = %q", rangeText)
	}
	if !strings.Contains(body, `<p class="muted">`+rangeText+`</p>`) {
		t.Errorf("the header line is not the range text %q", rangeText)
	}
	if strings.Contains(body, report.Text.AsOfRunning("2 Oktober 2026")) {
		t.Error("a past range is labelled as the running month")
	}

	// The period form: Rentang tanggal chosen, the two days and their bounds.
	if !isRangePage(body) {
		t.Error("the period select does not read Rentang tanggal")
	}
	for name, want := range map[string]string{"from": "2026-08-20", "to": "2026-09-10"} {
		if got, ok := dateField(t, body, name); !ok || got != want {
			t.Errorf("date field %s = %q (set %v), want %q", name, got, ok, want)
		}
	}
	if !strings.Contains(body, `min="2026-08-20" max="2026-10-02"`) {
		t.Error("the date fields are not bounded by the first transaction day and today")
	}
	if strings.Contains(body, report.Text.MonthPrev) || strings.Contains(body, report.Text.MonthNext) {
		t.Error("a range shows the month steps")
	}

	// The walk runs from the day before From to To: 7000 + 200000 + 25000 (dues)
	// + 15000 (a gift) came in; nothing went out before the 12th.
	totals := totalsOf(t, body)
	if len(totals) < 4 || totals[0] != (totalLine{report.Text.WalkStart("19 Agustus 2026"), money.FormatIDR(0)}) {
		t.Errorf("walk starts %+v, want Saldo 19 Agustus 2026 at Rp0", totals)
	}
	if last := totals[len(totals)-1]; last != (totalLine{report.Text.WalkEnd("10 September 2026"), money.FormatIDR(247_000)}) {
		t.Errorf("walk ends %+v, want Saldo 10 September 2026 at Rp247.000", last)
	}
	if got := rowCount(t, body); got != 4 {
		t.Errorf("rows = %d, want the 4 dated 20 Aug to 10 Sep", got)
	}
	if !strings.Contains(body, money.FormatIDR(247_000)) {
		t.Error("the hero is not the balance as of To")
	}

	// By month, the list splits at the boundary the range crosses.
	byMonth := s.get(t, slug+"?from=2026-08-20&to=2026-09-10&group=month").Body.String()
	if groupSection(t, byMonth, "2026-08") == "" || groupSection(t, byMonth, "2026-09") == "" {
		t.Error("a by-month range has no heading for each month it crosses")
	}
}

func TestReportRangeClampsSwapsAndFallsBack(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	slug := "/report/" + s.fund.ReportSlug

	ranges := []struct{ name, query, from, to string }{
		{"as given", "from=2026-09-03&to=2026-09-10", "2026-09-03", "2026-09-10"},
		{"from before the first transaction", "from=2026-01-01&to=2026-09-10", "2026-08-20", "2026-09-10"},
		{"to after today", "from=2026-09-01&to=2027-01-01", "2026-09-01", "2026-10-02"},
		{"both ends past the bounds", "from=2020-01-01&to=2030-01-01", "2026-08-20", "2026-10-02"},
		{"swapped", "from=2026-09-10&to=2026-09-01", "2026-09-01", "2026-09-10"},
		{"swapped and clamped", "from=2030-01-01&to=2020-01-01", "2026-08-20", "2026-10-02"},
		{"both after today", "from=2027-01-01&to=2027-02-01", "2026-10-02", "2026-10-02"},
		{"both before the first transaction", "from=2020-01-01&to=2020-02-01", "2026-08-20", "2026-08-20"},
		{"a single day", "from=2026-09-05&to=2026-09-05", "2026-09-05", "2026-09-05"},
		{"month is ignored beside a range", "month=2026-08&from=2026-09-03&to=2026-09-10", "2026-09-03", "2026-09-10"},
		{"month is ignored even when malformed", "month=oktober&from=2026-09-03&to=2026-09-10", "2026-09-03", "2026-09-10"},
	}
	for _, tc := range ranges {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := s.get(t, slug+"?"+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET ?%s = %d, want 200", tc.query, rec.Code)
			}
			body := rec.Body.String()
			if !isRangePage(body) {
				t.Fatalf("?%s is not read as a range", tc.query)
			}
			from, _ := dateField(t, body, "from")
			to, _ := dateField(t, body, "to")
			if from != tc.from || to != tc.to {
				t.Errorf("?%s shows %s to %s, want %s to %s", tc.query, from, to, tc.from, tc.to)
			}
		})
	}

	// Anything that is not two real days is the month, quietly: the current one,
	// or the one ?month= names.
	months := []struct{ name, query, want string }{
		{"from alone", "from=2026-09-03", "2026-10"},
		{"to alone", "to=2026-09-10", "2026-10"},
		{"from empty", "from=&to=2026-09-10", "2026-10"},
		{"from malformed", "from=2026-9-3&to=2026-09-10", "2026-10"},
		{"to impossible", "from=2026-09-03&to=2026-02-30", "2026-10"},
		{"words", "from=kemarin&to=hari-ini", "2026-10"},
		{"months, not days", "from=2026-09&to=2026-10", "2026-10"},
		{"period=range alone is not a range", "period=range", "2026-10"},
		{"period=range with one day", "period=range&from=2026-09-03", "2026-10"},
		{"one-sided beside a month", "month=2026-09&from=2026-09-03", "2026-09"},
		{"empty days beside a month", "month=2026-09&period=&from=&to=", "2026-09"},
	}
	for _, tc := range months {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := s.get(t, slug+"?"+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET ?%s = %d, want 200", tc.query, rec.Code)
			}
			body := rec.Body.String()
			if isRangePage(body) {
				t.Errorf("?%s reads as a range", tc.query)
			}
			if got := selectedMonth(t, body); got != tc.want {
				t.Errorf("?%s shows month %s, want %s", tc.query, got, tc.want)
			}
		})
	}
}

// In a month the date fields carry no value (so a plain submit without
// JavaScript still asks for the month), only the days they start from.
func TestReportMonthLeavesTheDateFieldsEmptyWithDefaults(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	body := s.get(t, s.base).Body.String()

	for _, name := range []string{"from", "to"} {
		if v, ok := dateField(t, body, name); ok {
			t.Errorf("month mode: date field %s has value %q, want none", name, v)
		}
	}
	for name, want := range map[string]string{"from": "2026-10-01", "to": "2026-10-02"} {
		if !strings.Contains(body, `id="`+name+`"`) || !regexp.MustCompile(`id="`+name+`"[^>]*data-default="`+want+`"`).MatchString(body) {
			t.Errorf("date field %s does not default to %s", name, want)
		}
	}
	if !strings.Contains(body, report.Text.MonthPrev) {
		t.Error("a month lacks its step to the previous month")
	}
	for _, want := range []string{report.Text.PeriodLabel, report.Text.PeriodMonth, report.Text.PeriodRange, report.Text.RangeFrom, report.Text.RangeTo} {
		if !strings.Contains(body, ">"+want+"<") {
			t.Errorf("the period form lacks %q", want)
		}
	}
	if isRangePage(body) {
		t.Error("a month reads as a range")
	}
}

// from and to ride in Active, so the filter and dues forms keep the range (and
// carry no month); the period form carries the filters but not the old days,
// which its own fields replace.
func TestReportRangeKeepsItsDaysInTheFormsAndDropsTheMonth(t *testing.T) {
	t.Parallel()
	s := newDuesScenario(t)
	q := url.Values{}
	q.Set("from", "2026-09-05")
	q.Set("to", "2026-09-30")
	q.Set("group", "month")
	q.Set("dues", "unpaid")
	body := s.get(t, "/report/"+s.fund.ReportSlug+"?"+q.Encode()).Body.String()

	const hidden = `<input type="hidden" name="`
	if strings.Contains(body, hidden+`month"`) {
		t.Error("a range page carries a hidden month")
	}

	filters := body[strings.Index(body, `action="#transactions"`):]
	filters = filters[:strings.Index(filters, "</form>")]
	for _, want := range []string{hidden + `from" value="2026-09-05">`, hidden + `to" value="2026-09-30">`, hidden + `dues" value="unpaid">`} {
		if !strings.Contains(filters, want) {
			t.Errorf("the transactions filter form lacks %q:\n%s", want, filters)
		}
	}

	dues := body[strings.Index(body, `action="#dues"`):]
	dues = dues[:strings.Index(dues, "</form>")]
	for _, want := range []string{hidden + `from" value="2026-09-05">`, hidden + `to" value="2026-09-30">`, hidden + `group" value="month">`} {
		if !strings.Contains(dues, want) {
			t.Errorf("the dues filter form lacks %q:\n%s", want, dues)
		}
	}
	if strings.Contains(dues, hidden+`dues"`) {
		t.Error("the dues form carries its own filter as a hidden field")
	}

	period := periodForm(t, body)
	for _, want := range []string{hidden + `group" value="month">`, hidden + `dues" value="unpaid">`} {
		if !strings.Contains(period, want) {
			t.Errorf("the period form drops the filter %q", want)
		}
	}
	if strings.Contains(period, hidden+`from"`) || strings.Contains(period, hidden+`to"`) {
		t.Error("the period form carries the old days as hidden fields beside its own date fields")
	}
}

// The dues section is monthly: in a range it names the month it reads, the one
// that holds To, and a month has no such line.
func TestReportRangeCaptionsTheDuesSection(t *testing.T) {
	t.Parallel()
	s := newDuesScenario(t)
	slug := "/report/" + s.fund.ReportSlug

	for _, tc := range []struct{ to, want string }{
		{"2026-09-30", "Iuran bulan September 2026"},
		{"2026-10-02", "Iuran bulan Oktober 2026"},
	} {
		body := s.get(t, slug+"?from=2026-09-05&to="+tc.to).Body.String()
		if !strings.Contains(body, `<p class="muted">`+tc.want+`</p>`) {
			t.Errorf("to %s: no dues caption %q", tc.to, tc.want)
		}
	}
	if body := s.get(t, s.base).Body.String(); strings.Contains(body, "Iuran bulan") {
		t.Error("a month carries the dues caption")
	}

	// And the list is that month's: September's members, in September's statuses.
	sec := duesSection(t, s.get(t, slug+"?from=2026-09-05&to=2026-09-30").Body.String())
	if got, want := strings.Join(duesNames(sec), ","), "Ani,Budi,Cici,Dedi,Eka"; got != want {
		t.Errorf("dues members = %q, want %q", got, want)
	}
}

func TestReportRangeWithNoRowsSaysSoInItsOwnWords(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	slug := "/report/" + s.fund.ReportSlug

	body := s.get(t, slug+"?from=2026-08-21&to=2026-08-31").Body.String()
	if !strings.Contains(body, `<p class="empty">`+report.Text.NoRowsRange+`</p>`) || report.Text.NoRowsRange != "Tidak ada transaksi yang cocok pada rentang ini." {
		t.Errorf("an empty range does not say %q", report.Text.NoRowsRange)
	}
	if strings.Contains(body, report.Text.NoRows) {
		t.Error("an empty range says the month's line")
	}
	if body := s.get(t, slug+"?month=2026-10").Body.String(); !strings.Contains(body, report.Text.NoRows) {
		t.Error("an empty month lost its line")
	}
}

// The page's download is the period it shows: the range's two clamped days, the
// grouping alongside, or the month.
func TestReportPDFLinkCarriesTheRange(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	slug := "/report/" + s.fund.ReportSlug
	pdf := slug + "/pdf"

	for _, tc := range []struct{ query, want string }{
		{"?from=2026-09-03&to=2026-09-10", pdf + "?from=2026-09-03&amp;to=2026-09-10"},
		{"?from=2026-01-01&to=2030-01-01&group=month", pdf + "?from=2026-08-20&amp;to=2026-10-02&amp;group=month"},
		{"?from=2026-09-10&to=2026-09-03", pdf + "?from=2026-09-03&amp;to=2026-09-10"},
		{"?month=2026-09", pdf + "?month=2026-09"},
		{"?from=2026-09-03", pdf + "?month=2026-10"},
		{"?month=2026-09&group=month", pdf + "?month=2026-09&amp;group=month"},
	} {
		body := s.get(t, slug+tc.query).Body.String()
		if !strings.Contains(body, `href="`+tc.want+`"`) {
			t.Errorf("GET %s: no download link %q in:\n%s", tc.query, tc.want, body[strings.LastIndex(body, "<p class=\"muted pdf\">"):])
		}
	}
}

func TestReportPDFRangeTitleFilenameAndContent(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	pdf := "/report/" + s.fund.ReportSlug + "/pdf"

	rec := s.get(t, pdf+"?from=2026-08-20&to=2026-09-10")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET pdf = %d, want 200", rec.Code)
	}
	if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="laporan-kas-2026-08-20_2026-09-10.pdf"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	runs := pdfText(t, rec.Body.Bytes())
	statement := "Laporan kas 20 Agustus 2026 \u2013 10 September 2026"
	if !pdfHas(runs, statement) || report.Text.PDFStatementRange(report.Text.RangeText("20 Agustus 2026", "10 September 2026")) != statement {
		t.Errorf("the PDF has no statement %q", statement)
	}
	if pdfHas(runs, "Laporan kas bulan September 2026") {
		t.Error("a range PDF carries a month's statement")
	}
	for _, want := range []string{report.Text.WalkStart("19 Agustus 2026"), report.Text.WalkEnd("10 September 2026"), money.FormatIDR(247_000), "+" + money.FormatIDR(200_000)} {
		if !pdfHas(runs, want) {
			t.Errorf("the PDF has no %q", want)
		}
	}
	if pdfHas(runs, "-"+money.FormatIDR(60_000)) {
		t.Error("the PDF carries the 12 Sep spend, past the range")
	}
	// The statement already names the range: the date line is not repeated.
	if n := countRuns(runs, report.Text.RangeText("20 Agustus 2026", "10 September 2026")); n != 0 {
		t.Errorf("the range text stands alone %d times as a line, want 0", n)
	}

	// Clamped like the page; the filename is the clamped days, never the query.
	clamped := s.get(t, pdf+"?from=2026-01-01&to=2030-01-01")
	if got, want := clamped.Header().Get("Content-Disposition"), `attachment; filename="laporan-kas-2026-08-20_2026-10-02.pdf"`; got != want {
		t.Errorf("clamped: Content-Disposition = %q, want %q", got, want)
	}
	swapped := s.get(t, pdf+"?from=2026-09-10&to=2026-09-03")
	if got, want := swapped.Header().Get("Content-Disposition"), `attachment; filename="laporan-kas-2026-09-03_2026-09-10.pdf"`; got != want {
		t.Errorf("swapped: Content-Disposition = %q, want %q", got, want)
	}

	// One-sided or invalid is the month, as the page reads it.
	for _, q := range []string{"?from=2026-09-03", "?to=2026-09-03", "?from=x&to=y", ""} {
		rec := s.get(t, pdf+q)
		if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="laporan-kas-2026-10.pdf"`; got != want {
			t.Errorf("GET pdf%s: Content-Disposition = %q, want %q", q, got, want)
		}
	}
	if got := s.get(t, pdf+"?month=2026-09&from=2026-09-03").Header().Get("Content-Disposition"); got != `attachment; filename="laporan-kas-2026-09.pdf"` {
		t.Errorf("a month beside a one-sided range: Content-Disposition = %q", got)
	}
}

func countRuns(runs []string, want string) int {
	n := 0
	for _, r := range runs {
		if r == want {
			n++
		}
	}
	return n
}

func TestReportPDFRangeEmptyAndGroupedAndWithDues(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	pdf := "/report/" + s.fund.ReportSlug + "/pdf"

	empty := pdfText(t, s.get(t, pdf+"?from=2026-08-21&to=2026-08-31").Body.Bytes())
	if !pdfHas(empty, report.Text.PDFNoRowsRange) || report.Text.PDFNoRowsRange != "Tidak ada transaksi pada rentang ini." {
		t.Errorf("an empty range PDF does not say %q", report.Text.PDFNoRowsRange)
	}
	if pdfHas(empty, report.Text.PDFNoRows) {
		t.Error("an empty range PDF says the month's line")
	}

	byMonth := strings.Join(pdfText(t, s.get(t, pdf+"?from=2026-08-20&to=2026-09-10&group=month").Body.Bytes()), "\n")
	for _, want := range []string{"Agustus 2026", "September 2026"} {
		if !strings.Contains(byMonth, want) {
			t.Errorf("the by-month range PDF has no %q heading", want)
		}
	}
	byKind := strings.Join(pdfText(t, s.get(t, pdf+"?from=2026-08-20&to=2026-09-10").Body.Bytes()), "\n")
	if !strings.Contains(byKind, report.Text.DirectionIn) || !strings.Contains(byKind, report.Text.DirectionOut) {
		t.Error("the by-kind range PDF lacks its Uang masuk and Uang keluar blocks")
	}

	d := newDuesScenario(t)
	runs := pdfText(t, d.get(t, "/report/"+d.fund.ReportSlug+"/pdf?from=2026-09-05&to=2026-09-30").Body.Bytes())
	if !pdfHas(runs, "Iuran bulan September 2026") {
		t.Error("the range PDF's dues table has no month caption")
	}
	if month := pdfText(t, d.get(t, "/report/"+d.fund.ReportSlug+"/pdf?month=2026-09").Body.Bytes()); pdfHas(month, "Iuran bulan September 2026") {
		t.Error("a month PDF carries the dues caption")
	}
}

// The page's one script is nonce'd and still the only one; it knows the period
// select, which does not submit on change.
func TestReportScriptHandlesThePeriodSelect(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	body := s.get(t, s.base).Body.String()
	if n := strings.Count(body, "<script "); n != 1 {
		t.Errorf("scripts = %d, want 1", n)
	}
	if !strings.Contains(body, "if (s !== period)") || !strings.Contains(body, "showPeriod") {
		t.Error("the script does not leave the period select out of the auto-submit")
	}
	if strings.Contains(body, " style=") {
		t.Error("an inline style attribute would be blocked by the CSP")
	}
}
