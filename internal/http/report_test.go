package http

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// reportNow is 2026-10-02 10:00 in Asia/Jakarta - early enough in a month
// that the current month is unambiguous on any CI clock.
var reportNow = time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC)

type reportFixture struct {
	db     *sql.DB
	router http.Handler
	l      *ledger.Ledger
	fund   store.Fund
	cashID int64
	mainID int64
}

func newReportFixture(t *testing.T, fundName string) reportFixture {
	t.Helper()
	sqlDB := testStoreDB(t)
	l := ledger.New(sqlDB)
	res, err := l.SetUpFund(context.Background(), ledger.SetUpFundParams{
		FundName: fundName,
		Accounts: []ledger.AccountInput{{Kind: "cash", Name: "Tunai"}},
	})
	if err != nil {
		t.Fatalf("SetUpFund() = %v, want no error", err)
	}
	r := chi.NewRouter()
	r.Get("/report/{slug}", reportHandler(l, store.New(sqlDB), testLogger(), func() time.Time { return reportNow }))
	r.Get("/report/{slug}/pdf", reportPDFHandler(l, store.New(sqlDB), testLogger(), func() time.Time { return reportNow }))
	return reportFixture{db: sqlDB, router: r, l: l, fund: res.Fund, cashID: res.Accounts[0].ID, mainID: res.MainPurposeID}
}

func (f reportFixture) post(t *testing.T, direction string, amount money.Amount, on string) {
	t.Helper()
	if _, err := f.l.PostTransaction(context.Background(), ledger.PostTransactionParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID,
		Direction: direction, Amount: amount, OccurredOn: on,
	}); err != nil {
		t.Fatalf("PostTransaction(%s %d on %s) = %v, want no error", direction, amount, on, err)
	}
}

