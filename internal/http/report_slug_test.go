package http

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// authedRouterWithBaseURL is authedRouterFor with a real base URL, so the
// report_url the API builds can be asserted on.
func authedRouterWithBaseURL(t *testing.T, sqlDB *sql.DB, baseURL string) http.Handler {
	t.Helper()
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), baseURL, t.TempDir(), t.TempDir(), nil)
	reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if reg.Code != http.StatusCreated {
		t.Fatalf("fixture POST /api/register = %d, want %d", reg.Code, http.StatusCreated)
	}
	return withSessionCookie{Handler: r, token: sessionCookie(reg)}
}

func postReplaceSlug(r http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/fund/report-slug", nil))
	return rec
}

func TestPostReportSlugReplacesTheLinkAndTheOldOneIs404(t *testing.T) {
	t.Parallel()
	r := authedRouterWithBaseURL(t, testStoreDB(t), "https://kas.example.org/")
	old := setUpFund(t, r).Fund

	if old.ReportURL != "https://kas.example.org/report/"+old.ReportSlug {
		t.Errorf("setup report_url = %q, want base + /report/ + slug", old.ReportURL)
	}
	before := httptest.NewRecorder()
	r.ServeHTTP(before, httptest.NewRequest(http.MethodGet, "/report/"+old.ReportSlug, nil))
	if before.Code != http.StatusOK {
		t.Fatalf("GET /report/{old} before = %d, want 200", before.Code)
	}

	rec := postReplaceSlug(r)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/fund/report-slug = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got fundResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.ID != old.ID || got.Name != old.Name {
		t.Errorf("response fund = %+v, want the same fund as %+v", got, old)
	}
	if got.ReportSlug == old.ReportSlug {
		t.Errorf("slug = %q, want it to differ from the old one", got.ReportSlug)
	}
	if len(got.ReportSlug) < 22 {
		t.Errorf("len(slug) = %d, want at least the schema's 22", len(got.ReportSlug))
	}
	if got.ReportURL != "https://kas.example.org/report/"+got.ReportSlug {
		t.Errorf("report_url = %q, want base (no double slash) + /report/ + slug", got.ReportURL)
	}

	for slug, want := range map[string]int{old.ReportSlug: http.StatusNotFound, got.ReportSlug: http.StatusOK} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/report/"+slug, nil))
		if rec.Code != want {
			t.Errorf("GET /report/%s = %d, want %d", slug, rec.Code, want)
		}
	}

	// And GET /api/fund is what the next reader sees.
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	var read fundResponse
	if err := json.NewDecoder(getRec.Body).Decode(&read); err != nil {
		t.Fatalf("decoding GET response: %v", err)
	}
	if read != got {
		t.Errorf("GET /api/fund = %+v, want %+v", read, got)
	}
}

func TestPostReportSlugBeforeSetupIs404(t *testing.T) {
	t.Parallel()
	if rec := postReplaceSlug(testRouter(t)); rec.Code != http.StatusNotFound {
		t.Errorf("POST /api/fund/report-slug before setup = %d, want 404", rec.Code)
	}
}

func TestReportURLIsABarePathWithoutABaseURL(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	fund := setUpFund(t, r).Fund
	if fund.ReportURL != "/report/"+fund.ReportSlug {
		t.Errorf("report_url = %q, want the bare path", fund.ReportURL)
	}
}
