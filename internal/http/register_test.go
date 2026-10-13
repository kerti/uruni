package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// testRouterAndDB is testRouter with the raw *sql.DB, to look at the session
// table (store.Queries has no route for "does a session row exist": that would
// be its own oracle).
func testRouterAndDB(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	sqlDB := testStoreDB(t)
	return New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir(), nil), sqlDB
}

func postRegister(t *testing.T, r http.Handler, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	//nolint:gosec // not a credential leak - this is the request body POST /api/register's own contract requires
	body, err := json.Marshal(registerRequest{Email: email, Password: password})
	if err != nil {
		t.Fatalf("marshaling register request: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewReader(body)))
	return rec
}

// sessionCookie returns the "session" cookie a response set, or "" if none,
// to check register sets one and a refused second call does not.
func sessionCookie(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			return c.Value
		}
	}
	return ""
}

// TestPostRegisterReturns201SetsTheCookieAndWritesASessionRow is the DoD's
// first line: 201, a cookie, and a real session row behind it, not only a
// Set-Cookie header.
func TestPostRegisterReturns201SetsTheCookieAndWritesASessionRow(t *testing.T) {
	t.Parallel()
	r, sqlDB := testRouterAndDB(t)

	rec := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var got userResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if got.ID == 0 {
		t.Error("id is zero")
	}
	if got.Email != "treasurer@example.org" {
		t.Errorf("email = %q, want %q", got.Email, "treasurer@example.org")
	}
	if got.CreatedAt == 0 {
		t.Error("created_at is zero")
	}

	token := sessionCookie(rec)
	if token == "" {
		t.Fatal("no session cookie was set")
	}

	sess, err := store.New(sqlDB).GetSession(context.Background(), store.GetSessionParams{
		Token: token, ExpiresAt: time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("GetSession(the cookie's token) = %v, want a session row", err)
	}
	if len(sess.Data) == 0 {
		t.Error("the session row's data is empty")
	}
}

// TestPostRegisterCookieIsHttpOnlyAndSameSiteLax pins the flags #114 fixes
// whatever URUNI_BASE_URL: httpOnly and SameSite=Lax always. Secure varies
// with baseURL's scheme and is covered separately.
func TestPostRegisterCookieIsHttpOnlyAndSameSiteLax(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	rec := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie was set")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie is not httpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", cookie.SameSite)
	}
}

// TestSessionCookieSecureFollowsBaseURLScheme (#114, ADR-019): Secure is
// derived from URUNI_BASE_URL's scheme, not a new variable. https turns it on;
// http or unset (the plain-HTTP loopback of make web-dev) leaves it off,
// since a browser never sends a Secure cookie over plain HTTP; the wrong way
// would make dev login look silently broken.
func TestSessionCookieSecureFollowsBaseURLScheme(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{"unset", "", false},
		{"http", "http://localhost:5173", false},
		{"https", "https://uruni.example.org", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sqlDB := testStoreDB(t)
			r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), tc.baseURL, t.TempDir(), t.TempDir(), nil)

			rec := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
			if rec.Code != http.StatusCreated {
				t.Fatalf("POST /api/register = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
			}

			var cookie *http.Cookie
			for _, c := range rec.Result().Cookies() {
				if c.Name == "session" {
					cookie = c
				}
			}
			if cookie == nil {
				t.Fatal("no session cookie was set")
			}
			if cookie.Secure != tc.want {
				t.Errorf("baseURL=%q: Secure = %v, want %v", tc.baseURL, cookie.Secure, tc.want)
			}
		})
	}
}

// TestPostRegisterSecondCallRefusesRegardlessOfEmail is the DoD's second line:
// a second register with a different email (ADR-030 decision 2) answers 409,
// writes no row, and changes no cookie.
func TestPostRegisterSecondCallRefusesRegardlessOfEmail(t *testing.T) {
	t.Parallel()
	r, sqlDB := testRouterAndDB(t)

	first := postRegister(t, r, "first@example.org", "correct-horse-battery")
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST /api/register = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}

	second := postRegister(t, r, "second@example.org", "another-long-enough-password")
	if second.Code != http.StatusConflict {
		t.Fatalf("second POST /api/register = %d, want %d (body: %s)", second.Code, http.StatusConflict, second.Body.String())
	}
	got := decodeError(t, second)
	if got.Code != "already_registered" {
		t.Errorf("error code = %q, want %q", got.Code, "already_registered")
	}

	if token := sessionCookie(second); token != "" {
		t.Errorf("refused register set a session cookie (token %q), want none", token)
	}

	count, err := store.New(sqlDB).CountUsers(context.Background())
	if err != nil {
		t.Fatalf("CountUsers() = %v, want no error", err)
	}
	if count != 1 {
		t.Errorf("CountUsers() = %d, want 1 (the refusal must not have written a second account)", count)
	}
}

