package http

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// #386: the treasurer changes her own email and password. Hashing is stubbed
// (as limitsRouter does) so the eleven-attempt lockout test costs no argon2id;
// the real hash path is auth's own tests.

const (
	userEmail    = "treasurer@example.org"
	userPassword = "correct-horse-battery"
	userIP       = "203.0.113.10"
)

type userEnv struct {
	r    http.Handler
	db   *sql.DB
	logs *bytes.Buffer
	// a is the session making the change (the cookie it holds now: a change
	// renews it, and changeEmail/changePassword adopt the new one), b another
	// device's.
	a, b string
}

// newUserEnv is a router with one registered treasurer and two sessions. opts
// follow the default stubbed hashing, so a test may replace it.
func newUserEnv(t *testing.T, opts ...auth.Option) *userEnv {
	t.Helper()
	sqlDB := testStoreDB(t)
	au := auth.New(sqlDB, append([]auth.Option{auth.WithPasswordFuncs(
		func(p string) (string, error) { return "stub:" + p, nil },
		func(p, encoded string) (bool, error) { return encoded == "stub:"+p, nil },
	)}, opts...)...)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	r := newWithClock(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, logger, au, "", t.TempDir(), t.TempDir(), nil, newFakeClock().now)

	reg := postRegister(t, r, userEmail, userPassword)
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}
	login := loginWith(t, r, userEmail, userPassword, "198.51.100.7")
	if login.Code != http.StatusOK {
		t.Fatalf("second login = %d, want %d", login.Code, http.StatusOK)
	}
	e := &userEnv{r: r, db: sqlDB, logs: &logs, a: sessionCookie(reg), b: sessionCookie(login)}
	if e.a == "" || e.b == "" || e.a == e.b {
		t.Fatalf("want two distinct sessions, got %q and %q", e.a, e.b)
	}
	return e
}

// call sends one request as the session cookie names ("" for none), from userIP.
func (e *userEnv) call(t *testing.T, method, path, cookie string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return e.callFrom(t, userIP, method, path, cookie, body)
}

