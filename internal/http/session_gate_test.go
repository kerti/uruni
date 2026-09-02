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
// from api.go's second r.Group in registration order - including POST
// /setup, ADR-030's explicit ruling and the whole point of this slice. A
// path segment that names an id in the real route ({id}, {purposeID}) is
// filled with a placeholder value here: sessionRequired runs before chi
// resolves the wildcard or any handler reads it, so what the placeholder
// says does not matter, only that a route exists there for the sweep to
// hit. Kept as its own explicit table, not derived from the router at
// runtime, so a route added later without rejoining this group is caught
// rather than silently passing by construction.
// TestGatedRouteTableMatchesTheRouter below is what makes that true: on its
// own, this table would only ever test the routes someone remembered to add
// to it.
var gatedRoutes = []struct {
	method string
	path   string
}{
	{http.MethodPost, "/api/setup"},
	{http.MethodGet, "/api/fund"},
	{http.MethodPost, "/api/accounts"},
	{http.MethodGet, "/api/accounts"},
	{http.MethodPatch, "/api/accounts/1"},
	{http.MethodDelete, "/api/accounts/1"},
	{http.MethodPost, "/api/accounts/1/opening-balance"},
	{http.MethodGet, "/api/purposes"},
	{http.MethodPost, "/api/pass-through-purposes"},
	{http.MethodPost, "/api/members"},
	{http.MethodGet, "/api/members"},
	{http.MethodPatch, "/api/members/1"},
	{http.MethodDelete, "/api/members/1"},
	{http.MethodPost, "/api/dues-tiers"},
	{http.MethodGet, "/api/dues-tiers"},
	{http.MethodPatch, "/api/dues-tiers/1"},
	{http.MethodPost, "/api/dues-tiers/1/rates"},
	{http.MethodGet, "/api/dues-tiers/1/rates"},
	{http.MethodPatch, "/api/dues-rates/1"},
	{http.MethodDelete, "/api/dues-rates/1"},
	{http.MethodPost, "/api/transactions"},
	{http.MethodGet, "/api/transactions"},
	{http.MethodPost, "/api/transfers"},
	{http.MethodPost, "/api/reimbursements"},
	{http.MethodGet, "/api/reimbursements"},
	{http.MethodPatch, "/api/reimbursements/1"},
	{http.MethodDelete, "/api/reimbursements/1"},
	{http.MethodPost, "/api/reimbursements/1/settle"},
	{http.MethodPost, "/api/dues-payments"},
	{http.MethodPost, "/api/dues-payments/1/reversal"},
	{http.MethodGet, "/api/dues-status"},
	{http.MethodPost, "/api/incidentals"},
	{http.MethodGet, "/api/incidentals"},
	{http.MethodGet, "/api/incidentals/1"},
	{http.MethodPost, "/api/incidentals/1/close"},
	{http.MethodPost, "/api/reconciliations"},
	{http.MethodGet, "/api/reconciliations"},
	{http.MethodGet, "/api/reconciliations/latest"},
	{http.MethodGet, "/api/reconciliations/open-lines"},
	{http.MethodGet, "/api/reconciliations/1"},
	{http.MethodGet, "/api/balances"},
}

// TestEveryGatedRouteReturns401WithNoSession is the DoD's first line: every
// previously-open /api route answers 401 with the "unauthenticated" code
// when the caller carries no session cookie at all - testRouterAndDB, not
// testRouter, since testRouter's whole purpose is the opposite of what this
// test needs (see authedRouterFor's own comment).
func TestEveryGatedRouteReturns401WithNoSession(t *testing.T) {
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

// TestGatedRouteTableMatchesTheRouter closes the gap the table above would
// otherwise leave. The sweep proves every entry in gatedRoutes is registered
// and gated - an unregistered path would 404, not 401 - but that is only
// "the table is a subset of the gate." It says nothing about a route added
// to api.go's gated group and forgotten here, which would then never be
// swept at all: the exact failure this table exists to catch, passing
// silently.
//
// Counting the router's own registered routes closes it from the other
// side. A subset of equal size is the whole set, so together the two tests
// mean the sweep covers the gated surface exactly, and adding a route to
// either place without the other fails here.
func TestGatedRouteTableMatchesTheRouter(t *testing.T) {
	r, _ := testRouterAndDB(t)

	mux, ok := r.(chi.Routes)
	if !ok {
		t.Fatalf("router is %T, want a chi.Routes - this test walks the real routing table, not a copy of it", r)
	}

	// The four routes #116 deliberately left reachable without a session.
	// Named here rather than counted as a bare 4 so that adding a fifth
	// public route is a decision someone has to write down in this test,
	// beside the reason the other four are allowed.
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

// TestGetSessionNeverReturns401 covers the other line of the same DoD rule:
// a booting, logged-out client is GET /api/session's one caller that must
// reach it with no cookie at all, so a 401 there would be self-defeating -
// there would be no way left to learn that logging in is even an option.
func TestGetSessionNeverReturns401(t *testing.T) {
	r, _ := testRouterAndDB(t)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/session with no cookie = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// TestPostLogoutNeverReturns401AndIs204WithOrWithoutASession is the DoD's
// logout half: 204 whether or not the caller had anything to log out of,
// never 401 either way - a caller with an already-expired cookie is asking
// for exactly what logout gives.
func TestPostLogoutNeverReturns401AndIs204WithOrWithoutASession(t *testing.T) {
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
// actually land in. The fourth mathematically possible combination -
// authenticated with no account - has no path through this app's API at
// all (nothing ever deletes the one account a session can name), so it is
// not modeled here the same way this package leaves other API-unreachable
// states untested elsewhere.
func TestGetSessionShapeAcrossReachableStates(t *testing.T) {
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

		// Logged out with that very cookie, then asked again with no cookie
		// at all - the shape a second, never-authenticated browser tab (or
		// this one after logout) would see.
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

// getSessionStatus is GET /api/session's own request helper - token == ""
// sends no cookie at all, matching the no-cookie idiom every other helper
// in this package uses (plain httptest.NewRequest with nothing added).
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
// integration line: the one path a real first run actually takes, cookie
// carried by hand from one call to the next exactly the way
// TestPostRegisterFailsClosedWhenTheSessionCannotBeRenewed (register_test.go)
// already does, rather than inventing a second way to attach it.
func TestFirstRunWalksRegisterSetupLogoutLoginThenAFundScopedRoute(t *testing.T) {
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

	// The destroyed session's own cookie must no longer open the gate -
	// otherwise logout would not actually have logged anyone out.
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
