package http

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/report"
	"github.com/kerti/uruni/internal/store"
)

// reportNow is 2026-10-02 10:00 in Asia/Jakarta: early enough in a month that
// the current month is unambiguous on any CI clock.
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
	l := ledger.New(sqlDB, ledger.WithClock(fixedClock(reportNow)))
	res, err := l.SetUpFund(context.Background(), ledger.SetUpFundParams{
		FundName: fundName,
		Accounts: []ledger.AccountInput{{Kind: "cash", Name: "Tunai"}},
	})
	if err != nil {
		t.Fatalf("SetUpFund() = %v, want no error", err)
	}
	r := chi.NewRouter()
	r.Get("/report/{slug}", reportHandler(l, store.New(sqlDB), testLogger()))
	r.Get("/report/{slug}/pdf", reportPDFHandler(l, store.New(sqlDB), testLogger()))
	return reportFixture{db: sqlDB, router: r, l: l, fund: res.Fund, cashID: res.Accounts[0].ID, mainID: res.MainPurposeID}
}

func (f reportFixture) post(t *testing.T, direction ledger.Direction, amount money.Amount, on string) {
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
		report.Text.PurposesLabel,
		report.Text.NeverChecked,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, "Tunai") {
		t.Error("body names a location; the report never shows where the money sits (ADR-035)")
	}
}

// Every report response, the page and its 404, carries a nonce CSP whose nonce
// is the one on the page's <style> and <script>, and every <style> and
// <script> carries it (an untagged one would be blocked). A second request
// draws a different nonce.
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
	if !strings.Contains(body, report.Text.NotFoundBody) {
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
	if !strings.Contains(body, report.Text.Empty) {
		t.Errorf("empty fund body does not carry the empty line")
	}

	f.post(t, "in", 1, "2026-10-01")
	if body := f.get(t, "/report/"+f.fund.ReportSlug).Body.String(); strings.Contains(body, report.Text.Empty) {
		t.Error("a fund with a transaction still says nothing has been recorded")
	}
}

// The total owed to members (#406) sits in the summary as of today, whatever
// month is shown, and the line is absent while nothing is owed.
func TestReportSummaryShowsTheTotalOwedToMembers(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-09-01")

	if body := f.get(t, "/report/"+f.fund.ReportSlug).Body.String(); strings.Contains(body, report.Text.OwedLabel) {
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
		want := "<li><span>" + report.Text.OwedLabel + "</span><span class=\"tabular\">" + money.FormatIDR(45_000) + "</span></li>"
		if !strings.Contains(body, want) {
			t.Errorf("month %s: body has no owed line %q", month, want)
		}
	}
}

// The date line says what every figure answers to (ADR-037): a past month's
// last day, or today for the running month. The month picker comes first, so
// everything beneath reads as that month's.
func TestReportDateLineAndMonthPickerFirst(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-09-01")
	f.post(t, "in", 25_000, "2026-10-01")

	past := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-09").Body.String()
	if want := report.Text.AsOf("30 September 2026"); !strings.Contains(past, want) {
		t.Errorf("past month has no date line %q", want)
	}
	if !strings.Contains(past, money.FormatIDR(100_000)) || strings.Contains(past, money.FormatIDR(125_000)) {
		t.Error("past month's Saldo kas is not its month-end balance")
	}

	running := f.get(t, "/report/"+f.fund.ReportSlug).Body.String()
	if want := report.Text.AsOfRunning("2 Oktober 2026"); !strings.Contains(running, want) {
		t.Errorf("running month has no date line %q", want)
	}
	if nav, hero := strings.Index(running, `<nav class="months"`), strings.Index(running, `<section class="hero"`); nav < 0 || hero < 0 || nav > hero {
		t.Errorf("month picker at %d, summary at %d; want the picker first", nav, hero)
	}
}

// #498: the public page reads selisih for what a partial entry left open (the
// remainder, not the difference found), and cocok once an exact entry closes
// the line.
func TestReportReadsAPartialCekKasEntryAsSelisihForTheRemainder(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-09-03")
	today := reportNow.In(wib).Format("2006-01-02")

	take := func(amount money.Amount) {
		t.Helper()
		if _, err := f.l.TakeReconciliation(context.Background(), ledger.TakeReconciliationParams{
			FundID: f.fund.ID,
			Counts: []ledger.AccountCount{{
				AccountID: f.cashID, ActualAmount: 130_000, Resolution: "entry_added",
				Fix: &ledger.Fix{PurposeID: f.mainID, Direction: "in", Amount: amount, OccurredOn: today},
			}},
		}); err != nil {
			t.Fatalf("TakeReconciliation() = %v, want no error", err)
		}
	}

	take(20_000)
	body := f.get(t, "/report/"+f.fund.ReportSlug).Body.String()
	if want := report.Text.Discrepancy(money.FormatIDR(10_000)); !strings.Contains(body, want) {
		t.Errorf("report after a partial entry lacks %q:\n%s", want, body)
	}
	if strings.Contains(body, report.Text.Matched) || strings.Contains(body, money.FormatIDR(30_000)) {
		t.Error("report after a partial entry says cocok or shows the original 30000 difference")
	}

	take(10_000)
	body = f.get(t, "/report/"+f.fund.ReportSlug).Body.String()
	if !strings.Contains(body, report.Text.Matched) {
		t.Errorf("report after the exact entry does not say cocok:\n%s", body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
