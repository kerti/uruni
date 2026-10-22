package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// gatedRoutes is every route #116 moved behind sessionRequired, transcribed
// from api.go's second r.Group in registration order, including POST /setup
// (ADR-030's ruling and the point of the gate). A path segment naming an id
// ({id}, {purposeID}) is a placeholder: sessionRequired runs before chi
// resolves it or any handler reads it, so only that a route exists matters.
// An explicit table, not derived from the router, so a route added without
// rejoining this group is caught; TestGatedRouteTableMatchesTheRouter makes
// that true, since alone this table tests only the routes someone remembered.
var gatedRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/user"},
	{http.MethodPost, "/api/user/email"},
	{http.MethodPost, "/api/user/password"},
	{http.MethodPost, "/api/setup"},
	{http.MethodGet, "/api/fund"},
	{http.MethodPatch, "/api/fund"},
	{http.MethodPost, "/api/fund/report-slug"},
	{http.MethodPost, "/api/accounts"},
	{http.MethodGet, "/api/accounts"},
	{http.MethodPatch, "/api/accounts/1"},
	{http.MethodDelete, "/api/accounts/1"},
	{http.MethodGet, "/api/purposes"},
	{http.MethodPost, "/api/pass-through-purposes"},
	{http.MethodPatch, "/api/purposes/1"},
	{http.MethodPost, "/api/members"},
	{http.MethodGet, "/api/members"},
	{http.MethodPatch, "/api/members/1"},
	{http.MethodDelete, "/api/members/1"},
	{http.MethodPost, "/api/dues-tiers"},
	{http.MethodGet, "/api/dues-tiers"},
	{http.MethodPatch, "/api/dues-tiers/1"},
	{http.MethodDelete, "/api/dues-tiers/1"},
	{http.MethodPost, "/api/dues-tiers/1/rates"},
	{http.MethodPatch, "/api/dues-rates/1"},
	{http.MethodDelete, "/api/dues-rates/1"},
	{http.MethodPost, "/api/transactions"},
	{http.MethodGet, "/api/transactions"},
	{http.MethodPost, "/api/transactions/1/purpose-correction"},
	{http.MethodPost, "/api/transfers"},
	{http.MethodPost, "/api/purpose-moves"},
	{http.MethodPost, "/api/reimbursements"},
	{http.MethodGet, "/api/reimbursements"},
	{http.MethodGet, "/api/reimbursements/1"},
	{http.MethodPatch, "/api/reimbursements/1"},
	{http.MethodDelete, "/api/reimbursements/1"},
	{http.MethodPost, "/api/reimbursements/1/settle"},
	{http.MethodPost, "/api/dues-payments"},
	{http.MethodPost, "/api/transactions/1/reversal"},
	{http.MethodGet, "/api/dues-payments"},
	{http.MethodGet, "/api/dues-status"},
	{http.MethodGet, "/api/members/1/outstanding-dues"},
	{http.MethodPost, "/api/incidentals"},
	{http.MethodGet, "/api/incidentals"},
	{http.MethodGet, "/api/incidentals/1"},
	{http.MethodPatch, "/api/incidentals/1"},
	{http.MethodPost, "/api/incidentals/1/close"},
	{http.MethodPost, "/api/incidentals/1/reopen"},
	{http.MethodGet, "/api/incidentals/1/participation"},
	{http.MethodPost, "/api/reconciliations"},
	{http.MethodGet, "/api/reconciliations"},
	{http.MethodGet, "/api/reconciliations/latest"},
	{http.MethodGet, "/api/reconciliations/open-lines"},
	{http.MethodGet, "/api/reconciliations/1"},
	{http.MethodGet, "/api/balances"},
	{http.MethodPost, "/api/transactions/1/receipts"},
	{http.MethodPost, "/api/reimbursements/1/receipts"},
	{http.MethodGet, "/api/receipts/1"},
	{http.MethodDelete, "/api/receipts/1"},
	{http.MethodGet, "/api/backup"},
	{http.MethodGet, "/api/backups"},
	{http.MethodGet, "/api/backups/uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip"},
	{http.MethodPost, "/api/restore/inspect"},
	{http.MethodPost, "/api/restore/inspect-stored/uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip"},
	{http.MethodPost, "/api/restore/confirm"},
}

// TestEveryGatedRouteReturns401WithNoSession is the DoD's first line: every
// previously-open /api route answers 401 "unauthenticated" with no session
// cookie. It uses testRouterAndDB, not testRouter, whose purpose is the
// opposite (see authedRouterFor).
func TestEveryGatedRouteReturns401WithNoSession(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	for _, tc := range gatedRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, bytes.NewReader([]byte("{}"))))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s with no session = %d, want %d (body: %s)",
					tc.method, tc.path, rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
			got := decodeError(t, rec)
			if got.Code != "unauthenticated" {
				t.Errorf("error code = %q, want %q", got.Code, "unauthenticated")
			}
		})
	}
}

