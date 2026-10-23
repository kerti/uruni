package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func getDuesStatusGrid(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dues-status/grid", nil))
	return rec
}

func TestGetDuesStatusGridRequiresAFund(t *testing.T) {
	t.Parallel()
	rec := getDuesStatusGrid(t, testRouter(t))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/dues-status/grid before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// Nobody owes anything: both lists are present and empty, never null, so the
// client does not branch on a missing key.
func TestGetDuesStatusGridOfAFundWhereNobodyOwesIsEmpty(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	if rec := postMember(t, r, memberRequest{Name: "No Tier"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := getDuesStatusGrid(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dues-status/grid = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got, want := rec.Body.String(), "{\"periods\":[],\"members\":[]}\n"; got != want {
		t.Errorf("GET /api/dues-status/grid = %q, want %q", got, want)
	}
}

// The window ends at the fund's current month, read from the server's clock in
// the fund's zone; the request names no month.
func TestGetDuesStatusGridReadsTheFundsCurrentMonth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 15, 5, 0, 0, 0, time.UTC)
	r := authedRouterAt(t, testStoreDB(t), func() time.Time { return now })
	setup := setUpFund(t, r)

	tierRec := postDuesTier(t, r, "Full")
	if tierRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-tiers = %d, want %d (body: %s)", tierRec.Code, http.StatusCreated, tierRec.Body.String())
	}
	var tier duesTierResponse
	if err := json.NewDecoder(tierRec.Body).Decode(&tier); err != nil {
		t.Fatalf("decoding dues tier response: %v", err)
	}
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 25_000, EffectiveFrom: "2020-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	joined := "2026-08-10"
	members := map[string]memberResponse{}
	for _, name := range []string{"Sari", "Budi"} {
		rec := postMember(t, r, memberRequest{Name: name, TierID: &tier.ID, JoinedOn: &joined})
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
		}
		var m memberResponse
		if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
			t.Fatalf("decoding member response: %v", err)
		}
		members[name] = m
	}
	budi := members["Budi"]
	if rec := postDuesPayment(t, r, duesPaymentRequest{
		AccountID: setup.CashAccountID(t), MemberID: budi.ID, OccurredOn: "2026-10-05",
		Periods: []duesPaymentPeriod{{DuesPeriod: "2026-08", Amount: 25_000}, {DuesPeriod: "2026-09", Amount: 10_000}},
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-payments = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	read := func() duesGridResponse {
		t.Helper()
		rec := getDuesStatusGrid(t, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/dues-status/grid = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got duesGridResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
		}
		return got
	}

	got := read()
	if want := []string{"2026-08", "2026-09", "2026-10"}; !reflect.DeepEqual(got.Periods, want) {
		t.Errorf("periods = %v, want %v", got.Periods, want)
	}
	// By name, as every member list reads.
	if len(got.Members) != 2 || got.Members[0].Name != "Budi" || got.Members[1].Name != "Sari" {
		t.Fatalf("members = %+v, want Budi then Sari", got.Members)
	}
	if want := []string{"paid", "partial", "unpaid"}; !reflect.DeepEqual(got.Members[0].Cells, want) {
		t.Errorf("Budi cells = %v, want %v", got.Members[0].Cells, want)
	}
	if want := []string{"unpaid", "unpaid", "unpaid"}; !reflect.DeepEqual(got.Members[1].Cells, want) {
		t.Errorf("Sari cells = %v, want %v", got.Members[1].Cells, want)
	}
	if got.Members[0].ID != budi.ID {
		t.Errorf("Budi id = %d, want %d", got.Members[0].ID, budi.ID)
	}

	// A month on, the window follows the clock: the client sent nothing.
	now = time.Date(2026, time.November, 2, 5, 0, 0, 0, time.UTC)
	if got := read(); got.Periods[len(got.Periods)-1] != "2026-11" || len(got.Members[0].Cells) != 4 {
		t.Errorf("after the month turned: periods %v, Budi cells %v, want a window ending 2026-11", got.Periods, got.Members[0].Cells)
	}
}
