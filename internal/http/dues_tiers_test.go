package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postDuesTier(t *testing.T, r http.Handler, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(duesTierRequest{Name: name})
	if err != nil {
		t.Fatalf("marshaling dues tier request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/dues-tiers", bytes.NewReader(body)))
	return rec
}

func TestPostDuesTiersRequiresAFund(t *testing.T) {
	rec := postDuesTier(t, testRouter(t), "Full")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/dues-tiers before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostDuesTiersCreatesAndListReturnsIt(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := postDuesTier(t, r, "Full")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/dues-tiers = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var created duesTierResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if created.ID == 0 {
		t.Error("dues_tier.id is zero")
	}
	if created.Name != "Full" {
		t.Errorf("dues_tier.name = %q, want %q", created.Name, "Full")
	}

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/dues-tiers", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/dues-tiers = %d, want %d (body: %s)", list.Code, http.StatusOK, list.Body.String())
	}

	var tiers []duesTierResponse
	if err := json.NewDecoder(list.Body).Decode(&tiers); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, list.Body.String())
	}
	if len(tiers) != 1 || tiers[0] != created {
		t.Errorf("GET /api/dues-tiers = %+v, want [%+v]", tiers, created)
	}
}

func TestPostDuesTiersRejectsADuplicateNameWith409(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}
	if rec := postDuesTier(t, r, "Full"); rec.Code != http.StatusCreated {
		t.Fatalf("first POST /api/dues-tiers = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// dues_tier's own UNIQUE (fund_id, name) - proven through the shared
	// mapper, not a hand-rolled duplicate check in the handler.
	rec := postDuesTier(t, r, "Full")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second POST /api/dues-tiers with a duplicate name = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "unique_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "unique_violation")
	}
}