func (e *userEnv) callFrom(t *testing.T, ip, method, path, cookie string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encoding body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = net.JoinHostPort(ip, "1234")
	if cookie != "" {
		req.AddCookie(requestCookie("session", cookie))
	}
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

func (e *userEnv) changeEmail(t *testing.T, cookie, email, current string) *httptest.ResponseRecorder {
	t.Helper()
	return e.adopt(cookie, e.call(t, http.MethodPost, "/api/user/email", cookie, changeEmailRequest{Email: email, CurrentPassword: current}))
}

func (e *userEnv) changePassword(t *testing.T, cookie, next, current string) *httptest.ResponseRecorder {
	t.Helper()
	return e.changePasswordFrom(t, userIP, cookie, next, current)
}

func (e *userEnv) changePasswordFrom(t *testing.T, ip, cookie, next, current string) *httptest.ResponseRecorder {
	t.Helper()
	return e.adopt(cookie, e.callFrom(t, ip, http.MethodPost, "/api/user/password", cookie, changePasswordRequest{NewPassword: next, CurrentPassword: current}))
}

// adopt takes the renewed session cookie a successful change sets, when the
// request rode e.a.
func (e *userEnv) adopt(cookie string, rec *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	if c := sessionCookie(rec); c != "" && cookie == e.a {
		e.a = c
	}
	return rec
}

// sessionRows counts the rows in the session table.
func (e *userEnv) sessionRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM session`).Scan(&n); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	return n
}

func (e *userEnv) wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, status, rec.Body.String())
	}
	if code == "" {
		return
	}
	var env errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decoding error envelope: %v", err)
	}
	if env.Error.Code != code {
		t.Errorf("error code = %q, want %q", env.Error.Code, code)
	}
}

// alive reports whether a cookie still opens a gated route.
func (e *userEnv) alive(t *testing.T, cookie string) bool {
	t.Helper()
	switch rec := e.call(t, http.MethodGet, "/api/user", cookie, nil); rec.Code {
	case http.StatusOK:
		return true
	case http.StatusUnauthorized:
		return false
	default:
		t.Fatalf("GET /api/user = %d, want 200 or 401", rec.Code)
		return false
	}
}

func (e *userEnv) loginStatus(t *testing.T, email, password string) int {
	t.Helper()
	return loginWith(t, e.r, email, password, "192.0.2.1").Code
}

func TestGetUserReturnsTheEmailAndNoHash(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	rec := e.call(t, http.MethodGet, "/api/user", e.a, nil)
	e.wantStatus(t, rec, http.StatusOK, "")
	var got userResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Email != userEmail {
		t.Errorf("email = %q, want %q", got.Email, userEmail)
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "hash") || strings.Contains(rec.Body.String(), "stub:") {
		t.Errorf("body leaks the password hash: %s", rec.Body.String())
	}
}

func TestUserRoutesNeedASession(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	e.wantStatus(t, e.call(t, http.MethodGet, "/api/user", "", nil), http.StatusUnauthorized, "unauthenticated")
	e.wantStatus(t, e.changeEmail(t, "", "new@example.org", userPassword), http.StatusUnauthorized, "unauthenticated")
	e.wantStatus(t, e.changePassword(t, "", "a-new-password", userPassword), http.StatusUnauthorized, "unauthenticated")
}

func TestChangeEmailSwitchesTheLoginAndSignsOutOtherSessions(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)
	old := e.a

	rec := e.changeEmail(t, e.a, "  New.Address@Example.ORG ", userPassword)
	e.wantStatus(t, rec, http.StatusOK, "")
	if got := e.sessionRows(t); got != 1 {
		t.Errorf("session rows = %d, want only the renewed session", got)
	}
	var got userResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Email != "new.address@example.org" {
		t.Errorf("email = %q, want the normalised form", got.Email)
	}

	if e.loginStatus(t, "new.address@example.org", userPassword) != http.StatusOK {
		t.Error("the new email does not log in")
	}
	if e.loginStatus(t, userEmail, userPassword) != http.StatusUnauthorized {
		t.Error("the old email still logs in")
	}
	if e.a == old {
		t.Error("the session token was not renewed by the change")
	}
	if e.alive(t, old) {
		t.Error("the old token of the session that made the change still works")
	}
	if !e.alive(t, e.a) {
		t.Error("the session that made the change was signed out")
	}
	if e.alive(t, e.b) {
		t.Error("another device's session survived the change")
	}
	if user := e.call(t, http.MethodGet, "/api/user", e.a, nil); !strings.Contains(user.Body.String(), "new.address@example.org") {
		t.Errorf("GET /api/user after the change = %s, want the new email", user.Body.String())
	}
}

func TestChangePasswordSwitchesTheLoginAndSignsOutOtherSessions(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	old := e.a
	e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", userPassword), http.StatusNoContent, "")
	if got := e.sessionRows(t); got != 1 {
		t.Errorf("session rows = %d, want only the renewed session", got)
	}

	if e.loginStatus(t, userEmail, "a-brand-new-secret") != http.StatusOK {
		t.Error("the new password does not log in")
	}
	if e.loginStatus(t, userEmail, userPassword) != http.StatusUnauthorized {
		t.Error("the old password still logs in")
	}
	if e.a == old {
		t.Error("the session token was not renewed by the change")
	}
	if e.alive(t, old) {
		t.Error("the old token of the session that made the change still works")
	}
	if !e.alive(t, e.a) {
		t.Error("the session that made the change was signed out")
	}
	if e.alive(t, e.b) {
		t.Error("another device's session survived the change")
	}
}

func TestAWrongCurrentPasswordChangesNothingAndHasItsOwnCode(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	e.wantStatus(t, e.changeEmail(t, e.a, "new@example.org", "not-the-password"), http.StatusForbidden, "wrong_current_password")
	e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", "not-the-password"), http.StatusForbidden, "wrong_current_password")

	if e.loginStatus(t, userEmail, userPassword) != http.StatusOK {
		t.Error("the original login stopped working after refused changes")
	}
	if !e.alive(t, e.b) {
		t.Error("a refused change signed another session out")
	}
}

func TestWrongCurrentPasswordsCountTowardsOneLimitAcrossBothRoutes(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		var rec *httptest.ResponseRecorder
		if i%2 == 0 {
			rec = e.changeEmail(t, e.a, "new@example.org", "not-the-password")
		} else {
			rec = e.changePassword(t, e.a, "a-brand-new-secret", "not-the-password")
		}
		e.wantStatus(t, rec, http.StatusForbidden, "wrong_current_password")
	}
	// The next attempt is refused before the password is looked at: the right one
	// included.
	e.wantStatus(t, e.changeEmail(t, e.a, "new@example.org", "not-the-password"), http.StatusTooManyRequests, "too_many_requests")
	e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", userPassword), http.StatusTooManyRequests, "too_many_requests")

	if e.loginStatus(t, userEmail, userPassword) != http.StatusOK {
		t.Error("the original password was changed while locked out")
	}
	if !strings.Contains(e.logs.String(), "email_change_refused") || !strings.Contains(e.logs.String(), "reason=locked_out") {
		t.Errorf("log lacks the locked-out security event:\n%s", e.logs.String())
	}
}

// The change budget is its own: filling it leaves login, which another
// device uses, untouched.
func TestTheUserLockoutDoesNotLockLoginOut(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", "not-the-password"), http.StatusForbidden, "")
	}
	e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", userPassword), http.StatusTooManyRequests, "")
	if got := loginWith(t, e.r, userEmail, userPassword, userIP).Code; got != http.StatusOK {
		t.Errorf("login from the locked-out address = %d, want %d", got, http.StatusOK)
	}
}

func TestASuccessfulChangeClearsTheWrongGuesses(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	for i := 0; i < loginRateLimitMaxAttempts-1; i++ {
		e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", "not-the-password"), http.StatusForbidden, "")
	}
	e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", userPassword), http.StatusNoContent, "")
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		e.wantStatus(t, e.changePassword(t, e.a, "another-new-secret", "not-the-password"), http.StatusForbidden, "")
	}
}

func TestAShortNewPasswordIsRefusedAndIsNoGuess(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	for i := 0; i < loginRateLimitMaxAttempts+1; i++ {
		e.wantStatus(t, e.changePassword(t, e.a, "short", userPassword), http.StatusBadRequest, "invalid_argument")
	}
	// None of those spent the budget, and none changed anything.
	e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", "not-the-password"), http.StatusForbidden, "wrong_current_password")
	if e.loginStatus(t, userEmail, userPassword) != http.StatusOK {
		t.Error("a refused short password changed the login")
	}
	if !e.alive(t, e.b) {
		t.Error("a refused change signed another session out")
	}
	// Exactly the minimum is accepted.
	e.wantStatus(t, e.changePassword(t, e.a, strings.Repeat("x", auth.MinPasswordLength), userPassword), http.StatusNoContent, "")
}

func TestAMalformedNewEmailIsRefusedAndIsNoGuess(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	for _, bad := range []string{"", "   ", "no-at-sign"} {
		for i := 0; i < loginRateLimitMaxAttempts; i++ {
			e.wantStatus(t, e.changeEmail(t, e.a, bad, userPassword), http.StatusBadRequest, "invalid_argument")
		}
	}
	e.wantStatus(t, e.changeEmail(t, e.a, "new@example.org", "not-the-password"), http.StatusForbidden, "wrong_current_password")
	if e.loginStatus(t, userEmail, userPassword) != http.StatusOK {
		t.Error("a refused email changed the login")
	}
}

func TestCredentialChangesAreLoggedWithoutSecrets(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	e.changeEmail(t, e.a, "secret.new@example.org", "not-the-password")
	e.changePassword(t, e.a, "never-log-this-one", "not-the-password")
	e.changeEmail(t, e.a, "secret.new@example.org", userPassword)
	e.changePassword(t, e.a, "never-log-this-one", userPassword)

	logs := e.logs.String()
	for _, want := range []string{"email_change_refused", "password_change_refused", "reason=wrong_password", "event=email_changed", "event=password_changed"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log lacks %q:\n%s", want, logs)
		}
	}
	for _, secret := range []string{"secret.new@example.org", userEmail, "never-log-this-one", userPassword, "not-the-password"} {
		if strings.Contains(logs, secret) {
			t.Errorf("log contains %q", secret)
		}
	}
}
