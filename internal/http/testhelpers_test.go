package http

import (
	"encoding/json"
	"net/http"
	"testing"
)

// setUpFund is the one fund-setup fixture this whole package's tests share:
// a fund with one cash and one bank account and the main purpose SetUpFund
// creates. Before #78 (M6.1) that pair was fixed and every test file in this
// package rolled its own setUpFundForX helper (or inlined the same
// postSetup-then-decode steps) to reach it; #78 made accounts a treasurer
// choice, which is exactly the moment "which account is cash" needs to be
// decided in one place instead of reinterpreted at every call site. Nothing
// in this package's own tests needs more than the default pair - a test that
// does (setup_test.go's own account-count tests, accounts_test.go's
// multi-account cases) calls postSetup directly instead.
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

// CashAccountID and BankAccountID find setupResponse's own cash/bank account
// by Kind rather than assuming a fixed slice position - #78 dropped both the
// fixed count and the fixed order from production code, so a test reading a
// setup response back out does not get to lean on either.
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
