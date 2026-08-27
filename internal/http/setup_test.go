package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

func postSetup(t *testing.T, r http.Handler, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(setupRequest{Name: name})
	if err != nil {
		t.Fatalf("marshaling setup request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewReader(body)))
	return rec
}

// The 201 response body is exactly {fund, main_purpose_id, cash_account_id,
// bank_account_id} - pinned by #64, asserted here rather than only on the
// error path, since #65-#67 all consume these ids the moment setup returns.
func TestPostSetupReturnsThePinnedResponseShape(t *testing.T) {
	rec := postSetup(t, testRouter(t), "Test Fund")

	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var got setupResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}

	if got.Fund.ID == 0 {
		t.Error("fund.id is zero")
	}
	if got.Fund.Name != "Test Fund" {
		t.Errorf("fund.name = %q, want %q", got.Fund.Name, "Test Fund")
	}
	if got.Fund.Currency != "IDR" {
		t.Errorf("fund.currency = %q, want %q", got.Fund.Currency, "IDR")
	}
	if len(got.Fund.ReportSlug) < 22 {
		t.Errorf("len(fund.report_slug) = %d, want >= 22", len(got.Fund.ReportSlug))
	}
	if got.MainPurposeID == 0 {
		t.Error("main_purpose_id is zero")
	}
	if got.CashAccountID == 0 {
		t.Error("cash_account_id is zero")
	}
	if got.BankAccountID == 0 {
		t.Error("bank_account_id is zero")
	}
	if got.CashAccountID == got.BankAccountID {
		t.Errorf("cash_account_id and bank_account_id are both %d, want two distinct accounts", got.CashAccountID)
	}
}

// A second POST /api/setup answers with the clean 409 envelope, never a raw
// constraint failure and never a second fund row.
func TestPostSetupSecondCallReturnsAClean409(t *testing.T) {
	r := testRouter(t)

	first := postSetup(t, r, "Test Fund")
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST /api/setup = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}

	second := postSetup(t, r, "Second Fund")
	if second.Code != http.StatusConflict {
		t.Fatalf("second POST /api/setup = %d, want %d (body: %s)", second.Code, http.StatusConflict, second.Body.String())
	}
	got := decodeError(t, second)
	if got.Code != "fund_already_exists" {
		t.Errorf("error code = %q, want %q", got.Code, "fund_already_exists")
	}
}

func TestGetFundReturns404BeforeSetupHasRun(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/fund before setup = %d, want %d", rec.Code, http.StatusNotFound)
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestGetFundReturnsTheFundAfterSetup(t *testing.T) {
	r := testRouter(t)

	setup := postSetup(t, r, "Test Fund")
	if setup.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d (body: %s)", setup.Code, http.StatusCreated, setup.Body.String())
	}
	var created setupResponse
	if err := json.NewDecoder(setup.Body).Decode(&created); err != nil {
		t.Fatalf("decoding setup response: %v", err)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/fund = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got fundResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if got != created.Fund {
		t.Errorf("GET /api/fund = %+v, want %+v (the fund POST /api/setup created)", got, created.Fund)
	}
}

func TestPostSetupRejectsMalformedJSON(t *testing.T) {
	// The one shape problem this layer can see that the ledger cannot: the
	// body never becomes a request at all, so there is nothing to pass down.
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader("{oops")))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/setup with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the body = %v, want the JSON error envelope (body: %q)", err, rec.Body.String())
	}
	if body.Error.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", body.Error.Code, "invalid_json")
	}
}

func TestAPIRejectsAnUnsupportedMethodInTheEnvelope(t *testing.T) {
	// /api/setup exists, DELETE does not - chi answers 405 rather than 404,
	// and it has to do so in the same envelope as everything else under /api.
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/setup", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE /api/setup = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the body = %v, want the JSON error envelope (body: %q)", err, rec.Body.String())
	}
	if body.Error.Code != "method_not_allowed" {
		t.Errorf("error code = %q, want %q", body.Error.Code, "method_not_allowed")
	}
}

func TestGetFundReportsAStoreFailureAsA500(t *testing.T) {
	// A read that fails for a reason no handler can anticipate - here the
	// store is closed underneath it, which is what an unwritable or vanished
	// database file looks like from up here. It must not be mistaken for
	// "no fund yet", which is a 404 and means something entirely different
	// to the client: run setup.
	sqlDB := testStoreDB(t)
	router := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), testLogger(), auth.New(sqlDB), "")
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing the store = %v, want no error", err)
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET /api/fund with a closed store = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the body = %v, want the JSON error envelope (body: %q)", err, rec.Body.String())
	}
	if body.Error.Code != "internal_error" {
		t.Errorf("error code = %q, want %q", body.Error.Code, "internal_error")
	}
}
