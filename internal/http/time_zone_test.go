package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// ADR-041: the fund's time zone is set at Setup, changed with PATCH /api/fund,
// and read by every route that reckons a day or a month. The instant the three
// zones disagree about is 22:30 WIB on a month's last day: 23:30 WITA, 00:30 WIT
// the next day. Tests name instants by an explicit wall clock; CI runs in UTC.

var (
	// 22:30 WIB on 31 August 2026: still August for WIB and WITA, already
	// 1 September for WIT.
	lateEveningWIB = time.Date(2026, 8, 31, 22, 30, 0, 0, wib)
	// 23:30 WIB: already 1 September for WITA too.
	lastHourWIB = time.Date(2026, 8, 31, 23, 30, 0, 0, wib)
)

func postSetupJSON(t *testing.T, r http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body)))
	return rec
}

func getFundJSON(t *testing.T, r http.Handler) fundResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/fund = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var f fundResponse
	if err := json.NewDecoder(rec.Body).Decode(&f); err != nil {
		t.Fatalf("decoding fund: %v", err)
	}
	return f
}

// setFundZone changes the zone through the route the treasurer uses.
func setFundZone(t *testing.T, r http.Handler, zone string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"time_zone": zone})
	if rec := patchFund(t, r, string(body)); rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/fund time_zone=%q = %d, want 200 (body: %s)", zone, rec.Code, rec.Body.String())
	}
}

// POST /api/setup: time_zone is optional and absent means WIB; a named zone is
// stored and echoed; anything else is refused by the schema's CHECK (ADR-027)
// and leaves no fund behind.
func TestPostSetupTakesTheTimeZone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ body, want string }{
		{`{"name":"Dana","accounts":[{"kind":"cash","name":"Tunai"}]}`, tz.Jakarta},
		{`{"name":"Dana","time_zone":"Asia/Jakarta","accounts":[{"kind":"cash","name":"Tunai"}]}`, tz.Jakarta},
		{`{"name":"Dana","time_zone":"Asia/Makassar","accounts":[{"kind":"cash","name":"Tunai"}]}`, tz.Makassar},
		{`{"name":"Dana","time_zone":"Asia/Jayapura","accounts":[{"kind":"cash","name":"Tunai"}]}`, tz.Jayapura},
	} {
		r := testRouter(t)
		rec := postSetupJSON(t, r, tc.body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/setup %s = %d, want 201 (body: %s)", tc.body, rec.Code, rec.Body.String())
		}
		var got setupResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Fund.TimeZone != tc.want {
			t.Errorf("setup response fund.time_zone = %q, want %q", got.Fund.TimeZone, tc.want)
		}
		if f := getFundJSON(t, r); f.TimeZone != tc.want {
			t.Errorf("GET /api/fund time_zone = %q, want %q", f.TimeZone, tc.want)
		}
	}
}

