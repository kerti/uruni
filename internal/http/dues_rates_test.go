package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

func patchDuesRate(t *testing.T, r http.Handler, id int64, amount int64) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(updateDuesRateRequest{Amount: &amount})
	if err != nil {
		t.Fatalf("marshaling update dues rate request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-rates/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body)))
	return rec
}

func deleteDuesRate(t *testing.T, r http.Handler, id int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-rates/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	return rec
}

func TestPatchDuesRatesCorrectsTheAmount(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	created := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"})
	if created.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", created.Code, http.StatusCreated, created.Body.String())
	}
	var rate duesRateResponse
	if err := json.NewDecoder(created.Body).Decode(&rate); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	rec := patchDuesRate(t, r, rate.ID, 75_000)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/dues-rates/%d = %d, want %d (body: %s)", rate.ID, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got duesRateResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if got.Amount != 75_000 {
		t.Errorf("dues_rate.amount = %d, want %d", got.Amount, 75_000)
	}
	if got.EffectiveFrom != "2026-01" {
		t.Errorf("dues_rate.effective_from = %q, want %q (unchanged)", got.EffectiveFrom, "2026-01")
	}
}

// A body carrying no amount must be refused rather than read as zero.
// CHECK (amount >= 0) admits 0, so decoding an absent key to the zero value
// would silently make the tier free and read every derived dues status for
// the periods that rate covers as paid - the exact corruption this route
// exists to let a treasurer fix. Covers both an empty body and a misspelt
// key, which decode identically.
func TestPatchDuesRatesRejectsABodyWithNoAmount(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"misspelt key": `{"amout":75000}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := testRouter(t)
			tier := setUpTier(t, r, "Full")
			created := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"})
			var rate duesRateResponse
			if err := json.NewDecoder(created.Body).Decode(&rate); err != nil {
				t.Fatalf("decoding response: %v", err)
			}

			rec := httptest.NewRecorder()
			path := fmt.Sprintf("/api/dues-rates/%d", rate.ID)
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PATCH %s with %s = %d, want %d (body: %s)", path, name, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "invalid_argument" {
				t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
			}

			// The rate must be untouched, not zeroed.
			rates := getDuesRates(t, r, strconv.FormatInt(tier.ID, 10))
			var got []duesRateResponse
			if err := json.NewDecoder(rates.Body).Decode(&got); err != nil {
				t.Fatalf("decoding rates: %v", err)
			}
			if len(got) != 1 || got[0].Amount != 50_000 {
				t.Errorf("rates after refused PATCH = %+v, want the original 50000 unchanged", got)
			}
		})
	}
}

func TestPatchDuesRatesReturns404ForAnUnknownID(t *testing.T) {
	r := testRouter(t)
	setUpTier(t, r, "Full")

	rec := patchDuesRate(t, r, 999, 75_000)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH /api/dues-rates/999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPatchDuesRatesReturns400ForANonNumericID(t *testing.T) {
	r := testRouter(t)
	setUpTier(t, r, "Full")

	amount := int64(75_000)
	body, _ := json.Marshal(updateDuesRateRequest{Amount: &amount})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/dues-rates/abc", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/dues-rates/abc = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPatchDuesRatesRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	created := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"})
	var rate duesRateResponse
	if err := json.NewDecoder(created.Body).Decode(&rate); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-rates/%d", rate.ID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader([]byte("{oops"))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/dues-rates/%d with malformed JSON = %d, want %d", rate.ID, rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// TestDeleteDuesRatesThenRepostingForTheRightMonthSucceeds is the concrete
// case that motivated the slice: a rate entered against the wrong month,
// which UNIQUE (tier_id, effective_from) otherwise makes permanently
// uncorrectable, is deleted and re-added for the right one.
func TestDeleteDuesRatesThenRepostingForTheRightMonthSucceeds(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	wrong := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-02"})
	if wrong.Code != http.StatusCreated {
		t.Fatalf("POST .../rates = %d, want %d (body: %s)", wrong.Code, http.StatusCreated, wrong.Body.String())
	}
	var wrongRate duesRateResponse
	if err := json.NewDecoder(wrong.Body).Decode(&wrongRate); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	del := deleteDuesRate(t, r, wrongRate.ID)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/dues-rates/%d = %d, want %d (body: %s)", wrongRate.ID, del.Code, http.StatusNoContent, del.Body.String())
	}

	// The corrected row for the intended month - 2026-01, not 2026-02 -
	// would previously never even reach here, since it isn't a duplicate of
	// the deleted row's period. Posted anyway, to prove the delete really
	// freed the tier up rather than merely accepting an unrelated period.
	right := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"})
	if right.Code != http.StatusCreated {
		t.Fatalf("POST .../rates for the corrected month = %d, want %d (body: %s)", right.Code, http.StatusCreated, right.Body.String())
	}

	list := getDuesRates(t, r, fmt.Sprintf("%d", tier.ID))
	var rates []duesRateResponse
	if err := json.NewDecoder(list.Body).Decode(&rates); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(rates) != 1 || rates[0].EffectiveFrom != "2026-01" {
		t.Errorf("GET .../rates = %+v, want a single rate effective 2026-01", rates)
	}
}

func TestDeleteDuesRatesReturns404ForAnUnknownID(t *testing.T) {
	r := testRouter(t)
	setUpTier(t, r, "Full")

	rec := deleteDuesRate(t, r, 999)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE /api/dues-rates/999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestDeleteDuesRatesReturns400ForANonNumericID(t *testing.T) {
	r := testRouter(t)
	setUpTier(t, r, "Full")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/dues-rates/abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("DELETE /api/dues-rates/abc = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
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