// TestPostRegisterPasswordNeverAppearsInTheResponse is the DoD's third line's
// response half (the log half: internal/auth never interpolates the password
// into an error, and this handler never logs the request body).
func TestPostRegisterPasswordNeverAppearsInTheResponse(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	const password = "correct-horse-battery-staple-marker"
	rec := postRegister(t, r, "treasurer@example.org", password)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	if strings.Contains(rec.Body.String(), password) {
		t.Errorf("response body contains the plaintext password: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Errorf("response body mentions a password field at all: %s", rec.Body.String())
	}
}

// TestPostRegisterRejectsAShortPassword covers the mapping for
// auth.ErrInvalidArgument: 400, not 500, and no row written.
func TestPostRegisterRejectsAShortPassword(t *testing.T) {
	t.Parallel()
	r, sqlDB := testRouterAndDB(t)

	rec := postRegister(t, r, "treasurer@example.org", "short")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/register(short password) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_argument")
	}

	count, err := store.New(sqlDB).CountUsers(context.Background())
	if err != nil {
		t.Fatalf("CountUsers() = %v, want no error", err)
	}
	if count != 0 {
		t.Errorf("CountUsers() = %d, want 0", count)
	}
}

// TestPostRegisterRejectsAMalformedBody: decodeJSON's refusal, before
// internal/auth: a body that is not JSON is a 400 and writes nothing.
func TestPostRegisterRejectsAMalformedBody(t *testing.T) {
	t.Parallel()
	r, sqlDB := testRouterAndDB(t)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader("{not json")))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/register(malformed body) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}

	count, err := store.New(sqlDB).CountUsers(context.Background())
	if err != nil {
		t.Fatalf("CountUsers() = %v, want no error", err)
	}
	if count != 0 {
		t.Errorf("CountUsers() = %d, want 0", count)
	}
}

// TestPostRegisterFailsClosedWhenTheSessionCannotBeRenewed is the fixation
// guard's failure arm. RenewToken deletes the arriving token before minting a
// new one; if that delete fails, the handler must answer 500 and put nothing
// in the session, since carrying on would leave her identity on a token that
// existed before she was known.
//
// Only one setup reaches it: the request needs an existing session (else
// nothing to delete) while the instance has no account (else Register refuses
// first). A first register makes both, the user row is then removed, and a
// trigger refuses the delete the second attempt's RenewToken makes.
func TestPostRegisterFailsClosedWhenTheSessionCannotBeRenewed(t *testing.T) {
	t.Parallel()
	r, sqlDB := testRouterAndDB(t)

	first := postRegister(t, r, "first@example.org", "correct-horse-battery")
	if first.Code != http.StatusCreated {
		t.Fatalf("first POST /api/register = %d, want %d (body: %s)", first.Code, http.StatusCreated, first.Body.String())
	}
	token := sessionCookie(first)
	if token == "" {
		t.Fatal("register set no session cookie")
	}

	if _, err := sqlDB.Exec("DELETE FROM user"); err != nil {
		t.Fatalf("removing the user row: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER refuse_session_delete BEFORE DELETE ON session
		BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatalf("creating the trigger: %v", err)
	}

	//nolint:gosec // not a credential leak - this is the request body POST /api/register's own contract requires
	body, err := json.Marshal(registerRequest{Email: "second@example.org", Password: "another-long-enough-password"})
	if err != nil {
		t.Fatalf("marshaling register request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "session", Value: token}) //nolint:gosec // request-side cookie carries name and value only
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /api/register with an unrenewable session = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if got := decodeError(t, rec).Code; got != "internal_error" {
		t.Errorf("error code = %q, want %q", got, "internal_error")
	}

	// The old token must not have become the one carrying the new account.
	var userID sql.NullInt64
	if err := sqlDB.QueryRow("SELECT 1 FROM session WHERE token = ?", token).Scan(&userID); err != nil {
		t.Fatalf("the pre-existing session row is gone: %v", err)
	}
}