func TestPostSetupRefusesAZoneOutsideTheThree(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{"UTC", "Asia/Tokyo", "WIB", "asia/jakarta", "Asia/Pontianak", " Asia/Jakarta"} {
		r := testRouter(t)
		rec := postSetupJSON(t, r, `{"name":"Dana","time_zone":"`+zone+`","accounts":[{"kind":"cash","name":"Tunai"}]}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST /api/setup time_zone=%q = %d, want 400 (body: %s)", zone, rec.Code, rec.Body.String())
		}
		get := httptest.NewRecorder()
		r.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
		if get.Code != http.StatusNotFound {
			t.Errorf("after a refused setup (%q), GET /api/fund = %d, want 404: no fund may be left behind", zone, get.Code)
		}
	}
}

// PATCH /api/fund takes name and time_zone, each optional, at least one
// required. Either alone leaves the other as it was; both change both.
func TestPatchFundTakesEitherFieldOrBoth(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	var rec *httptest.ResponseRecorder
	rec = patchFund(t, r, `{"time_zone":"Asia/Jayapura"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH time_zone only = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got fundResponse
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.TimeZone != tz.Jayapura || got.Name != "Test Fund" {
		t.Errorf("after PATCH time_zone only: name=%q time_zone=%q, want the name kept and WIT", got.Name, got.TimeZone)
	}

	rec = patchFund(t, r, `{"name":"Kas Ruang 3B"}`)
	got = fundResponse{}
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if rec.Code != http.StatusOK || got.Name != "Kas Ruang 3B" || got.TimeZone != tz.Jayapura {
		t.Errorf("after PATCH name only: %d name=%q time_zone=%q, want the zone kept", rec.Code, got.Name, got.TimeZone)
	}

	rec = patchFund(t, r, `{"name":"Kas 3B","time_zone":"Asia/Makassar"}`)
	got = fundResponse{}
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if rec.Code != http.StatusOK || got.Name != "Kas 3B" || got.TimeZone != tz.Makassar {
		t.Errorf("after PATCH both: %d name=%q time_zone=%q", rec.Code, got.Name, got.TimeZone)
	}
	if f := getFundJSON(t, r); f.Name != "Kas 3B" || f.TimeZone != tz.Makassar {
		t.Errorf("GET /api/fund = %q/%q, want what the PATCH wrote", f.Name, f.TimeZone)
	}
}

func TestPatchFundRefusesAnEmptyBodyAndAZoneOutsideTheThree(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	setFundZone(t, r, tz.Makassar)

	if rec := patchFund(t, r, `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH {} = %d, want 400", rec.Code)
	}
	for _, zone := range []string{"", "UTC", "Asia/Tokyo", "WITA", "asia/makassar"} {
		rec := patchFund(t, r, `{"time_zone":"`+zone+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH time_zone=%q = %d, want 400 (body: %s)", zone, rec.Code, rec.Body.String())
		}
	}
	// A refused zone changes nothing, the name sent with it included.
	if rec := patchFund(t, r, `{"name":"Tak Jadi","time_zone":"UTC"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH name + bad zone = %d, want 400", rec.Code)
	}
	if f := getFundJSON(t, r); f.TimeZone != tz.Makassar || f.Name != "Test Fund" {
		t.Errorf("after refused PATCHes: %q/%q, want the fund untouched (Test Fund, WITA)", f.Name, f.TimeZone)
	}
}

// Every answer that carries the fund carries its zone: the SPA sets its client
// zone from whichever it called last (ADR-041).
func TestEveryFundAnswerCarriesTheZone(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	setFundZone(t, r, tz.Jayapura)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/fund/report-slug", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/fund/report-slug = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got fundResponse
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.TimeZone != tz.Jayapura {
		t.Errorf("report-slug answer time_zone = %q, want %q", got.TimeZone, tz.Jayapura)
	}
}

func decodeMemberBody(t *testing.T, rec *httptest.ResponseRecorder) memberResponse {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var m memberResponse
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

// POST /api/members with no joined_on dates the member on the fund's day: the
// same instant is 31 August in WIB and WITA at 22:30 WIB, and 1 September in WIT.
func TestPostMembersDefaultsJoinedOnToTheFundsDayInItsZone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		zone string
		now  time.Time
		want string
	}{
		{tz.Jakarta, lateEveningWIB, "2026-08-31"},
		{tz.Makassar, lateEveningWIB, "2026-08-31"},
		{tz.Jayapura, lateEveningWIB, "2026-09-01"},
		{tz.Jakarta, lastHourWIB, "2026-08-31"},
		{tz.Makassar, lastHourWIB, "2026-09-01"},
		{tz.Jayapura, lastHourWIB, "2026-09-01"},
	}
	for _, tc := range cases {
		t.Run(tc.zone+"/"+tc.now.Format("15:04"), func(t *testing.T) {
			t.Parallel()
			r := authedRouterAt(t, testStoreDB(t), fixedClock(tc.now))
			if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
				t.Fatalf("POST /api/setup = %d", rec.Code)
			}
			setFundZone(t, r, tc.zone)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/members", strings.NewReader(`{"name":"Jane"}`)))
			if got := decodeMemberBody(t, rec).JoinedOn; got != tc.want {
				t.Errorf("member.joined_on = %q, want %q", got, tc.want)
			}
		})
	}
}

// The members list and a member's outstanding dues both read the fund's current
// month: at 22:30 WIB on 31 August a WIT fund is already in September, where the
// August dues are one month behind.
func TestMembersAndOutstandingDuesReadTheFundsMonth(t *testing.T) {
	t.Parallel()
	cases := []struct {
		zone        string
		now         time.Time
		wantRate    int64
		wantArrears int
		wantPeriods string
	}{
		{tz.Jakarta, lateEveningWIB, 25_000, 0, "2026-08"},
		{tz.Makassar, lateEveningWIB, 25_000, 0, "2026-08"},
		{tz.Jayapura, lateEveningWIB, 30_000, 1, "2026-08,2026-09"},
		{tz.Makassar, lastHourWIB, 30_000, 1, "2026-08,2026-09"},
	}
	for _, tc := range cases {
		t.Run(tc.zone+"/"+tc.now.Format("15:04"), func(t *testing.T) {
			t.Parallel()
			r := authedRouterAt(t, testStoreDB(t), fixedClock(tc.now))
			member := clockFixture(t, r)
			setFundZone(t, r, tc.zone)

			page := decodeMembersPage(t, getMembers(t, r, ""))
			got := page.Members[0]
			if got.CurrentRate == nil || *got.CurrentRate != tc.wantRate || got.ArrearsMonths != tc.wantArrears {
				t.Errorf("current_rate/arrears_months = %v/%d, want %d/%d", got.CurrentRate, got.ArrearsMonths, tc.wantRate, tc.wantArrears)
			}

			rec := getOutstandingDues(t, r, member.ID, "")
			var periods []outstandingDuesResponse
			if err := json.NewDecoder(rec.Body).Decode(&periods); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			var names []string
			for _, p := range periods {
				names = append(names, p.Period)
			}
			if strings.Join(names, ",") != tc.wantPeriods {
				t.Errorf("outstanding periods = %v, want %s", names, tc.wantPeriods)
			}
		})
	}
}

// A direct GET /api/backup names the zip with the fund's day, not the server's
// and not Jakarta's.
func TestDownloadBackupNamesTheZipWithTheFundsDate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		zone string
		now  time.Time
		want string
	}{
		{tz.Jakarta, lateEveningWIB, "uruni-2026-08-31.zip"},
		{tz.Makassar, lateEveningWIB, "uruni-2026-08-31.zip"},
		{tz.Jayapura, lateEveningWIB, "uruni-2026-09-01.zip"},
	}
	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			t.Parallel()
			r := authedRouterAt(t, testStoreDB(t), fixedClock(tc.now))
			setUpFund(t, r)
			setFundZone(t, r, tc.zone)
			rec := getBackup(t, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /api/backup = %d", rec.Code)
			}
			if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="`+tc.want+`"`; got != want {
				t.Errorf("Content-Disposition = %q, want %q", got, want)
			}
		})
	}
}

