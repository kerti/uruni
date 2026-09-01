package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

func postLogin(t *testing.T, r http.Handler, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	return postLoginFrom(t, r, email, password, "")
}

// postLoginFrom is postLogin's twin for the rate-limit tests, which need
// each request to carry its own source IP - X-Forwarded-For, since
// httptest.NewRequest pins RemoteAddr to the same fixed address on every
// call (see clientIP's own comment on why that header is trusted at all).
// ip == "" leaves the default in place, so postLogin above is exactly this
// with no header added.
func postLoginFrom(t *testing.T, r http.Handler, email, password, ip string) *httptest.ResponseRecorder {
	t.Helper()
	//nolint:gosec // not a credential leak - this is the request body POST /api/login's own contract requires
	body, err := json.Marshal(loginRequest{Email: email, Password: password})
	if err != nil {
		t.Fatalf("marshaling login request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestPostLoginReturns200SetsTheCookieAndWritesASessionRow is the DoD's
// first line: the correct password logs the treasurer in exactly the way
// register.go's own equivalent test proves - 200, a cookie, and a real row
// behind it.
func TestPostLoginReturns200SetsTheCookieAndWritesASessionRow(t *testing.T) {
	r, sqlDB := testRouterAndDB(t)

	reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}

	rec := postLogin(t, r, "treasurer@example.org", "correct-horse-battery")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/login = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var got userResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding response: %v (body: %s)", err, rec.Body.String())
	}
	if got.Email != "treasurer@example.org" {
		t.Errorf("email = %q, want %q", got.Email, "treasurer@example.org")
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

// TestPostLoginWrongPasswordAndUnknownEmailReturnByteIdenticalBodies is the
// DoD's second line, at the wire level: both failure causes must answer
// 401 with the exact same bytes, or the response itself becomes the
// account-existence oracle auth.Authenticate's own doc comment forbids.
func TestPostLoginWrongPasswordAndUnknownEmailReturnByteIdenticalBodies(t *testing.T) {
	r, _ := testRouterAndDB(t)

	reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}

	wrongPassword := postLogin(t, r, "treasurer@example.org", "not-the-password")
	unknownEmail := postLogin(t, r, "nobody@example.org", "not-the-password")

	if wrongPassword.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password POST /api/login = %d, want %d (body: %s)", wrongPassword.Code, http.StatusUnauthorized, wrongPassword.Body.String())
	}
	if unknownEmail.Code != http.StatusUnauthorized {
		t.Fatalf("unknown email POST /api/login = %d, want %d (body: %s)", unknownEmail.Code, http.StatusUnauthorized, unknownEmail.Body.String())
	}
	if wrongPassword.Body.String() != unknownEmail.Body.String() {
		t.Errorf("bodies differ:\nwrong password: %s\nunknown email:  %s", wrongPassword.Body.String(), unknownEmail.Body.String())
	}
	got := decodeError(t, wrongPassword)
	if got.Code != "invalid_credentials" {
		t.Errorf("error code = %q, want %q", got.Code, "invalid_credentials")
	}

	if token := sessionCookie(wrongPassword); token != "" {
		t.Errorf("a failed login set a session cookie (token %q), want none", token)
	}
}

// TestPostLoginNthFailureFromOneIPReturns429 is the DoD's third line, IP
// half: enough rapid failures from one address, even against different
// (all unknown) identifiers, trips the IP-keyed counter on its own.
func TestPostLoginNthFailureFromOneIPReturns429(t *testing.T) {
	r, _ := testRouterAndDB(t)

	const ip = "203.0.113.7"
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		rec := postLoginFrom(t, r, fmt.Sprintf("nobody-%d@example.org", i), "whatever", ip)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d from %s = %d, want %d (body: %s)", i+1, ip, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}

	rec := postLoginFrom(t, r, "nobody-overflow@example.org", "whatever", ip)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d from %s = %d, want %d (body: %s)", loginRateLimitMaxAttempts+1, ip, rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "too_many_requests" {
		t.Errorf("error code = %q, want %q", got.Code, "too_many_requests")
	}
}

// TestPostLoginNthFailureFromOneIdentifierAcrossIPsReturns429 is the DoD's
// third line, identifier half: the same target email, guessed wrong from a
// different address every time, still trips its own counter - an attacker
// spreading a brute force of one account across many source IPs gains
// nothing from the IP-keyed counter alone.
func TestPostLoginNthFailureFromOneIdentifierAcrossIPsReturns429(t *testing.T) {
	r, _ := testRouterAndDB(t)

	reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		rec := postLoginFrom(t, r, "treasurer@example.org", "not-the-password", ip)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d from %s = %d, want %d (body: %s)", i+1, ip, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}

	rec := postLoginFrom(t, r, "treasurer@example.org", "not-the-password", "198.51.100.250")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d (new IP) = %d, want %d (body: %s)", loginRateLimitMaxAttempts+1, rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
	got := decodeError(t, rec)
	if got.Code != "too_many_requests" {
		t.Errorf("error code = %q, want %q", got.Code, "too_many_requests")
	}

	// The correct password, from yet another fresh IP, must also be
	// refused while locked out - the lockout blocks the identifier
	// regardless of whether the next guess would actually have been right.
	rec = postLoginFrom(t, r, "treasurer@example.org", "correct-horse-battery", "198.51.100.251")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("correct password while locked out = %d, want %d (body: %s)", rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
}

// TestPostLoginSuccessResetsTheCounter is the DoD's fourth line: getting it
// right clears the slate, so a treasurer who fumbled her password a few
// times is not left a few attempts closer to a lockout afterward.
func TestPostLoginSuccessResetsTheCounter(t *testing.T) {
	r, _ := testRouterAndDB(t)

	reg := postRegister(t, r, "treasurer@example.org", "correct-horse-battery")
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", reg.Code, http.StatusCreated, reg.Body.String())
	}

	const ip = "203.0.113.9"
	for i := 0; i < loginRateLimitMaxAttempts-1; i++ {
		rec := postLoginFrom(t, r, "treasurer@example.org", "not-the-password", ip)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want %d (body: %s)", i+1, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}

	success := postLoginFrom(t, r, "treasurer@example.org", "correct-horse-battery", ip)
	if success.Code != http.StatusOK {
		t.Fatalf("POST /api/login(correct password) = %d, want %d (body: %s)", success.Code, http.StatusOK, success.Body.String())
	}

	// A fresh run of failures, same identifier and same IP, must reach the
	// same threshold again rather than starting already partway (or
	// already locked out) from before the success.
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		rec := postLoginFrom(t, r, "treasurer@example.org", "not-the-password", ip)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("post-reset failure %d = %d, want %d (body: %s) - the earlier success should have cleared the counter", i+1, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}
	rec := postLoginFrom(t, r, "treasurer@example.org", "not-the-password", ip)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("post-reset overflow attempt = %d, want %d (body: %s) - the limiter must still work after a reset", rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
}

// TestPostLoginRejectsAMalformedBody: decodeJSON's own refusal, before
// internal/auth or the rate limiter are ever reached.
func TestPostLoginRejectsAMalformedBody(t *testing.T) {
	r, _ := testRouterAndDB(t)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader([]byte("{not json"))))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/login(malformed body) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
