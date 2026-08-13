package http

import (
	"bytes"
	"encoding/json"
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
