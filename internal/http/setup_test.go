package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postSetup is the fixture for a test that needs a fund but not particular
// accounts: one default cash account, the minimum #78 requires. A test needing
// specific accounts calls postSetupWithAccounts; one needing the usual
// cash+bank pair calls setUpFund (testhelpers_test.go).
func postSetup(t *testing.T, r http.Handler, name string) *httptest.ResponseRecorder {
	t.Helper()
	return postSetupWithAccounts(t, r, name, []setupAccountRequest{{Kind: "cash", Name: "Tunai"}})
}

// The 201 body is exactly {fund, main_purpose_id, accounts}: pinned by #64 for
// fund and main_purpose_id; accounts became a list under #78, in requested
// order.
func TestPostSetupReturnsThePinnedResponseShape(t *testing.T) {
	t.Parallel()
	rec := postSetupWithAccounts(t, testRouter(t), "Test Fund", []setupAccountRequest{
		{Kind: "cash", Name: "Tunai"},
		{Kind: "bank", Name: "Bank"},
	})

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

	if len(got.Accounts) != 2 {
		t.Fatalf("len(accounts) = %d, want 2", len(got.Accounts))
	}
	if got.Accounts[0].Kind != "cash" || got.Accounts[0].Name != "Tunai" {
		t.Errorf("accounts[0] = %+v, want {Kind: cash, Name: Tunai}", got.Accounts[0])
	}
	if got.Accounts[1].Kind != "bank" || got.Accounts[1].Name != "Bank" {
		t.Errorf("accounts[1] = %+v, want {Kind: bank, Name: Bank}", got.Accounts[1])
	}
	if got.Accounts[0].ID == 0 || got.Accounts[1].ID == 0 {
		t.Error("an account id is zero")
	}
	if got.Accounts[0].ID == got.Accounts[1].ID {
		t.Errorf("accounts[0] and accounts[1] share id %d, want two distinct accounts", got.Accounts[0].ID)
	}
	if got.Accounts[0].InactiveOn != nil || got.Accounts[1].InactiveOn != nil {
		t.Error("a freshly set-up account has a non-nil inactive_on, want nil")
	}
}