// TestGatedRouteTableMatchesTheRouter closes the table's gap. The sweep
// proves every gatedRoutes entry is registered and gated (an unregistered
// path would 404, not 401), but only "the table is a subset of the gate"; a
// route added to api.go's gated group and forgotten here would never be swept.
//
// Counting the router's registered routes closes it from the other side: a
// subset of equal size is the whole set, so together the tests make the sweep
// cover the gated surface exactly, and adding a route to either place without
// the other fails here.
func TestGatedRouteTableMatchesTheRouter(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	mux, ok := r.(chi.Routes)
	if !ok {
		t.Fatalf("router is %T, want a chi.Routes - this test walks the real routing table, not a copy of it", r)
	}

	// The four routes #116 left reachable without a session. Named, not counted
	// as a bare 4, so a fifth public route is a decision written down here beside
	// the reason for the other four.
	publicRoutes := map[string]bool{
		"POST /api/register": true,
		"POST /api/login":    true,
		"GET /api/session":   true,
		"POST /api/logout":   true,
	}

	registered := 0
	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/") {
			return nil
		}
		if publicRoutes[method+" "+strings.TrimSuffix(route, "/")] {
			return nil
		}
		registered++
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk() = %v, want no error", err)
	}

	if registered != len(gatedRoutes) {
		t.Errorf("router registers %d gated /api routes, gatedRoutes lists %d - a route was added to one and not the other, so the sweep above no longer covers the whole gate", registered, len(gatedRoutes))
	}
}

// TestGetSessionNeverReturns401: a booting, logged-out client is GET
// /api/session's one caller that must reach it without a cookie; a 401 would
// leave no way to learn that logging in is an option.
func TestGetSessionNeverReturns401(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/session with no cookie = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestPostLogoutNeverReturns401AndIs204WithOrWithoutASession is the DoD's
// logout half: 204 with or without a session, never 401; a caller with an
// expired cookie is asking for what logout gives.
func TestPostLogoutNeverReturns401AndIs204WithOrWithoutASession(t *testing.T) {
	t.Parallel()
	t.Run("no session at all", func(t *testing.T) {
		r, _ := testRouterAndDB(t)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/logout", nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("POST /api/logout with no session = %d, want %d (body: %s)", rec.Code, http.StatusNoContent, rec.Body.String())
		}
	})

	t.Run("a real session", func(t *testing.T) {
		r, _ := testRouterAndDB(t)
		reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
		if reg.Code != http.StatusCreated {
			t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
		}
		token := sessionCookie(reg)
		if token == "" {
			t.Fatal("register set no session cookie")
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token}) //nolint:gosec // test fixture cookie, not a credential
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("POST /api/logout with a real session = %d, want %d (body: %s)", rec.Code, http.StatusNoContent, rec.Body.String())
		}
	})
}

// TestGetSessionShapeAcrossReachableStates is the DoD's session-shape line:
// {authenticated, has_account} across every combination a real client can
// land in. The fourth, authenticated with no account, has no path through the
// API (nothing deletes the one account a session can name), so it is not
// modeled.
func TestGetSessionShapeAcrossReachableStates(t *testing.T) {
	t.Parallel()
	t.Run("no account registered yet", func(t *testing.T) {
		r, _ := testRouterAndDB(t)
		got := getSessionStatus(t, r, "")
		want := sessionStatusResponse{Authenticated: false, HasAccount: false}
		if got != want {
			t.Errorf("GET /api/session = %+v, want %+v", got, want)
		}
	})

	t.Run("account exists but this caller is logged out", func(t *testing.T) {
		r, _ := testRouterAndDB(t)
		reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
		if reg.Code != http.StatusCreated {
			t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
		}
		token := sessionCookie(reg)
		if token == "" {
			t.Fatal("register set no session cookie")
		}

		// Logged out with that cookie, then asked again with none: what a second,
		// never-authenticated tab (or this one after logout) sees.
		logoutRec := httptest.NewRecorder()
		logoutReq := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
		logoutReq.AddCookie(&http.Cookie{Name: "session", Value: token}) //nolint:gosec // test fixture cookie, not a credential
		r.ServeHTTP(logoutRec, logoutReq)
		if logoutRec.Code != http.StatusNoContent {
			t.Fatalf("POST /api/logout = %d, want %d", logoutRec.Code, http.StatusNoContent)
		}

		got := getSessionStatus(t, r, "")
		want := sessionStatusResponse{Authenticated: false, HasAccount: true}
		if got != want {
			t.Errorf("GET /api/session after logout = %+v, want %+v", got, want)
		}
	})

	t.Run("logged in", func(t *testing.T) {
		r, _ := testRouterAndDB(t)
		reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
		if reg.Code != http.StatusCreated {
			t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
		}
		token := sessionCookie(reg)
		if token == "" {
			t.Fatal("register set no session cookie")
		}

		got := getSessionStatus(t, r, token)
		want := sessionStatusResponse{Authenticated: true, HasAccount: true}
		if got != want {
			t.Errorf("GET /api/session while logged in = %+v, want %+v", got, want)
		}
	})
}