func (f reportFixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// selectedMonth is the <option> the month selector marks selected.
func selectedMonth(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`<option value="(\d{4}-\d{2})" selected>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no selected month option in body:\n%s", body)
	}
	return m[1]
}

func TestReportKnownSlugIs200WithEveryHeader(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 250_000, "2026-09-15")

	rec := f.get(t, "/report/"+f.fund.ReportSlug)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /report/{slug} = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	for name, want := range map[string]string{
		"Content-Type":    "text/html; charset=utf-8",
		"X-Robots-Tag":    "noindex, nofollow",
		"Referrer-Policy": "no-referrer",
		"Cache-Control":   "no-store",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("Set-Cookie = %q, want none: the report sets no cookie", got)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`<meta name="robots" content="noindex, nofollow">`,
		"Kas RT 05",
		"per 2 Oktober 2026",
		money.FormatIDR(250_000),
		reportText.PurposesLabel,
		reportText.NeverChecked,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, "Tunai") {
		t.Error("body names a location; the report never shows where the money sits (ADR-035)")
	}
}

// Every report response, the page and its 404, carries a nonce CSP whose
// nonce is the one on the page's <style> and <script>, and every <style> and
// <script> on the page carries it - an untagged one would be blocked in the
// browser. A second request draws a different nonce.
func TestReportCSPNonceMatchesThePage(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 250_000, "2026-09-15")

	nonceRe := regexp.MustCompile(`script-src 'nonce-([A-Za-z0-9]+)'`)
	seen := map[string]bool{}
	for _, path := range []string{"/report/" + f.fund.ReportSlug, "/report/" + f.fund.ReportSlug, "/report/no-such-slug"} {
		rec := f.get(t, path)
		csp := rec.Header().Get("Content-Security-Policy")
		m := nonceRe.FindStringSubmatch(csp)
		if m == nil {
			t.Fatalf("GET %s: Content-Security-Policy = %q, want a script-src nonce", path, csp)
		}
		nonce := m[1]
		if want := reportCSP(nonce); csp != want {
			t.Errorf("GET %s: Content-Security-Policy = %q, want %q", path, csp, want)
		}
		if seen[nonce] {
			t.Errorf("GET %s: nonce %q repeats an earlier response's", path, nonce)
		}
		seen[nonce] = true

		body := rec.Body.String()
		tagged := `nonce="` + nonce + `"`
		for _, tag := range regexp.MustCompile(`<(script|style)\b[^>]*>`).FindAllString(body, -1) {
			if !strings.Contains(tag, tagged) {
				t.Errorf("GET %s: %s lacks this response's nonce", path, tag)
			}
		}
		if !strings.Contains(body, "<style "+tagged+">") {
			t.Errorf("GET %s: the page's <style> does not carry the nonce", path)
		}
		if path != "/report/no-such-slug" && !strings.Contains(body, "<script "+tagged+">") {
			t.Errorf("GET %s: the page's <script> does not carry the nonce", path)
		}
	}
}

func TestReportUnknownSlugIs404NamingNoFund(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")

	rec := f.get(t, "/report/thisslugdoesnotexistanywhere00")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /report/<unknown> = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Kas RT 05") {
		t.Error("404 body names the fund; an unknown slug must reveal nothing")
	}
	if !strings.Contains(body, reportText.NotFoundBody) {
		t.Errorf("404 body does not carry the not-found copy:\n%s", body)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("404 X-Robots-Tag = %q, want noindex, nofollow", got)
	}
}

func TestReportMonthFallsBackToTheCurrentMonth(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-08-10")
	slug := "/report/" + f.fund.ReportSlug

	for _, tc := range []struct{ name, query, want string }{
		{"missing", "", "2026-10"},
		{"malformed", "?month=oktober", "2026-10"},
		{"impossible", "?month=2026-13", "2026-10"},
		{"before the first transaction", "?month=2026-07", "2026-10"},
		{"in the future", "?month=2026-11", "2026-10"},
		{"a real past month", "?month=2026-08", "2026-08"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.get(t, slug+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tc.query, rec.Code)
			}
			if got := selectedMonth(t, rec.Body.String()); got != tc.want {
				t.Errorf("selected month for %q = %s, want %s", tc.query, got, tc.want)
			}
		})
	}
}

func TestReportMonthStepLinks(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-08-10")
	slug := "/report/" + f.fund.ReportSlug

	body := f.get(t, slug+"?month=2026-09").Body.String()
	if !strings.Contains(body, `href="?month=2026-08#months"`) || !strings.Contains(body, `href="?month=2026-10#months"`) {
		t.Errorf("September should link to August and October:\n%s", body)
	}
	body = f.get(t, slug).Body.String()
	if strings.Contains(body, `href="?month=2026-11#months"`) {
		t.Error("the current month links forward to a month that has not happened")
	}
	body = f.get(t, slug+"?month=2026-08").Body.String()
	if strings.Contains(body, `href="?month=2026-07#months"`) {
		t.Error("the first month links back to a month before any transaction")
	}
}

func TestReportEmptyFundShowsRpZeroAndTheWarmLine(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")

	body := f.get(t, "/report/"+f.fund.ReportSlug).Body.String()
	if !strings.Contains(body, money.FormatIDR(0)) {
		t.Errorf("empty fund body does not show %q", money.FormatIDR(0))
	}
	if !strings.Contains(body, reportText.Empty) {
		t.Errorf("empty fund body does not carry the empty line")
	}

	f.post(t, "in", 1, "2026-10-01")
	if body := f.get(t, "/report/"+f.fund.ReportSlug).Body.String(); strings.Contains(body, reportText.Empty) {
		t.Error("a fund with a transaction still says nothing has been recorded")
	}
}

// The three cek kas states, read the way the home banner reads them: never
// counted is neutral (never green), matched is green, a discrepancy is
// terracotta with its amount.
func TestReportPageCheckStates(t *testing.T) {
	t.Parallel()
	base := ledger.Report{FundName: "Kas", AsOf: "2026-10-02", Month: "2026-10", Months: []string{"2026-10"}}

	page := buildReportPage(base, false)
	if page.Check.Class != "never" || page.Check.Message != reportText.NeverChecked {
		t.Errorf("no count = %+v, want the never state", page.Check)
	}

	base.Reconciliation = &ledger.ReportReconciliation{Date: "2026-09-30", Matched: true}
	page = buildReportPage(base, false)
	if page.Check.Class != "matched" || page.Check.When != "Terakhir dicek 30 September 2026" {
		t.Errorf("matched = %+v", page.Check)
	}

	base.Reconciliation = &ledger.ReportReconciliation{Date: "2026-09-30", Difference: 15_000}
	page = buildReportPage(base, false)
	if page.Check.Class != "discrepancy" || page.Check.Message != reportText.Discrepancy(money.FormatIDR(15_000)) {
		t.Errorf("discrepancy = %+v", page.Check)
	}
}

func TestReportPagePurposeBalances(t *testing.T) {
	t.Parallel()
	page := buildReportPage(ledger.Report{
		AsOf: "2026-10-02", Month: "2026-10", Months: []string{"2026-10"},
		PurposeBalances: []ledger.ReportPurposeBalance{
			{Name: "Kas Utama", Kind: "main", Balance: -20_000},
			{Name: "Duka", Kind: "incidental", Balance: 50_000},
		},
	}, false)
	if len(page.Purposes) != 2 || page.Purposes[0].Name != "Kas Utama" {
		t.Fatalf("purposes = %+v, want Kas Utama first, in the ledger's order", page.Purposes)
	}
	if !page.Purposes[0].Negative || page.Purposes[1].Negative {
		t.Errorf("negative flags = %v, %v; want only Kas Utama's", page.Purposes[0].Negative, page.Purposes[1].Negative)
	}
}

// TestReportPaletteMatchesIndexCSS is ADR-035's guard on the hand-kept
// palette copy: every token the report uses must hold the value index.css's
// :root gives it.
func TestReportPaletteMatchesIndexCSS(t *testing.T) {
	t.Parallel()
	css, err := os.ReadFile("../../web/src/index.css")
	if err != nil {
		t.Fatalf("reading index.css: %v", err)
	}
	root := regexp.MustCompile(`(?s):root\s*\{(.*?)\}`).FindSubmatch(css)
	if root == nil {
		t.Fatal("no :root block in index.css")
	}
	values := map[string]string{}
	for _, m := range regexp.MustCompile(`--([a-z-]+):\s*([^;]+);`).FindAllSubmatch(root[1], -1) {
		values[string(m[1])] = strings.ToLower(strings.TrimSpace(string(m[2])))
	}
	for name, want := range reportPalette {
		got, ok := values[name]
		if !ok {
			t.Errorf("--%s is in the report palette but not in index.css's :root", name)
			continue
		}
		if got != want {
			t.Errorf("--%s = %s in index.css, %s in the report palette", name, got, want)
		}
	}
	if len(reportPaletteOrder) != len(reportPalette) {
		t.Errorf("reportPaletteOrder has %d names, reportPalette %d", len(reportPaletteOrder), len(reportPalette))
	}
}

func TestReportCopyDates(t *testing.T) {
	t.Parallel()
	if got := reportText.longDate("2026-01-09"); got != "9 Januari 2026" {
		t.Errorf("longDate = %q", got)
	}
	if got := reportText.monthName("2026-12"); got != "Desember 2026" {
		t.Errorf("monthName = %q", got)
	}
}

// The total owed to members (#406) sits in the summary as of today, whatever
// month is shown, and the line is absent while nothing is owed.
func TestReportSummaryShowsTheTotalOwedToMembers(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-09-01")

	if body := f.get(t, "/report/"+f.fund.ReportSlug).Body.String(); strings.Contains(body, reportText.OwedLabel) {
		t.Error("a fund owing nothing still shows the owed line")
	}

	q := store.New(f.db)
	member, err := q.CreateMember(context.Background(), store.CreateMemberParams{JoinedOn: "2000-01-01", FundID: f.fund.ID, Name: "Ani", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateMember() = %v, want no error", err)
	}
	if _, err := q.CreateReimbursement(context.Background(), store.CreateReimbursementParams{
		FundID: f.fund.ID, MemberID: member.ID, PurposeID: f.mainID, Amount: 45_000, IncurredOn: "2026-09-03", CreatedAt: 1,
	}); err != nil {
		t.Fatalf("CreateReimbursement() = %v, want no error", err)
	}

	for _, month := range []string{"2026-09", "2026-10"} {
		body := f.get(t, "/report/"+f.fund.ReportSlug+"?month="+month).Body.String()
		want := "<li><span>" + reportText.OwedLabel + "</span><span class=\"tabular\">" + money.FormatIDR(45_000) + "</span></li>"
		if !strings.Contains(body, want) {
			t.Errorf("month %s: body has no owed line %q", month, want)
		}
	}
}

// The date line says what every figure answers to (ADR-037): a past month's
// last day, or today as the running month. The month picker comes first, so
// everything beneath it reads as that month's.
func TestReportDateLineAndMonthPickerFirst(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-09-01")
	f.post(t, "in", 25_000, "2026-10-01")

	past := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-09").Body.String()
	if want := reportText.AsOf("30 September 2026"); !strings.Contains(past, want) {
		t.Errorf("past month has no date line %q", want)
	}
	if !strings.Contains(past, money.FormatIDR(100_000)) || strings.Contains(past, money.FormatIDR(125_000)) {
		t.Error("past month's Saldo kas is not its month-end balance")
	}

	running := f.get(t, "/report/"+f.fund.ReportSlug).Body.String()
	if want := reportText.AsOfRunning("2 Oktober 2026"); !strings.Contains(running, want) {
		t.Errorf("running month has no date line %q", want)
	}
	if nav, hero := strings.Index(running, `<nav class="months"`), strings.Index(running, `<section class="hero"`); nav < 0 || hero < 0 || nav > hero {
		t.Errorf("month picker at %d, summary at %d; want the picker first", nav, hero)
	}
}
