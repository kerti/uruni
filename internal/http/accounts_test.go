package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func getAccounts(t *testing.T, r http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	return rec
}

func TestGetAccountsRequiresAFund(t *testing.T) {
	rec := getAccounts(t, testRouter(t))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/accounts before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// Setup creates both locations and nothing else can, so the list is exactly
// the two kinds PRD §6 names.
func TestGetAccountsReturnsTheTwoSetupLocations(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := getAccounts(t, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got []accountResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if len(got) != 2 {
		t.Fatalf("accounts = %d, want 2 (body: %s)", len(got), rec.Body.String())
	}

	kinds := map[string]bool{}
	for _, acc := range got {
		if acc.ID == 0 {
			t.Error("account.id is zero")
		}
		if acc.Name == "" {
			t.Error("account.name is empty")
		}
		kinds[acc.Kind] = true
	}
	if !kinds["cash"] || !kinds["bank"] {
		t.Errorf("account kinds = %v, want both cash and bank", kinds)
	}
}

// There is no POST /api/accounts and adding one is out of scope, so the
// route answers 405 rather than quietly falling through to something else.
func TestPostAccountsIsNotARoute(t *testing.T) {
	r := testRouter(t)
	if rec := postSetup(t, r, "Test Fund"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d", rec.Code, http.StatusCreated)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/accounts", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/accounts = %d, want %d (body: %s)", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}
