package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// listDuesTiers is GET /api/dues-tiers, decoded: every tier with its rates.
func listDuesTiers(t *testing.T, r http.Handler) []duesTierListItem {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dues-tiers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dues-tiers = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var tiers []duesTierListItem
	if err := json.NewDecoder(rec.Body).Decode(&tiers); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	return tiers
}

// listedRates is one tier's rates as GET /api/dues-tiers lists them.
func listedRates(t *testing.T, r http.Handler, tierID int64) []duesRateResponse {
	t.Helper()
	for _, tier := range listDuesTiers(t, r) {
		if tier.ID == tierID {
			return tier.Rates
		}
	}
	t.Fatalf("GET /api/dues-tiers does not list tier %d", tierID)
	return nil
}

// setUpTier is the fixture every dues-rate test needs: a fund, then a tier to
// hang rates off.
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
	t.Parallel()
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

	rates := listedRates(t, r, tier.ID)
	if len(rates) != 1 || rates[0] != created {
		t.Errorf("GET .../rates = %+v, want [%+v]", rates, created)
	}
}

func TestPostDuesRatesRejectsADuplicatePeriodWith409(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("first POST .../rates = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// UNIQUE (tier_id, effective_from): a second rate for the same tier and period
	// is a correction, and PRD section 6 corrects it with a different
	// effective_from, not this one repeated.
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
	t.Parallel()
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	// "2026-13" has the shape of a month but names none; the ledger refuses it as
	// an invalid argument (#632), ahead of the schema's CHECK, which stays the
	// backstop.
	rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-13"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST .../rates with effective_from=2026-13 = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPostDuesRatesReturns404ForAnUnknownTierID(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestGetDuesTiersListsATierWithNoRateYetWithAnEmptyList(t *testing.T) {
	t.Parallel()
	// dues_rate.sql: "a tier whose rate is undecided simply has no row", a
	// legitimate state, not an error - and on the wire it is [], never null.
	r := testRouter(t)
	setUpTier(t, r, "Senior")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dues-tiers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/dues-tiers = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"rates":[]`) {
		t.Errorf("GET /api/dues-tiers body = %q, want the tier with \"rates\":[]", got)
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
	t.Parallel()
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

// A body with no amount must be refused, not read as zero. CHECK (amount >= 0)
// admits 0, so decoding an absent key to zero would make the tier free and read
// every derived dues status for the periods that rate covers as paid: the
// corruption this route exists to let a treasurer fix. Covers an empty body
// and a misspelt key, which decode identically.
func TestPatchDuesRatesRejectsABodyWithNoAmount(t *testing.T) {
	t.Parallel()
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
			got := listedRates(t, r, tier.ID)
			if len(got) != 1 || got[0].Amount != 50_000 {
				t.Errorf("rates after refused PATCH = %+v, want the original 50000 unchanged", got)
			}
		})
	}
}

func TestPatchDuesRatesReturns404ForAnUnknownID(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// TestDeleteDuesRatesThenRepostingForTheRightMonthSucceeds is the case that
// motivated the slice: a rate entered against the wrong month, which UNIQUE
// (tier_id, effective_from) otherwise makes uncorrectable, is deleted and
// re-added for the right one.
func TestDeleteDuesRatesThenRepostingForTheRightMonthSucceeds(t *testing.T) {
	t.Parallel()
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

	// The corrected row for the intended month (2026-01, not 2026-02) would never
	// have reached here, being no duplicate of the deleted row's period. Posted to
	// prove the delete freed the tier, not merely accepted an unrelated period.
	right := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"})
	if right.Code != http.StatusCreated {
		t.Fatalf("POST .../rates for the corrected month = %d, want %d (body: %s)", right.Code, http.StatusCreated, right.Body.String())
	}

	rates := listedRates(t, r, tier.ID)
	if len(rates) != 1 || rates[0].EffectiveFrom != "2026-01" {
		t.Errorf("GET .../rates = %+v, want a single rate effective 2026-01", rates)
	}
}

func TestDeleteDuesRatesReturns404ForAnUnknownID(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// --- corrections leave a trail (ADR-040) -------------------------------------

func mustPostRate(t *testing.T, r http.Handler, tierID, amount int64, from string) duesRateResponse {
	t.Helper()
	rec := postDuesRate(t, r, tierID, duesRateRequest{Amount: amount, EffectiveFrom: from})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST .../rates (%s) = %d, want %d (body: %s)", from, rec.Code, http.StatusCreated, rec.Body.String())
	}
	var rate duesRateResponse
	if err := json.NewDecoder(rec.Body).Decode(&rate); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return rate
}

func TestPatchDuesRatesAnswersANewIDAndTheOldOneIsThenRefused(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	old := mustPostRate(t, r, tier.ID, 50_000, "2026-01")

	rec := patchDuesRate(t, r, old.ID, 75_000)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var corrected duesRateResponse
	if err := json.NewDecoder(rec.Body).Decode(&corrected); err != nil {
		t.Fatal(err)
	}
	if corrected.ID == old.ID {
		t.Errorf("a correction answered id %d, want a new row's id: the old row stays behind", corrected.ID)
	}
	if rates := listedRates(t, r, tier.ID); len(rates) != 1 || rates[0] != corrected {
		t.Errorf("GET .../rates = %+v, want only the corrected row %+v", rates, corrected)
	}

	// A stale page still holds the old id: refused, never applied to the successor.
	stale := patchDuesRate(t, r, old.ID, 99_000)
	if stale.Code != http.StatusConflict || decodeError(t, stale).Code != "dues_rate_superseded" {
		t.Errorf("PATCH on a superseded id = %d %s, want 409 dues_rate_superseded", stale.Code, stale.Body.String())
	}
	staleDel := deleteDuesRate(t, r, old.ID)
	if staleDel.Code != http.StatusConflict || decodeError(t, staleDel).Code != "dues_rate_superseded" {
		t.Errorf("DELETE on a superseded id = %d %s, want 409 dues_rate_superseded", staleDel.Code, staleDel.Body.String())
	}
	if rates := listedRates(t, r, tier.ID); len(rates) != 1 || rates[0].Amount != 75_000 {
		t.Errorf("GET .../rates = %+v, want the successor untouched at 75000", rates)
	}
}

func TestPatchDuesRatesWithTheStandingAmountIsANoOp(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	rate := mustPostRate(t, r, tier.ID, 50_000, "2026-01")

	rec := patchDuesRate(t, r, rate.ID, 50_000)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH with the standing amount = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got duesRateResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got != rate {
		t.Errorf("PATCH with the standing amount answered %+v, want the standing row %+v: nothing written", got, rate)
	}
	// Still addressable: the id did not change, so it is not refused as superseded.
	if again := patchDuesRate(t, r, rate.ID, 60_000); again.Code != http.StatusOK {
		t.Errorf("PATCH after a no-op = %d, want %d", again.Code, http.StatusOK)
	}
}

func TestPatchDuesRatesAboveTheCeilingIsRefusedAndLeavesTheRate(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	rate := mustPostRate(t, r, tier.ID, 50_000, "2026-01")

	rec := patchDuesRate(t, r, rate.ID, 1_000_000_000_000)
	if rec.Code != http.StatusBadRequest || decodeError(t, rec).Code != "amount_too_large" {
		t.Errorf("PATCH above the ceiling = %d %s, want 400 amount_too_large", rec.Code, rec.Body.String())
	}
	if rates := listedRates(t, r, tier.ID); len(rates) != 1 || rates[0] != rate {
		t.Errorf("GET .../rates = %+v, want the rate untouched", rates)
	}
}

func TestDeleteDuesRatesWithdrawsTheRateAndAReAddIntoTheSameMonthSucceeds(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")
	jan := mustPostRate(t, r, tier.ID, 50_000, "2026-01")
	jun := mustPostRate(t, r, tier.ID, 70_000, "2026-06")

	if rec := deleteDuesRate(t, r, jun.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want %d (body: %s)", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if rates := listedRates(t, r, tier.ID); len(rates) != 1 || rates[0] != jan {
		t.Errorf("GET .../rates = %+v, want January only: a deleted month does not show", rates)
	}
	// Deleting twice is a stale request, not a second deletion.
	if rec := deleteDuesRate(t, r, jun.ID); rec.Code != http.StatusConflict {
		t.Errorf("DELETE of an already-deleted rate = %d, want %d", rec.Code, http.StatusConflict)
	}

	// A month whose standing row is a deletion takes a new rate (a month with a
	// rate still answers 409 unique_violation, tested above).
	again := mustPostRate(t, r, tier.ID, 80_000, "2026-06")
	if again.ID == jun.ID {
		t.Error("the re-added rate reused the withdrawn row's id")
	}
	if rates := listedRates(t, r, tier.ID); len(rates) != 2 || rates[1] != again {
		t.Errorf("GET .../rates = %+v, want January and the re-added June", rates)
	}
	if dup := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 1, EffectiveFrom: "2026-06"}); dup.Code != http.StatusConflict ||
		decodeError(t, dup).Code != "unique_violation" {
		t.Errorf("POST into a month that has a rate again = %d %s, want 409 unique_violation", dup.Code, dup.Body.String())
	}
}