// #78: any positive number of accounts is accepted, not only two; a cash-only
// fund is as valid as cash+bank.
func TestPostSetupAcceptsASingleAccount(t *testing.T) {
	t.Parallel()
	rec := postSetupWithAccounts(t, testRouter(t), "Test Fund", []setupAccountRequest{
		{Kind: "cash", Name: "Tunai"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup with one account = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var got setupResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Accounts) != 1 {
		t.Fatalf("len(accounts) = %d, want 1", len(got.Accounts))
	}
}

// #230 applied to setup: an account may carry its opening balance in the
// request, posted in SetUpFund's transaction. A mixed batch posts exactly the
// rows asked for.
func TestPostSetupWithOpeningBalancePostsExactlyTheAccountsThatHaveOne(t *testing.T) {
	t.Parallel()
	r := testRouter(t)

	rec := postSetupWithAccounts(t, r, "Test Fund", []setupAccountRequest{
		{Kind: "cash", Name: "Tunai"},
		{Kind: "bank", Name: "Bank", OpeningBalance: &openingBalanceRequest{Amount: 500_000, OccurredOn: "2026-08-01"}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got setupResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}

	transactions := decodeTransactionsPage(t, getTransactions(t, r)).Transactions
	if len(transactions) != 1 {
		t.Fatalf("GET /api/transactions = %d rows, want exactly 1 (only the bank account had a balance)", len(transactions))
	}
	if transactions[0].AccountID != got.BankAccountID(t) {
		t.Errorf("transaction.account_id = %d, want the bank account (%d)", transactions[0].AccountID, got.BankAccountID(t))
	}
	if transactions[0].Kind != "opening" || transactions[0].Direction != "in" || transactions[0].Amount != 500_000 {
		t.Errorf("transaction = %+v, want kind=opening direction=in amount=500000", transactions[0])
	}
	if transactions[0].PurposeID != got.MainPurposeID {
		t.Errorf("transaction.purpose_id = %d, want the main purpose %d", transactions[0].PurposeID, got.MainPurposeID)
	}
}

// A negative opening balance anywhere in the batch refuses the whole setup and
// leaves no fund, so a corrected retry does not hit ErrFundAlreadyExists.
func TestPostSetupRejectsNegativeOpeningBalanceLeavesNoFundBehind(t *testing.T) {
	t.Parallel()
	r := testRouter(t)

	rec := postSetupWithAccounts(t, r, "Test Fund", []setupAccountRequest{
		{Kind: "cash", Name: "Tunai", OpeningBalance: &openingBalanceRequest{Amount: -1, OccurredOn: "2026-08-01"}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/setup with a negative opening balance = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}

	retry := postSetupWithAccounts(t, r, "Test Fund", []setupAccountRequest{{Kind: "cash", Name: "Tunai"}})
	if retry.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup retry after a rejected opening balance = %d, want %d (body: %s)", retry.Code, http.StatusCreated, retry.Body.String())
	}
}

// Zero accounts is refused with the ledger's ErrInvalidArgument, mapped to a
// clean 400, not a 500, with no fund left for a retry to collide with.
func TestPostSetupRejectsZeroAccounts(t *testing.T) {
	t.Parallel()
	r := testRouter(t)

	rec := postSetupWithAccounts(t, r, "Test Fund", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/setup with zero accounts = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}

	// The refused call left no fund: a retry with real accounts must succeed, not
	// hit ErrFundAlreadyExists.
	retry := postSetupWithAccounts(t, r, "Test Fund", []setupAccountRequest{{Kind: "cash", Name: "Tunai"}})
	if retry.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup retry after a rejected zero-account call = %d, want %d (body: %s)", retry.Code, http.StatusCreated, retry.Body.String())
	}
}

// A malformed kind is refused by the schema's CHECK (kind IN ('cash','bank')),
// a 400 not a 500, the check_violation path of account_test.go's POST
// /api/accounts test.
func TestPostSetupRejectsAMalformedAccountKind(t *testing.T) {
	t.Parallel()
	rec := postSetupWithAccounts(t, testRouter(t), "Test Fund", []setupAccountRequest{
		{Kind: "wallet", Name: "Dompet"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/setup with kind=wallet = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

// A blank account name is refused by the schema's CHECK (length(trim(name)) >
// 0), as the fund name is one level up (SetUpFund's ErrInvalidArgument).
func TestPostSetupRejectsABlankAccountName(t *testing.T) {
	t.Parallel()
	rec := postSetupWithAccounts(t, testRouter(t), "Test Fund", []setupAccountRequest{
		{Kind: "cash", Name: "   "},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/setup with a blank account name = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

// A second POST /api/setup answers with the clean 409 envelope, never a raw
// constraint failure and never a second fund row.
func TestPostSetupSecondCallReturnsAClean409(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	// The one shape problem this layer sees that the ledger cannot: the body never
	// becomes a request, so there is nothing to pass down.
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
	t.Parallel()
	// /api/setup exists, DELETE does not: chi answers 405, not 404, in the same
	// envelope as everything else under /api.
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
	t.Parallel()
	// A read failing for a reason no handler anticipates: the store is closed
	// underneath it, as an unwritable or vanished database file looks from here.
	// It must not read as "no fund yet" (404, run setup), which means something
	// else to the client.
	sqlDB := testStoreDB(t)
	router := authedRouterFor(t, sqlDB)

	// The session cookie must be minted before the store dies: #116 gates GET
	// /api/fund, and the session manager's ErrorFunc (session.go) answers a store
	// failure loading that cookie with this same 500 envelope, so the test would
	// pass even without reaching resolveFund. Either failure point answers
	// identically, which is the point of routing both through writeAPIError.
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

// postSetupWithAccounts is postSetup for a test naming its own accounts, not
// postSetup's single cash account or setUpFund's cash+bank pair
// (testhelpers_test.go).
func postSetupWithAccounts(t *testing.T, r http.Handler, name string, accounts []setupAccountRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(setupRequest{Name: name, Accounts: accounts})
	if err != nil {
		t.Fatalf("marshaling setup request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewReader(body)))
	return rec
}

func patchFund(t *testing.T, r http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/fund", bytes.NewReader([]byte(body))))
	return rec
}

// The fund's name is a display label, so correcting it is a rename, not a
// ledger event; the setup wizard's copy promises "bisa diganti nanti kalau
// perlu."
func TestPatchFundRenamesTheFund(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := patchFund(t, r, `{"name":"Kas Ruang 3B"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/fund = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var updated fundResponse
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if updated.Name != "Kas Ruang 3B" {
		t.Errorf("Name = %q, want %q", updated.Name, "Kas Ruang 3B")
	}

	// And the rename is what the next reader sees, not only what the write echoed.
	getRec := httptest.NewRecorder()
	r.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/fund", nil))
	var got fundResponse
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding GET response: %v", err)
	}
	if got.Name != "Kas Ruang 3B" {
		t.Errorf("GET /api/fund Name = %q, want %q", got.Name, "Kas Ruang 3B")
	}
	// The report's address is not collateral damage of a rename: it is the public
	// link she may have shared.
	if got.ReportSlug != "" && updated.ReportSlug != got.ReportSlug {
		t.Errorf("ReportSlug changed on rename: %q -> %q", updated.ReportSlug, got.ReportSlug)
	}
}

// A body with no name is a 400, never a silent rename to the empty string, as
// updateDuesRateRequest.Amount is a pointer.
func TestPatchFundWithoutANameIsRejected(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := patchFund(t, r, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/fund {} = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

// Before setup there is no fund to rename, and resolveFund answers for it.
func TestPatchFundBeforeSetupIs404(t *testing.T) {
	t.Parallel()
	rec := patchFund(t, testRouter(t), `{"name":"Kas"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH /api/fund before setup = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
