package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func postOpeningBalance(t *testing.T, r http.Handler, accountID int64, req postOpeningBalanceRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling opening balance request: %v", err)
	}
	rec := httptest.NewRecorder()
	path := fmt.Sprintf("/api/accounts/%d/opening-balance", accountID)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body)))
	return rec
}

func TestPostAccountsRequiresAFund(t *testing.T) {
	rec := postAccount(t, testRouter(t), accountRequest{Kind: "bank", Name: "BCA"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/accounts before setup = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

// #78's other half: POST /api/accounts is what lets a treasurer add a
// location after setup, not only choose the starting set - a second bank
// account, or one setup under-counted.
func TestPostAccountsCreatesAndListReflectsIt(t *testing.T) {
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
	// setUpFund's cash+bank pair, plus the one just added - no cap on
	// accounts per kind (#134's own "not in scope" ruling), so a second bank
	// account is exactly as valid as the first.
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

// A malformed kind is refused by the schema's own CHECK (kind IN
// ('cash','bank')), not a hand-rolled enum check in the handler.
func TestPostAccountsRejectsAMalformedKind(t *testing.T) {
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

// A blank or whitespace-only name is refused by the schema's own CHECK
// (length(trim(name)) > 0).
func TestPostAccountsRejectsABlankName(t *testing.T) {
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

// TestPatchAccountInactiveOnAbsentPresentAndNull is #134's own account-
// lifecycle test: inactive_on absent leaves it untouched, present with a
// date retires the account, and present with null reinstates it - the same
// three states members_test.go's TestPatchMemberInactiveOnAbsentPresentAndNull
// already proves for member.inactive_on, applied to account.
func TestPatchAccountInactiveOnAbsentPresentAndNull(t *testing.T) {
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

// Both fields at once: a rename and a retirement in the same PATCH, proving
// the two *Set flags are independent, not a single "any field present"
// switch.
func TestPatchAccountBothFieldsAtOnce(t *testing.T) {
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

// A blank or whitespace-only name is refused by the schema's own CHECK on
// PATCH exactly as it is on POST.
func TestPatchAccountRejectsABlankName(t *testing.T) {
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

// A never-used duplicate deletes cleanly - #134's first account-lifecycle
// half.
func TestDeleteAccountRemovesANeverUsedDuplicate(t *testing.T) {
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

// #134's second account-lifecycle half: an account with a real transaction
// posted against it refuses the delete with a clean 409, not a 500 and not
// a cascade - the treasurer needs inactive_on for this account instead
// (updateAccount, tested above). No pre-check in the handler; this rides
// the composite foreign key's own refusal, the same as
// TestDeleteMemberWithTransactionsReturns409.
func TestDeleteAccountWithTransactionsReturns409(t *testing.T) {
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

	// The account and its transaction both survive - nothing was cascaded.
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

// PostOpeningBalance has been built and tested since M3 (internal/ledger)
// but had no HTTP route until #134. This is the ordinary path: a positive
// amount posts one kind='opening' transaction and the response carries it.
func TestPostAccountOpeningBalancePostsATransaction(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	rec := postOpeningBalance(t, r, cashID, postOpeningBalanceRequest{
		Amount: 100_000, OccurredOn: "2026-08-01",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/accounts/%d/opening-balance = %d, want %d (body: %s)", cashID, rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got postOpeningBalanceResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if got.PostedAmount != 100_000 {
		t.Errorf("posted_amount = %d, want %d", got.PostedAmount, 100_000)
	}
	if got.Transaction == nil {
		t.Fatal("transaction is nil, want the posted row")
	}
	if got.Transaction.Kind != "opening" {
		t.Errorf("transaction.kind = %q, want %q", got.Transaction.Kind, "opening")
	}
	if got.Transaction.AccountID != cashID {
		t.Errorf("transaction.account_id = %d, want %d", got.Transaction.AccountID, cashID)
	}
	if got.Transaction.PurposeID != setup.MainPurposeID {
		t.Errorf("transaction.purpose_id = %d, want the main purpose %d", got.Transaction.PurposeID, setup.MainPurposeID)
	}
	if got.Transaction.Direction != "in" {
		t.Errorf("transaction.direction = %q, want %q", got.Transaction.Direction, "in")
	}
	if got.Transaction.Amount != 100_000 {
		t.Errorf("transaction.amount = %d, want %d", got.Transaction.Amount, 100_000)
	}

	list := getTransactions(t, r)
	var transactions []transactionResponse
	if err := json.NewDecoder(list.Body).Decode(&transactions); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(transactions) != 1 {
		t.Fatalf("GET /api/transactions = %d rows, want 1", len(transactions))
	}
}

// A zero amount posts no row and no error - PostOpeningBalance's own no-op
// rule, surfaced the same way closeIncidentalResponse.RolledAmount already
// distinguishes "posted" from "nothing to post" for a zero roll:
// posted_amount is 0 and transaction is nil, not a 4xx.
func TestPostAccountOpeningBalanceZeroAmountIsANoOp(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	rec := postOpeningBalance(t, r, cashID, postOpeningBalanceRequest{
		Amount: 0, OccurredOn: "2026-08-01",
	})
	// 200, not 201: a zero amount posts no row, so there is no creation to
	// claim. The 201 path is TestPostAccountOpeningBalancePostsATransaction.
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/accounts/%d/opening-balance with amount 0 = %d, want %d (body: %s)", cashID, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got postOpeningBalanceResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.PostedAmount != 0 {
		t.Errorf("posted_amount = %d, want 0", got.PostedAmount)
	}
	if got.Transaction != nil {
		t.Errorf("transaction = %+v, want nil - a zero amount posts no row", got.Transaction)
	}

	list := getTransactions(t, r)
	var transactions []transactionResponse
	if err := json.NewDecoder(list.Body).Decode(&transactions); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(transactions) != 0 {
		t.Errorf("GET /api/transactions = %d rows, want 0 after a zero-amount opening balance", len(transactions))
	}

	// A zero call does not consume the account's one-opening slot: a genuine
	// later opening balance may still be posted.
	again := postOpeningBalance(t, r, cashID, postOpeningBalanceRequest{
		Amount: 50_000, OccurredOn: "2026-08-02",
	})
	if again.Code != http.StatusCreated {
		t.Fatalf("POST .../opening-balance after a zero call = %d, want %d (body: %s)", again.Code, http.StatusCreated, again.Body.String())
	}
}

// A second call for the same account is refused with the named 409 -
// ErrOpeningBalanceExists, already mapped in errors.go.
func TestPostAccountOpeningBalanceSecondCallReturns409(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	first := postOpeningBalance(t, r, cashID, postOpeningBalanceRequest{
		Amount: 100_000, OccurredOn: "2026-08-01",
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST .../opening-balance = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}

	second := postOpeningBalance(t, r, cashID, postOpeningBalanceRequest{
		Amount: 50_000, OccurredOn: "2026-08-02",
	})
	if second.Code != http.StatusConflict {
		t.Fatalf("second POST .../opening-balance = %d, want %d (body: %s)", second.Code, http.StatusConflict, second.Body.String())
	}
	got := decodeError(t, second)
	if got.Code != "opening_balance_exists" {
		t.Errorf("error code = %q, want %q", got.Code, "opening_balance_exists")
	}

	list := getTransactions(t, r)
	var transactions []transactionResponse
	if err := json.NewDecoder(list.Body).Decode(&transactions); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(transactions) != 1 {
		t.Errorf("GET /api/transactions = %d rows after a refused second opening balance, want 1", len(transactions))
	}
}

func TestPostAccountOpeningBalanceReturns404ForAnUnknownAccount(t *testing.T) {
	r := testRouter(t)
	setUpFund(t, r)

	rec := postOpeningBalance(t, r, 9_999, postOpeningBalanceRequest{
		Amount: 100_000, OccurredOn: "2026-08-01",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/accounts/9999/opening-balance = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("error code = %q, want %q", got.Code, "not_found")
	}
}

func TestPostAccountOpeningBalanceRejectsInvalidOccurredOn(t *testing.T) {
	r := testRouter(t)
	setup := setUpFund(t, r)
	cashID := setup.CashAccountID(t)

	rec := postOpeningBalance(t, r, cashID, postOpeningBalanceRequest{
		Amount: 100_000, OccurredOn: "not-a-date",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST .../opening-balance with a malformed date = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}
}
