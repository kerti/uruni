package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These pin #530: a body is exactly one JSON value whose keys the route takes,
// and an invalid_argument names the request field at fault in `field`.

func sendJSON(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// wantRefusal asserts a 400 with the code and field given; field "" asserts
// that none is sent.
func wantRefusal(t *testing.T, rec *httptest.ResponseRecorder, code, field string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if got := decodeError(t, rec); got.Code != code || got.Field != field {
		t.Errorf("error = %+v, want code %q field %q", got, code, field)
	}
}

// unknownJSONField reads encoding/json's message text; if a Go release words
// it differently, this is the test that says so.
func TestUnknownJSONFieldReadsEncodingJSONsMessage(t *testing.T) {
	t.Parallel()
	var dst struct {
		Name string `json:"name"`
	}
	dec := json.NewDecoder(strings.NewReader(`{"name":"x","nmae":"y"}`))
	dec.DisallowUnknownFields()
	err := dec.Decode(&dst)
	if field, ok := unknownJSONField(err); !ok || field != "nmae" {
		t.Errorf("unknownJSONField(%v) = %q, %v, want %q, true", err, field, ok, "nmae")
	}
	if _, ok := unknownJSONField(errors.New("json: cannot unmarshal")); ok {
		t.Error("unknownJSONField matched an error that is not about an unknown field")
	}
}

func TestABodyKeyTheRouteDoesNotTakeIsRefusedAndNamed(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)

	// A misspelt optional key used to decode as if absent: a note typed as
	// "notes" vanished without a word.
	body := fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"in","amount":10000,"occurred_on":"2026-08-01","notes":"iuran"}`,
		setup.CashAccountID(t), setup.MainPurposeID)
	wantRefusal(t, sendJSON(t, r, http.MethodPost, "/api/transactions", body), "invalid_argument", "notes")

	// A nested one is refused too, named by its own key.
	body = fmt.Sprintf(`{"note":null,"counts":[{"account_id":%d,"actual_amount":0,"resolution":"matched","fix":null,"memo":"x"}]}`,
		setup.CashAccountID(t))
	wantRefusal(t, sendJSON(t, r, http.MethodPost, "/api/reconciliations", body), "invalid_argument", "memo")
}

func TestABodyThatIsNotOneJSONValueIsInvalidJSON(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	for name, body := range map[string]string{
		"empty":           ``,
		"two values":      `{"name":"Satu"}{"name":"Dua"}`,
		"trailing text":   `{"name":"Satu"} x`,
		"trailing number": `{"name":"Satu"} 1`,
	} {
		t.Run(name, func(t *testing.T) {
			wantRefusal(t, sendJSON(t, r, http.MethodPost, "/api/pass-through-purposes", body), "invalid_json", "")
		})
	}

	// Trailing whitespace is still one value.
	if rec := sendJSON(t, r, http.MethodPost, "/api/pass-through-purposes", "{\"name\":\"Satu\"}\n\n"); rec.Code != http.StatusCreated {
		t.Errorf("POST with a trailing newline = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
}

// The PATCH routes decode into a raw map, so "this key was sent" is
// distinguishable from "this key is null"; a key outside the route's list is
// refused there too.
func TestAPatchKeyTheRouteDoesNotTakeIsRefusedAndNamed(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	memberID := memberFor(t, r, "Budi")

	claimRec := postReimbursement(t, r, reimbursementRequest{
		MemberID: memberID, PurposeID: setup.MainPurposeID, Amount: 20_000, IncurredOn: "2026-08-01",
	})
	if claimRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/reimbursements = %d, want %d (body: %s)", claimRec.Code, http.StatusCreated, claimRec.Body.String())
	}
	var claim reimbursementResponse
	if err := json.NewDecoder(claimRec.Body).Decode(&claim); err != nil {
		t.Fatalf("decoding claim: %v", err)
	}

	for _, c := range []struct{ path, body, field string }{
		{fmt.Sprintf("/api/accounts/%d", setup.CashAccountID(t)), `{"name":"Dompet","balance":0}`, "balance"},
		{fmt.Sprintf("/api/members/%d", memberID), `{"name":"Budi S","phone":"0812"}`, "phone"},
		{fmt.Sprintf("/api/reimbursements/%d", claim.ID), `{"amount":25000,"account_id":1}`, "account_id"},
	} {
		wantRefusal(t, sendJSON(t, r, http.MethodPatch, c.path, c.body), "invalid_argument", c.field)
	}
}

// The ledger's FieldError reaches the wire as `field`, by its JSON key.
func TestALedgerRefusalNamesTheFieldAtFault(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setup := setUpFund(t, r)
	cash := setup.CashAccountID(t)
	memberID := memberFor(t, r, "Sari")

	for _, c := range []struct{ name, path, body, field string }{
		{"amount", "/api/transactions",
			fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"in","amount":0,"occurred_on":"2026-08-01"}`, cash, setup.MainPurposeID),
			"amount"},
		{"calendar date", "/api/transactions",
			fmt.Sprintf(`{"account_id":%d,"purpose_id":%d,"direction":"in","amount":1000,"occurred_on":"2026-02-30"}`, cash, setup.MainPurposeID),
			"occurred_on"},
		{"a period's amount", "/api/dues-payments",
			fmt.Sprintf(`{"account_id":%d,"member_id":%d,"occurred_on":"2026-08-01","periods":[{"dues_period":"2026-08","amount":0}]}`, cash, memberID),
			"periods.amount"},
		{"a count's fix", "/api/reconciliations",
			fmt.Sprintf(`{"counts":[{"account_id":%d,"actual_amount":5000,"resolution":"adjusted","fix":{"purpose_id":%d,"direction":"in","amount":0,"occurred_on":"2026-08-01"}}]}`, cash, setup.MainPurposeID),
			"counts.fix.amount"},
		{"a location's opening balance", "/api/accounts",
			`{"kind":"bank","name":"Bank","opening_balance":{"amount":1000,"occurred_on":"2026-13-01"}}`,
			"opening_balance.occurred_on"},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantRefusal(t, sendJSON(t, r, http.MethodPost, c.path, c.body), "invalid_argument", c.field)
		})
	}
}

// Setup takes the same opening balance one level deeper, under accounts.
func TestASetupOpeningBalanceRefusalNamesItUnderAccounts(t *testing.T) {
	t.Parallel()
	r := testRouter(t)

	body := `{"name":"Kas RT","time_zone":"Asia/Jakarta","accounts":[{"kind":"cash","name":"Tunai","opening_balance":{"amount":-5,"occurred_on":"2026-08-01"}}]}`
	wantRefusal(t, sendJSON(t, r, http.MethodPost, "/api/setup", body), "invalid_argument", "accounts.opening_balance.amount")
}

func TestABadQueryParameterIsNamed(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)

	wantRefusal(t, sendJSON(t, r, http.MethodGet, "/api/transactions?member_id=abc", ""), "invalid_argument", "member_id")
	wantRefusal(t, sendJSON(t, r, http.MethodGet, "/api/transactions?cursor=zz", ""), "invalid_argument", "cursor")
}
