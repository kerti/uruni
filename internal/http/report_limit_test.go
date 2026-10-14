package http

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// limitedReport is the full router (so the limiter sits where production puts
// it) with a fund, a pinned clock the test can move, and a log buffer.
type limitedReport struct {
	h    http.Handler
	slug string
	logs *bytes.Buffer
	now  *time.Time
}

func newLimitedReport(t *testing.T) limitedReport {
	t.Helper()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	sqlDB := testStoreDB(t)
	l := ledger.New(sqlDB)
	res, err := l.SetUpFund(context.Background(), ledger.SetUpFundParams{
		FundName: "Kas RT 05", Accounts: []ledger.AccountInput{{Kind: "cash", Name: "Tunai"}},
	})
	if err != nil {
		t.Fatalf("SetUpFund() = %v", err)
	}
	now := reportNow
	h := newWithClock(testAssets(), testBuild, l, store.New(sqlDB), sqlDB, nil, logger, auth.New(sqlDB), "", t.TempDir(), t.TempDir(), nil, func() time.Time { return now })
	return limitedReport{h: h, slug: res.Fund.ReportSlug, logs: logs, now: &now}
}

func (f limitedReport) get(path, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = ip + ":4000"
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func (f limitedReport) page(ip string) *httptest.ResponseRecorder {
	return f.get("/report/"+f.slug, ip)
}

func (f limitedReport) pdf(ip string) *httptest.ResponseRecorder {
	return f.get("/report/"+f.slug+"/pdf", ip)
}

func TestReportHTMLIsLimitedPerClientWithAnIndonesianPage(t *testing.T) {
	t.Parallel()
	f := newLimitedReport(t)

	for i := range reportHTMLLimit {
		if rec := f.page("192.0.2.1"); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i+1, rec.Code)
		}
	}
	f.logs.Reset()
	rec := f.page("192.0.2.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request %d = %d, want 429", reportHTMLLimit+1, rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60 (the whole window, nothing has elapsed)", got)
	}
	body := rec.Body.String()
	for _, want := range []string{"Laporan sedang ramai dibuka", "Coba lagi sebentar lagi, ya.", `<meta name="robots" content="noindex, nofollow">`} {
		if !strings.Contains(body, want) {
			t.Errorf("429 body lacks %q:\n%s", want, body)
		}
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("X-Robots-Tag = %q, want noindex, nofollow", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the report's html", got)
	}
	if strings.Contains(body, f.slug) {
		t.Error("429 body names the slug")
	}

	// Time passes, no sleep: the window ends and the client is admitted again.
	*f.now = f.now.Add(reportLimitWindow)
	if rec := f.page("192.0.2.1"); rec.Code != http.StatusOK {
		t.Errorf("after the window = %d, want 200", rec.Code)
	}
}

func TestReportPDFLimitTripsBeforeHTMLsAndOwnsItsBucket(t *testing.T) {
	t.Parallel()
	f := newLimitedReport(t)

	for i := range reportPDFLimit {
		if rec := f.pdf("192.0.2.1"); rec.Code != http.StatusOK {
			t.Fatalf("pdf %d = %d, want 200", i+1, rec.Code)
		}
	}
	rec := f.pdf("192.0.2.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("pdf %d = %d, want 429", reportPDFLimit+1, rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "Coba lagi sebentar lagi, ya.") {
		t.Errorf("pdf 429 lacks Retry-After or the page:\n%s", rec.Body.String())
	}
	// The page is a separate bucket: the PDF's twenty did not spend its budget.
	if rec := f.page("192.0.2.1"); rec.Code != http.StatusOK {
		t.Errorf("page after a full PDF bucket = %d, want 200", rec.Code)
	}
}

func TestReportLimitsAreSeparatePerClientAndCoverUnknownSlugs(t *testing.T) {
	t.Parallel()
	f := newLimitedReport(t)

	for range reportPDFLimit {
		f.pdf("192.0.2.1")
	}
	if rec := f.pdf("192.0.2.1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("first client = %d, want 429", rec.Code)
	}
	if rec := f.pdf("192.0.2.2"); rec.Code != http.StatusOK {
		t.Errorf("another client = %d, want 200: budgets are per client", rec.Code)
	}

	// A probe of slugs that do not exist is counted before the lookup.
	for range reportHTMLLimit {
		f.get("/report/no-such-slug", "192.0.2.3")
	}
	if rec := f.get("/report/no-such-slug", "192.0.2.3"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("unknown-slug probe = %d, want 429", rec.Code)
	}
}

func TestReportRateLimitedSecurityEvent(t *testing.T) {
	t.Parallel()
	f := newLimitedReport(t)

	for range reportPDFLimit {
		f.pdf("192.0.2.1")
	}
	f.logs.Reset()
	f.pdf("192.0.2.1")
	line := logLine(t, f.logs, "event=report_rate_limited")
	for _, want := range []string{"level=WARN", "ip=192.0.2.1", "kind=pdf", "request_id="} {
		if !strings.Contains(line, want) {
			t.Errorf("event line lacks %q: %s", want, line)
		}
	}

	for range reportHTMLLimit {
		f.page("192.0.2.9")
	}
	f.logs.Reset()
	f.page("192.0.2.9")
	logLine(t, f.logs, "event=report_rate_limited", "kind=html", "ip=192.0.2.9")

	if strings.Contains(f.logs.String(), f.slug) {
		t.Errorf("a log line carries the slug:\n%s", f.logs.String())
	}
}

// The fonts are read from the embedded files once per process, however many
// PDFs are drawn, concurrently or not (#520). The race detector is the other
// half of the test: the shared bytes are only read.
func TestPDFFontsAreReadOnceAcrossConcurrentRenders(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 50_000, "2026-10-01")

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if rec := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf"); rec.Code != http.StatusOK {
				t.Errorf("pdf = %d, want 200", rec.Code)
			}
		})
	}
	wg.Wait()
	if got := pdfFontReads.Load(); got != 1 {
		t.Errorf("font files read %d times, want 1", got)
	}
}