func TestPostDuesTiersRejectsAnEmptyName(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := postDuesTier(t, r, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/dues-tiers with empty name = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

func patchDuesTier(t *testing.T, r http.Handler, id int64, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(duesTierRequest{Name: name})
	if err != nil {
		t.Fatalf("marshaling dues tier request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-tiers/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(body)))
	return rec
}

func TestPatchDuesTiersRenamesItAndListReflectsIt(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	rec := patchDuesTier(t, r, tier.ID, "Penuh")
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/dues-tiers/%d = %d, want %d (body: %s)", tier.ID, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got duesTierResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if got.ID != tier.ID {
		t.Errorf("dues_tier.id = %d, want %d", got.ID, tier.ID)
	}
	if got.Name != "Penuh" {
		t.Errorf("dues_tier.name = %q, want %q", got.Name, "Penuh")
	}

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/dues-tiers", nil))
	var tiers []duesTierResponse
	if err := json.NewDecoder(list.Body).Decode(&tiers); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(tiers) != 1 || tiers[0].Name != "Penuh" {
		t.Errorf("GET /api/dues-tiers = %+v, want a single tier named %q", tiers, "Penuh")
	}
}

func TestPatchDuesTiersRejectsAnEmptyName(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	rec := patchDuesTier(t, r, tier.ID, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/dues-tiers/%d with empty name = %d, want %d (body: %s)", tier.ID, rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

func TestPatchDuesTiersReturns404ForAnUnknownID(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := patchDuesTier(t, r, 999, "Penuh")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH /api/dues-tiers/999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPatchDuesTiersReturns400ForANonNumericID(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	body, _ := json.Marshal(duesTierRequest{Name: "Penuh"})
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/dues-tiers/abc", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/dues-tiers/abc = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPatchDuesTiersRejectsMalformedJSON(t *testing.T) {
	r := testRouter(t)
	tier := setUpTier(t, r, "Full")

	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-tiers/%d", tier.ID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader([]byte("{oops"))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/dues-tiers/%d with malformed JSON = %d, want %d", tier.ID, rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

func TestGetDuesTiersRequiresAFund(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dues-tiers", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/dues-tiers before setup = %d, want %d", rec.Code, http.StatusNotFound)
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func deleteDuesTier(t *testing.T, r http.Handler, idPathSegment string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/dues-tiers/%s", idPathSegment)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	return rec
}

// decodeDuesTier is the single-tier decode the delete tests need; the
// existing tests all read the list instead.
func decodeDuesTier(t *testing.T, rec *httptest.ResponseRecorder) duesTierResponse {
	t.Helper()
	var tier duesTierResponse
	if err := json.NewDecoder(rec.Body).Decode(&tier); err != nil {
		t.Fatalf("decoding dues tier response: %v", err)
	}
	return tier
}

// TestDeleteDuesTierTakesItsRatesWithIt is #232's ruling: a tier no member
// is in priced nothing, so its rates are its own children rather than
// history, and both go in one transaction.
func TestDeleteDuesTierTakesItsRatesWithIt(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	tier := decodeDuesTier(t, postDuesTier(t, r, "Madya"))
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 25_000, EffectiveFrom: "2026-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST rate = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := deleteDuesTier(t, r, fmt.Sprintf("%d", tier.ID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/dues-tiers/%d = %d, want %d (body: %s)", tier.ID, rec.Code, http.StatusNoContent, rec.Body.String())
	}

	// Gone from the list, and its rates with it - the rates route now has no
	// tier to answer for at all.
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/dues-tiers", nil))
	var tiers []duesTierResponse
	if err := json.NewDecoder(listRec.Body).Decode(&tiers); err != nil {
		t.Fatalf("decoding dues tiers: %v", err)
	}
	for _, got := range tiers {
		if got.ID == tier.ID {
			t.Errorf("deleted tier %d is still listed", tier.ID)
		}
	}
	if ratesRec := getDuesRates(t, r, fmt.Sprintf("%d", tier.ID)); ratesRec.Code != http.StatusNotFound {
		t.Errorf("GET rates for the deleted tier = %d, want %d (body: %s)", ratesRec.Code, http.StatusNotFound, ratesRec.Body.String())
	}
}

// TestDeleteDuesTierRefusesOneAMemberIsIn is the acceptance criterion, and
// the reason the delete runs in a transaction: the refusal comes from
// member's own FK, after the rate deletion has already run, so the rollback
// is what keeps that tier's rates intact.
func TestDeleteDuesTierRefusesOneAMemberIsIn(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	tier := decodeDuesTier(t, postDuesTier(t, r, "Penuh"))
	if rec := postDuesRate(t, r, tier.ID, duesRateRequest{Amount: 50_000, EffectiveFrom: "2026-01"}); rec.Code != http.StatusCreated {
		t.Fatalf("POST rate = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if rec := postMember(t, r, memberRequest{Name: "Budi", TierID: &tier.ID}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/members = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	rec := deleteDuesTier(t, r, fmt.Sprintf("%d", tier.ID))
	if rec.Code != http.StatusConflict {
		t.Fatalf("DELETE a tier in use = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "referenced_by_other_records" {
		t.Errorf("error code = %q, want %q", got.Code, "referenced_by_other_records")
	}

	// The rollback's whole point: the rate deletion ran inside the same
	// transaction and must not have survived it.
	ratesRec := getDuesRates(t, r, fmt.Sprintf("%d", tier.ID))
	if ratesRec.Code != http.StatusOK {
		t.Fatalf("GET rates after a refused delete = %d, want %d (body: %s)", ratesRec.Code, http.StatusOK, ratesRec.Body.String())
	}
	var rates []duesRateResponse
	if err := json.NewDecoder(ratesRec.Body).Decode(&rates); err != nil {
		t.Fatalf("decoding dues rates: %v", err)
	}
	if len(rates) != 1 {
		t.Errorf("tier has %d rates after a refused delete, want 1 - the rollback did not restore them: %+v", len(rates), rates)
	}
}

// A tier this fund does not have is 404, never 204 - an id names a row, it
// does not prove the caller may touch it (#188). An id that is not a number
// at all never gets that far and is 400, which is resolveDuesTier's own
// existing split, asserted here so the delete route keeps inheriting it.
func TestDeleteDuesTierRejectsAnIDItCannotUse(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	for _, tc := range []struct {
		segment string
		want    int
		code    string
	}{
		{"999", http.StatusNotFound, "not_found"},
		{"not-a-number", http.StatusBadRequest, "invalid_argument"},
	} {
		rec := deleteDuesTier(t, r, tc.segment)
		if rec.Code != tc.want {
			t.Errorf("DELETE /api/dues-tiers/%s = %d, want %d (body: %s)", tc.segment, rec.Code, tc.want, rec.Body.String())
			continue
		}
		if got := decodeError(t, rec); got.Code != tc.code {
			t.Errorf("DELETE /api/dues-tiers/%s: error code = %q, want %q", tc.segment, got.Code, tc.code)
		}
	}
}
