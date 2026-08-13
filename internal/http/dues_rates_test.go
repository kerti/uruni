package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postDuesRate(t *testing.T, r http.Handler, tierID int64, req duesRateRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling dues rate request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-tiers/%d/rates", tierID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	return rec
}

func getDuesRates(t *testing.T, r http.Handler, tierIDPathSegment string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-tiers/%s/rates", tierIDPathSegment)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// setUpTier is the fixture every dues-rate test needs: a fund, then a tier
// to hang rates off of.
func setUpTier(t *testing.T, r http.Handler, name string) duesTierResponse {
	t.Helper()
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}
	rec := postDuesTier(t, r, name)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-tiers = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var tier duesTierResponse
	if err := json.NewDecoder(rec.Body).Decode(&tier); err != nil {
		t.Fatalf("decoding dues tier response: %v", err)
	}
	return tier
}

func TestPostDuesRatesCreatesAndListReturnsIt(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var created duesRateResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if created.ID == 0 {
		t.Error("dues_rate.id is zero")
	}
	if created.TierID != tier.ID {
		t.Errorf("dues_rate.tier_id = %d, want %d", created.TierID, tier.ID)
	}
	if created.Amount != 50_000 {
		t.Errorf("dues_rate.amount = %d, want %d", created.Amount, 50_000)
	}
	if created.EffectiveFrom != "2026-01" {
		t.Errorf("dues_rate.effective_from = %q, want %q", created.EffectiveFrom, "2026-01")
	}

	list := getDuesRates(t, r, fmt.Sprintf("%d", tier.ID))
	if list.Code != http.StatusOK {
		t.Fatalf("GET .../rates = %d, want %d (body: %s)", list.Code, http.StatusOK, list.Body.String())
	}
	var rates []duesRateResponse
	if err := json.NewDecoder(list.Body).Decode(&rates); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, list.Body.String())
	}
	if len(rates) != 1 || rates[0] != created {
		t.Errorf("GET .../rates = %+v, want [%+v]", rates, created)
	}
}

func TestPostDuesRatesRejectsADuplicatePeriodWith409(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("first POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// dues_rate's own UNIQUE (tier_id, effective_from) - a second rate for
	// the same tier and period is a correction, and PRD §6 says the way to
	// correct it is a *different* effective_from, not this one repeated.
	rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 75_000, EffectiveFrom: "2026-01"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second POST .../rates with a duplicate period = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "unique_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "unique_violation")
	}
}

func TestPostDuesRatesRejectsAMalformedEffectiveFrom(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	// dues_rate.effective_from CHECK - GLOB 'YYYY-MM' and a real calendar
	// month. "2026-13" matches the GLOB shape but names no such month.
	rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-13"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST .../rates with effective_from=2026-13 = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

func TestPostDuesRatesReturns404ForAnUnknownTierID(t *testing.T) {
	r := testRouter(t)
	setUpTier(t, r, "Full")

	rec := postDuesRate(t, r, 999, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST .../rates for an unknown tier = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostDuesRatesReturns400ForANonNumericTierID(t *testing.T) {
	r := testRouter(t)
	setUpTier(t, r, "Full")

	body, err := json.Marshal(duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"})
	if err != nil {
		t.Fatalf("marshaling dues rate request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/dues-tiers/abc/rates", bytes.NewReader(body)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST .../rates with a non-numeric tier id = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestGetDuesRatesReturnsAnEmptyListForATierWithNoRateYet(t *testing.T) {
	// dues_rate.sql's own comment: "a tier whose rate is undecided simply
	// has no row" - a legitimate state, not an error.
	r := testRouter(t)
	tier := setUpTier(t, r, "Madya")

	rec := getDuesRates(t, r, fmt.Sprintf("%d", tier.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET .../rates for a tier with no rate = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "[]\n" {
		t.Errorf("GET .../rates body = %q, want %q", got, "[]\n")
	}
}

func TestGetDuesRatesReturns404ForAnUnknownTierID(t *testing.T) {
	r := testRouter(t)
	setUpTier(t, r, "Full")

	rec := getDuesRates(t, r, "999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET .../rates for an unknown tier = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetDuesRatesReturns400ForANonNumericTierID(t *testing.T) {
	r := testRouter(t)
	setUpTier(t, r, "Full")

	rec := getDuesRates(t, r, "abc")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET .../rates with a non-numeric tier id = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostDuesRatesRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-tiers/%d/rates", tier.ID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte("{oops"))))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST .../rates with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}
