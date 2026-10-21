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

func postAccount(t *testing.T, r http.Handler, req accountRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling account request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/accounts", bytes.NewReader(body)))
	return rec
}

func patchAccount(t *testing.T, r http.Handler, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/accounts/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, path, bytes.NewReader([]byte(body))))
	return rec
}

func deleteAccount(t *testing.T, r http.Handler, id int64) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/accounts/%d", id)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	return rec
}

func TestPostAccountsRequiresAFund(t *testing.T) {
	t.Parallel()
	rec := postAccount(t, testRouter(t), accountRequest{Kind: "bank", Name: "BCA"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/accounts before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// #78: POST /api/accounts lets a treasurer add a location after setup (a
// second bank account, or one setup under-counted).
func TestPostAccountsCreatesAndListReflectsIt(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := postAccount(t, r, accountRequest{Kind: "bank", Name: "BCA"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var created accountResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if created.ID == 0 {
		t.Error("account.id is zero")
	}
	if created.Kind != "bank" {
		t.Errorf("account.kind = %q, want %q", created.Kind, "bank")
	}
	if created.Name != "BCA" {
		t.Errorf("account.name = %q, want %q", created.Name, "BCA")
	}
	if created.InactiveOn != nil {
		t.Errorf("account.inactive_on = %v, want nil (a freshly created account is active)", created.InactiveOn)
	}

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts = %d, want %d (body: %s)", list.Code, http.StatusOK, list.Body.String())
	}
	var accounts []accountResponse
	if err := json.NewDecoder(list.Body).Decode(&accounts); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	// setUpFund's cash+bank pair, plus the one added; no cap per kind (#134), so a
	// second bank account is as valid as the first.
	if len(accounts) != 3 {
		t.Fatalf("GET /api/accounts = %d rows, want 3 (setup's 2 plus the one just created)", len(accounts))
	}
	found := false
	for _, a := range accounts {
		if a == created {
			found = true
		}
	}
	if !found {
		t.Errorf("GET /api/accounts = %+v, want it to include the created account %+v", accounts, created)
	}
}

// A malformed kind is refused by the schema's CHECK (kind IN
// ('cash','bank')), not a handler enum check.
func TestPostAccountsRejectsAMalformedKind(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := postAccount(t, r, accountRequest{Kind: "wallet", Name: "Dompet"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/accounts with kind=wallet = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

// A blank or whitespace-only name is refused by the schema's CHECK
// (length(trim(name)) > 0).
func TestPostAccountsRejectsABlankName(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	for _, name := range []string{"", "   "} {
		rec := postAccount(t, r, accountRequest{Kind: "cash", Name: name})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/accounts with name %q = %d, want %d (body: %s)", name, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "check_violation" {
			t.Errorf("name %q: error code = %q, want %q", name, got.Code, "check_violation")
		}
	}
}

func TestGetAccountsRequiresAFund(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	testRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/accounts before setup = %d, want %d", rec.Code, http.StatusNotFound)
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPatchAccountRenamesIt(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	rec := patchAccount(t, r, cashID, `{"name":"Kas RT 05"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/accounts/%d = %d, want %d (body: %s)", cashID, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got accountResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if got.ID != cashID {
		t.Errorf("account.id = %d, want %d", got.ID, cashID)
	}
	if got.Name != "Kas RT 05" {
		t.Errorf("account.name = %q, want %q", got.Name, "Kas RT 05")
	}
	if got.Kind != "cash" {
		t.Errorf("account.kind = %q, want unchanged %q", got.Kind, "cash")
	}
	if got.InactiveOn != nil {
		t.Errorf("account.inactive_on = %v, want nil (a rename does not touch it)", got.InactiveOn)
	}
}

// TestPatchAccountInactiveOnAbsentPresentAndNull is #134's lifecycle test:
// inactive_on absent leaves it, a date retires the account, null reinstates
// it, the three states members_test.go proves for member.inactive_on.
func TestPatchAccountInactiveOnAbsentPresentAndNull(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	bankID := setup.BankAccountID(t)

	t.Run("absent leaves it unchanged", func(t *testing.T) {
		rec := patchAccount(t, r, bankID, `{"name":"Bank"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got accountResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.InactiveOn != nil {
			t.Errorf("account.inactive_on = %v, want nil (inactive_on absent from the request)", got.InactiveOn)
		}
	})

	t.Run("present with a value retires it", func(t *testing.T) {
		rec := patchAccount(t, r, bankID, `{"inactive_on":"2026-03-15"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got accountResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.InactiveOn == nil || *got.InactiveOn != "2026-03-15" {
			t.Errorf("account.inactive_on = %v, want %q", got.InactiveOn, "2026-03-15")
		}
	})

	t.Run("present with null reinstates it", func(t *testing.T) {
		rec := patchAccount(t, r, bankID, `{"inactive_on":null}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
		}
		var got accountResponse
		if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		if got.InactiveOn != nil {
			t.Errorf("account.inactive_on = %v, want nil (inactive_on explicitly cleared)", got.InactiveOn)
		}
	})
}

// Both fields at once, a rename and a retirement in one PATCH: the two *Set
// flags are independent, not one "any field present" switch.
func TestPatchAccountBothFieldsAtOnce(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	bankID := setup.BankAccountID(t)

	rec := patchAccount(t, r, bankID, `{"name":"BCA (ditutup)","inactive_on":"2026-03-15"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got accountResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.Name != "BCA (ditutup)" {
		t.Errorf("account.name = %q, want %q", got.Name, "BCA (ditutup)")
	}
	if got.InactiveOn == nil || *got.InactiveOn != "2026-03-15" {
		t.Errorf("account.inactive_on = %v, want %q", got.InactiveOn, "2026-03-15")
	}
}

// A blank or whitespace-only name is refused by the schema's CHECK on PATCH as
// on POST.
func TestPatchAccountRejectsABlankName(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	rec := patchAccount(t, r, cashID, `{"name":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with a blank name = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("error code = %q, want %q", got.Code, "check_violation")
	}
}

func TestPatchAccountReturns404ForAnUnknownID(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := patchAccount(t, r, 9_999, `{"name":"Anything"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PATCH /api/accounts/9999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPatchAccountReturns400ForANonNumericID(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/api/accounts/abc", bytes.NewReader([]byte(`{"name":"Anything"}`))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/accounts/abc = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}

func TestPatchAccountRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	rec := patchAccount(t, r, cashID, "{oops")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH with malformed JSON = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// A never-used duplicate deletes cleanly (#134).
func TestDeleteAccountRemovesANeverUsedDuplicate(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	dupRec := postAccount(t, r, accountRequest{Kind: "cash", Name: "Tunai (duplikat)"})
	if dupRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts = %d, want %d", dupRec.Code, http.StatusCreated)
	}
	var dup accountResponse
	if err := json.NewDecoder(dupRec.Body).Decode(&dup); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	rec := deleteAccount(t, r, dup.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /api/accounts/%d = %d, want %d (body: %s)", dup.ID, rec.Code, http.StatusNoContent, rec.Body.String())
	}

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	var accounts []accountResponse
	if err := json.NewDecoder(list.Body).Decode(&accounts); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	for _, a := range accounts {
		if a.ID == dup.ID {
			t.Errorf("GET /api/accounts still lists the deleted account %+v", a)
		}
	}
}

// #134: an account with a transaction posted against it refuses the delete
// with a clean 409, not a 500 or a cascade; the treasurer needs inactive_on
// instead (updateAccount). No handler pre-check; it rides the composite
// foreign key's refusal, as TestDeleteMemberWithTransactionsReturns409.
func TestDeleteAccountWithTransactionsReturns409(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	txRec := postTransaction(t, r, transactionRequest{
		AccountID: cashID, PurposeID: setup.MainPurposeID,
		Direction: "in", Amount: 50_000, OccurredOn: "2026-08-12",
	})
	if txRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d, want %d (body: %s)", txRec.Code, http.StatusCreated, txRec.Body.String())
	}

	rec := deleteAccount(t, r, cashID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("DELETE /api/accounts/%d with a real transaction = %d, want %d (body: %s)", cashID, rec.Code, http.StatusConflict, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "referenced_by_other_records" {
		t.Errorf("error code = %q, want %q", got.Code, "referenced_by_other_records")
	}

	// The account and its transaction both survive.
	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	var accounts []accountResponse
	if err := json.NewDecoder(list.Body).Decode(&accounts); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	found := false
	for _, a := range accounts {
		if a.ID == cashID {
			found = true
		}
	}
	if !found {
		t.Errorf("GET /api/accounts no longer lists account %d after the refused delete", cashID)
	}
}

func TestDeleteAccountReturns404ForAnUnknownID(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := deleteAccount(t, r, 9_999)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE /api/accounts/9999 = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// #230 applied to POST /api/accounts: an optional opening_balance rides the
// request and posts in the account's transaction. The ordinary path: a
// positive amount creates the account and the balance shows wherever balances
// are read.
func TestPostAccountsWithOpeningBalanceCreatesAndBalancesReflectIt(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := postAccount(t, r, accountRequest{
		Kind: "cash", Name: "Kas RT 05",
		OpeningBalance: &openingBalanceRequest{Amount: 100_000, OccurredOn: "2026-08-01"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var created accountResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}

	balances := getBalances(t, r)
	if balances.Code != http.StatusOK {
		t.Fatalf("GET /api/balances = %d, want %d (body: %s)", balances.Code, http.StatusOK, balances.Body.String())
	}
	var got balancesResponse
	if err := json.NewDecoder(balances.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, balances.Body.String())
	}
	if got.FundTotal != 100_000 {
		t.Errorf("fund_total = %d, want %d", got.FundTotal, 100_000)
	}
	found := false
	for _, acc := range got.Accounts {
		if acc.ID == created.ID {
			found = true
			if acc.Balance != 100_000 {
				t.Errorf("account balance = %d, want %d", acc.Balance, 100_000)
			}
		}
	}
	if !found {
		t.Errorf("GET /api/balances accounts = %+v, want the created account %d among them", got.Accounts, created.ID)
	}

	list := getTransactions(t, r)
	transactions := decodeTransactionsPage(t, list).Transactions
	if len(transactions) != 1 {
		t.Fatalf("GET /api/transactions = %d rows, want 1", len(transactions))
	}
	if transactions[0].Kind != "opening" || transactions[0].AccountID != created.ID || transactions[0].PurposeID != setup.MainPurposeID {
		t.Errorf("transaction = %+v, want kind=opening account_id=%d purpose_id=%d", transactions[0], created.ID, setup.MainPurposeID)
	}
}

// A zero opening balance creates the account and posts no row; absent and
// zero are the same.
func TestPostAccountsWithZeroOpeningBalanceCreatesAccountNoTransaction(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := postAccount(t, r, accountRequest{
		Kind: "bank", Name: "BCA",
		OpeningBalance: &openingBalanceRequest{Amount: 0, OccurredOn: "2026-08-01"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	transactions := decodeTransactionsPage(t, getTransactions(t, r)).Transactions
	if len(transactions) != 0 {
		t.Errorf("GET /api/transactions = %d rows after a zero-amount opening balance, want 0", len(transactions))
	}
}

// A negative opening balance refuses the whole request, creating no account
// either: the two are born together or not at all (#230).
func TestPostAccountsRejectsNegativeOpeningBalanceCreatesNoAccount(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	before := decodeAccountsList(t, r)

	rec := postAccount(t, r, accountRequest{
		Kind: "cash", Name: "Tunai Baru",
		OpeningBalance: &openingBalanceRequest{Amount: -1, OccurredOn: "2026-08-01"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/accounts with a negative opening balance = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}

	after := decodeAccountsList(t, r)
	if len(after) != len(before) {
		t.Errorf("GET /api/accounts = %d accounts after a refused create, want %d (unchanged)", len(after), len(before))
	}
}

// A malformed occurred_on in the opening balance refuses the whole request alike.
func TestPostAccountsRejectsInvalidOpeningBalanceOccurredOnCreatesNoAccount(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	before := decodeAccountsList(t, r)

	rec := postAccount(t, r, accountRequest{
		Kind: "cash", Name: "Tunai Baru",
		OpeningBalance: &openingBalanceRequest{Amount: 100_000, OccurredOn: "not-a-date"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/accounts with a malformed opening balance date = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}

	after := decodeAccountsList(t, r)
	if len(after) != len(before) {
		t.Errorf("GET /api/accounts = %d accounts after a refused create, want %d (unchanged)", len(after), len(before))
	}
}

// decodeAccountsList decodes GET /api/accounts, for the refusal tests to prove
// a rejected create left the roster untouched.
func decodeAccountsList(t *testing.T, r http.Handler) []accountListItem {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/accounts = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var accounts []accountListItem
	if err := json.NewDecoder(rec.Body).Decode(&accounts); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return accounts
}

// mainPurposeID's last branch (internal/ledger): a fund with no kind='main'
// purpose. purpose_single_main makes this unreachable through any route
// (SetUpFund writes the row and nothing deletes it), so the row is removed
// with raw SQL, the only honest way to reach defensive code that exists
// because something outside this package (a hand-edited database, a botched
// restore) could violate the invariant. It must answer a clean 500 envelope,
// never panic on the zero id, and not create the account (CreateAccount's
// withTx rolls back).
func TestPostAccountsWithOpeningBalanceAndNoMainPurposeIs500(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	r := authedRouterFor(t, sqlDB)
	setUpFund(t, r)

	if _, err := sqlDB.Exec("DELETE FROM purpose WHERE kind = 'main'"); err != nil {
		t.Fatalf("deleting the main purpose = %v, want no error", err)
	}

	before := decodeAccountsList(t, r)

	rec := postAccount(t, r, accountRequest{
		Kind: "cash", Name: "Tunai Baru",
		OpeningBalance: &openingBalanceRequest{Amount: 100_000, OccurredOn: "2026-08-01"},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /api/accounts with no main purpose = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "internal_error" {
		t.Errorf("error code = %q, want %q", got.Code, "internal_error")
	}

	after := decodeAccountsList(t, r)
	if len(after) != len(before) {
		t.Errorf("GET /api/accounts = %d accounts after the 500, want %d - nothing should have been created", len(after), len(before))
	}
}

// postRawTo sends a body the typed helpers cannot express: a malformed
// document, or a well-formed one with wrong field types. Everything else uses
// postAccount.
func postRawTo(t *testing.T, r http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body))))
	return rec
}

func TestPostAccountsRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	rec := postRawTo(t, r, "/api/accounts", "{oops")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/accounts with malformed JSON = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_json" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_json")
	}
}

// A body that parses as an object with a wrong-typed field takes a different
// path through decodeUpdateAccountRequest than "{oops": the outer Decode
// succeeds and the per-field json.Unmarshal fails. Both must answer
// invalid_json, not reach the store with a zero value.
func TestPatchAccountRejectsAFieldOfTheWrongType(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	for _, body := range []string{`{"name": 123}`, `{"inactive_on": true}`} {
		rec := patchAccount(t, r, cashID, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("PATCH %s = %d, want %d (body: %s)", body, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		got := decodeError(t, rec)
		if got.Code != "invalid_json" {
			t.Errorf("PATCH %s error code = %q, want %q", body, got.Code, "invalid_json")
		}
	}
}

// Deliberately not covered, following TestGetBalancesOnADeadDatabaseIs500: the
// mapSQLiteError/mapLedgerError branches in listAccounts and createAccount's
// resolveFund call fire only when a store read fails, and #116's session gate
// reads the store first, so on a closed database the gate answers the same 500
// and the handler never runs. They stay as defensive code rather than gain an
// injection seam for branches that can only fire together.

// A location's kind is a label, not a rule about the money (nothing in
// internal/ledger branches on it), so the wrong one is a typo with a
// correction, like a misspelt name.
func TestPatchAccountChangesTheKind(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := patchAccount(t, r, setup.CashAccountID(t), `{"kind":"bank"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /api/accounts/{id} kind = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var updated accountResponse
	if err := json.NewDecoder(rec.Body).Decode(&updated); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if updated.Kind != "bank" {
		t.Errorf("Kind = %q, want %q", updated.Kind, "bank")
	}
	// The name is untouched: an absent key means "leave alone".
	if updated.Name != "Tunai" {
		t.Errorf("Name = %q, want it unchanged (%q)", updated.Name, "Tunai")
	}
}

// The set of kinds is the schema's CHECK to police (ADR-027): 400 through
// mapSQLiteError.
func TestPatchAccountRejectsAnUnknownKind(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	rec := patchAccount(t, r, setup.CashAccountID(t), `{"kind":"crypto"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH /api/accounts/{id} kind=crypto = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// #474: retiring a location that holds money answers 409 account_holds_money
// and leaves it active; once Pindah lokasi empties it, the same PATCH retires it.
func TestPatchAccountRefusesToRetireALocationHoldingMoney(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	bank := setup.BankAccountID(t)
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: bank, PurposeID: setup.MainPurposeID, Direction: "in", Amount: 40_000, OccurredOn: "2026-09-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec := patchAccount(t, r, bank, `{"inactive_on":"2026-09-05"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PATCH (retire, holding 40000) = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if code := decodeError(t, rec).Code; code != "account_holds_money" {
		t.Errorf("error code = %q, want %q", code, "account_holds_money")
	}

	if rec := postTransfer(t, r, transferRequest{
		PurposeID: setup.MainPurposeID, FromAccountID: bank, ToAccountID: setup.CashAccountID(t), Amount: 40_000, OccurredOn: "2026-09-04",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transfers = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if rec := patchAccount(t, r, bank, `{"inactive_on":"2026-09-05"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH (retire, empty) = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// Below zero is its own refusal (#474): nothing to move, so the code and the
// client's sentence point at a count, not Pindah lokasi.
func TestPatchAccountRefusesToRetireALocationBelowZero(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	bank := setup.BankAccountID(t)
	if rec := postTransaction(t, r, transactionRequest{
		AccountID: bank, PurposeID: setup.MainPurposeID, Direction: "out", Amount: 15_000, OccurredOn: "2026-09-01",
	}); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/transactions = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec := patchAccount(t, r, bank, `{"inactive_on":"2026-09-05"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PATCH (retire, at -15000) = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if code := decodeError(t, rec).Code; code != "account_balance_negative" {
		t.Errorf("error code = %q, want %q", code, "account_balance_negative")
	}
}

// #637: GET /api/accounts carries each location's opening-entry date so the
// entry forms can warn about a date before it; a location with none reads null.
func TestListAccountsCarriesOpeningDate(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	created := postAccount(t, r, accountRequest{
		Kind: "cash", Name: "Kas RT 05",
		OpeningBalance: &openingBalanceRequest{Amount: 100_000, OccurredOn: "2026-08-01"},
	})
	// Only the list carries the field: a create answers no opening_on at all,
	// never a null that would read as "no opening".
	if strings.Contains(created.Body.String(), "opening_on") {
		t.Errorf("POST /api/accounts answered opening_on: %s", created.Body.String())
	}
	withOpening := decodeAccount(t, created)

	var sawOpening, sawNone bool
	for _, acc := range decodeAccountsList(t, r) {
		switch {
		case acc.ID == withOpening.ID:
			sawOpening = true
			if acc.OpeningOn == nil || *acc.OpeningOn != "2026-08-01" {
				t.Errorf("opening_on = %v, want 2026-08-01", acc.OpeningOn)
			}
		case acc.OpeningOn != nil:
			t.Errorf("account %d opening_on = %q, want null (no opening entry)", acc.ID, *acc.OpeningOn)
		default:
			sawNone = true
		}
	}
	if !sawOpening || !sawNone {
		t.Errorf("sawOpening=%v sawNone=%v, want both", sawOpening, sawNone)
	}
}

func decodeAccount(t *testing.T, rec *httptest.ResponseRecorder) accountResponse {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got accountResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return got
}