// Beranda's "Bulan ini" follows the fund's zone: the same instant is a different
// running month for a WIT fund.
func TestGetBalancesMonthFollowsTheFundsZone(t *testing.T) {
	t.Parallel()
	for zone, wantIn := range map[string]int64{tz.Jakarta: 57_000, tz.Jayapura: 7_000} {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()
			r := authedRouterAt(t, testStoreDB(t), fixedClock(lateEveningWIB))
			setup := setUpFund(t, r)
			// 1 September is "today" only for a WIT fund at this instant (#632's ceiling
			// refuses it for a WIB one), so the rows are posted in WIT and the fund moved
			// to the zone under test afterwards: the row left a day ahead of a WIB fund's
			// today is exactly what a zone change leaves (ADR-041), and the month still
			// counts it.
			setFundZone(t, r, tz.Jayapura)
			cash, main := setup.Accounts[0].ID, setup.MainPurposeID
			for _, tx := range []struct {
				amount int64
				on     string
			}{{50_000, "2026-08-15"}, {7_000, "2026-09-01"}} {
				body, _ := json.Marshal(map[string]any{"account_id": cash, "purpose_id": main, "direction": "in", "amount": tx.amount, "occurred_on": tx.on})
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/transactions", bytes.NewReader(body)))
				if rec.Code != http.StatusCreated {
					t.Fatalf("POST /api/transactions = %d (body: %s)", rec.Code, rec.Body.String())
				}
			}
			setFundZone(t, r, zone)
			rec := getBalances(t, r)
			var got balancesResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.Month.In != wantIn {
				t.Errorf("month.in = %d, want %d", got.Month.In, wantIn)
			}
		})
	}
}

// The unauthenticated report reads the zone from the fund row the slug names, not
// from a request: at 22:30 WIB on 31 August it is "per 31 Agustus 2026" for a WIB
// fund and "per 1 September 2026" for a WIT one, in the page and in the month
// picker's last option (ADR-041).
func TestPublicReportIsDatedInTheFundsZone(t *testing.T) {
	t.Parallel()
	for zone, want := range map[string]struct{ asOf, lastMonth string }{
		tz.Jakarta:  {"per 31 Agustus 2026", "2026-08"},
		tz.Jayapura: {"per 1 September 2026", "2026-09"},
	} {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()
			sqlDB := testStoreDB(t)
			l := ledger.New(sqlDB)
			res, err := l.SetUpFund(context.Background(), ledger.SetUpFundParams{
				FundName: "Kas RT 05", TimeZone: zone, Accounts: []ledger.AccountInput{{Kind: "cash", Name: "Tunai"}},
			})
			if err != nil {
				t.Fatalf("SetUpFund() = %v", err)
			}
			rt := chi.NewRouter()
			rt.Get("/report/{slug}", reportHandler(l, store.New(sqlDB), testLogger(), fixedClock(lateEveningWIB)))
			rec := httptest.NewRecorder()
			rt.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/report/"+res.Fund.ReportSlug, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /report/{slug} = %d (body: %s)", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, want.asOf) {
				t.Errorf("report lacks %q:\n%s", want.asOf, body)
			}
			if got := selectedMonth(t, body); got != want.lastMonth {
				t.Errorf("selected month = %q, want %q", got, want.lastMonth)
			}
		})
	}
}