// getSessionStatus is GET /api/session's request helper; token == "" sends no
// cookie, the no-cookie idiom of the other helpers.
func getSessionStatus(t *testing.T, r http.Handler, token string) sessionStatusResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: token}) //nolint:gosec // test fixture cookie, not a credential
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/session = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got sessionStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding GET /api/session response: %v", err)
	}
	return got
}

// TestFirstRunWalksRegisterSetupLogoutLoginThenAFundScopedRoute is the DoD's
// integration line: the path a real first run takes, the cookie carried by
// hand call to call as in
// TestPostRegisterFailsClosedWhenTheSessionCannotBeRenewed (register_test.go).
func TestFirstRunWalksRegisterSetupLogoutLoginThenAFundScopedRoute(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}
	registerToken := sessionCookie(reg)
	if registerToken == "" {
		t.Fatal("register set no session cookie")
	}

	setupBody, err := json.Marshal(setupRequest{
		Name:     "Dana Warga RT 05",
		Accounts: []setupAccountRequest{{Kind: "cash", Name: "Tunai"}},
	})
	if err != nil {
		t.Fatalf("marshaling setup request: %v", err)
	}
	setupReq := httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewReader(setupBody))
	setupReq.AddCookie(&http.Cookie{Name: "session", Value: registerToken}) //nolint:gosec // test fixture cookie, not a credential
	setupRec := httptest.NewRecorder()
	r.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/setup = %d, want %d (body: %s)", setupRec.Code, http.StatusCreated, setupRec.Body.String())
	}
	var setup setupResponse
	if err := json.NewDecoder(setupRec.Body).Decode(&setup); err != nil {
		t.Fatalf("decoding setup response: %v", err)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: "session", Value: registerToken}) //nolint:gosec // test fixture cookie, not a credential
	logoutRec := httptest.NewRecorder()
	r.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("POST /api/logout = %d, want %d", logoutRec.Code, http.StatusNoContent)
	}

	// The destroyed session's cookie must no longer open the gate, or logout
	// would not log anyone out.
	afterLogoutReq := httptest.NewRequest(http.MethodGet, "/api/fund", nil)
	afterLogoutReq.AddCookie(&http.Cookie{Name: "session", Value: registerToken}) //nolint:gosec // test fixture cookie, not a credential
	afterLogoutRec := httptest.NewRecorder()
	r.ServeHTTP(afterLogoutRec, afterLogoutReq)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/fund with the logged-out cookie = %d, want %d", afterLogoutRec.Code, http.StatusUnauthorized)
	}

	login := postLogin(t, r, "treasurer@example.org", "correct-horse-battery")
	if login.Code != http.StatusOK {
		t.Fatalf("POST /api/login = %d, want %d (body: %s)", login.Code, http.StatusOK, login.Body.String())
	}
	loginToken := sessionCookie(login)
	if loginToken == "" {
		t.Fatal("login set no session cookie")
	}

	fundReq := httptest.NewRequest(http.MethodGet, "/api/fund", nil)
	fundReq.AddCookie(&http.Cookie{Name: "session", Value: loginToken}) //nolint:gosec // test fixture cookie, not a credential
	fundRec := httptest.NewRecorder()
	r.ServeHTTP(fundRec, fundReq)
	if fundRec.Code != http.StatusOK {
		t.Fatalf("GET /api/fund after logging back in = %d, want %d (body: %s)", fundRec.Code, http.StatusOK, fundRec.Body.String())
	}
	var fund fundResponse
	if err := json.NewDecoder(fundRec.Body).Decode(&fund); err != nil {
		t.Fatalf("decoding fund response: %v", err)
	}
	if fund.ID != setup.Fund.ID {
		t.Errorf("GET /api/fund id = %d, want %d (the fund setup created)", fund.ID, setup.Fund.ID)
	}
}
