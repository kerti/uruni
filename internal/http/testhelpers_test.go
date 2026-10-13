package http

import (
	"encoding/json"
	"net/http"
	"testing"
)

// setUpFund is the one fund-setup fixture this package's tests share: a fund
// with one cash and one bank account and the main purpose SetUpFund creates.
// Before #78 that pair was fixed and every test file rolled its own helper; #78
// made accounts a treasurer choice, so "which account is cash" is decided here
// once. A test needing more than the default pair (setup_test.go's
// account-count tests, accounts_test.go's multi-account cases) calls
// postSetup directly.
func setUpFund(t *testing.T, r http.Handler) setupResponse {
	t.Helper()
	rec := postSetupWithAccounts(t, r, "Test Fund", []setupAccountRequest{
		{Kind: "cash", Name: "Tunai"},
		{Kind: "bank", Name: "Bank"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var setup setupResponse
	if err := json.NewDecoder(rec.Body).Decode(&setup); err != nil {
		t.Fatalf("decoding setup response: %v", err)
	}
	return setup
}

// CashAccountID and BankAccountID find setupResponse's cash/bank account by
// Kind, not a fixed slice position: #78 dropped the fixed count and order from
// production code, so tests may not lean on either.
func (s setupResponse) CashAccountID(t *testing.T) int64 {
	t.Helper()
	return s.accountIDByKind(t, "cash")
}

func (s setupResponse) BankAccountID(t *testing.T) int64 {
	t.Helper()
	return s.accountIDByKind(t, "bank")
}

func (s setupResponse) accountIDByKind(t *testing.T, kind string) int64 {
	t.Helper()
	for _, a := range s.Accounts {
		if a.Kind == kind {
			return a.ID
		}
	}
	t.Fatalf("no account of kind %q among %+v", kind, s.Accounts)
	return 0
}
