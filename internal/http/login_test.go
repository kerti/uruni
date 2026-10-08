package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

func postLogin(t *testing.T, r http.Handler, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	return postLoginFrom(t, r, email, password, "")
}

// postLoginFrom is postLogin's twin for the rate-limit tests, which need
// each request to carry its own source IP - as the TCP peer, since the test
// router trusts no proxy and so reads no X-Forwarded-For (clientIP, #447).
// ip == "" keeps httptest.NewRequest's fixed RemoteAddr, so postLogin above
// is exactly this with nothing changed.
func postLoginFrom(t *testing.T, r http.Handler, email, password, ip string) *httptest.ResponseRecorder {
	t.Helper()
	//nolint:gosec // not a credential leak - this is the request body POST /api/login's own contract requires
	body, err := json.Marshal(loginRequest{Email: email, Password: password})
	if err != nil {
		t.Fatalf("marshaling login request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	if ip != "" {
		req.RemoteAddr = net.JoinHostPort(ip, "1234")
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	r, _ := testRouterAndDB(t)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader([]byte("{not json"))))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/login(malformed body) = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestPostLoginIgnoresAForgedForwardedForFromAnUntrustedPeer is #447's
// regression: a caller who reaches the app directly and names a fresh
// X-Forwarded-For on every guess is still counted under its own address.
func TestPostLoginIgnoresAForgedForwardedForFromAnUntrustedPeer(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	post := func(i int) *httptest.ResponseRecorder {
		//nolint:gosec // not a credential leak - this is the request body POST /api/login's own contract requires
		body, err := json.Marshal(loginRequest{Email: fmt.Sprintf("nobody-%d@example.org", i), Password: "whatever"})
		if err != nil {
			t.Fatalf("marshaling login request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		if rec := post(i); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want %d (body: %s)", i+1, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}
	if rec := post(loginRateLimitMaxAttempts); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d under a fresh forged address = %d, want %d", loginRateLimitMaxAttempts+1, rec.Code, http.StatusTooManyRequests)
	}
}

func TestClientIP(t *testing.T) {
	t.Parallel()
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}

	for _, tc := range []struct {
		name    string
		trusted []netip.Prefix
		remote  string
		xff     []string
		want    string
	}{
		{"no proxy, no header: the peer", nil, "203.0.113.7:4321", nil, "203.0.113.7"},
		{"no trusted proxies: the header is ignored", nil, "203.0.113.7:4321", []string{"198.51.100.1"}, "203.0.113.7"},
		{"an untrusted peer's header is ignored", proxies, "203.0.113.7:4321", []string{"198.51.100.1"}, "203.0.113.7"},
		{"a trusted peer: the hop it saw", proxies, "10.0.0.2:4321", []string{"203.0.113.7"}, "203.0.113.7"},
		{"a trusted peer, no header: the peer", proxies, "10.0.0.2:4321", nil, "10.0.0.2"},
		{"entries a client wrote, left of the first untrusted hop, are never read", proxies, "10.0.0.2:4321", []string{"198.51.100.1, 203.0.113.7"}, "203.0.113.7"},
		{"a chain of trusted proxies is walked past", proxies, "10.0.0.2:4321", []string{"203.0.113.7, 10.9.9.9"}, "203.0.113.7"},
		{"repeated headers read as one list", proxies, "10.0.0.2:4321", []string{"198.51.100.1", "203.0.113.7"}, "203.0.113.7"},
		{"a hop that is not an address stops at the last trusted one", proxies, "10.0.0.2:4321", []string{"203.0.113.7, junk"}, "10.0.0.2"},
		{"every hop trusted: the left-most", proxies, "10.0.0.2:4321", []string{"10.3.3.3, 10.9.9.9"}, "10.3.3.3"},
		{"IPv6 peer", proxies, "[fd00::2]:4321", []string{"2001:db8::7"}, "2001:db8::7"},
		{"an IPv4-mapped peer is its IPv4 address", proxies, "[::ffff:10.0.0.2]:4321", []string{"203.0.113.7"}, "203.0.113.7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
			req.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := (&api{trustedProxies: tc.trusted}).clientIP(req); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPostLoginRefusesAnOversizedBody is #447's: login is reachable by
// anyone, so its body is capped before it is decoded (decodeJSON).
func TestPostLoginRefusesAnOversizedBody(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	body := `{"email":"` + strings.Repeat("a", maxJSONBodyBytes) + `@example.org","password":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST /api/login with a %d-byte body = %d, want %d", len(body), rec.Code, http.StatusRequestEntityTooLarge)
	}
	if got := decodeError(t, rec); got.Code != "request_too_large" {
		t.Errorf("error code = %q, want %q", got.Code, "request_too_large")
	}
}

// TestPostLoginBehindATrustedProxyCountsEachForwardedClientApart pins the
// wiring clientIP's own table cannot see: a router built with a trusted
// proxy (httptest's fixed peer, 192.0.2.1) reads X-Forwarded-For, so one
// client tripping the IP counter leaves the next client behind the same
// proxy untouched. Drop trustedProxies anywhere between New and api and
// every forwarded client shares the proxy's counter - and this fails.
func TestPostLoginBehindATrustedProxyCountsEachForwardedClientApart(t *testing.T) {
	t.Parallel()
	sqlDB := testStoreDB(t)
	trusted := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	r := New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB), "", t.TempDir(), t.TempDir(), trusted)

	post := func(email, forwardedFor string) *httptest.ResponseRecorder {
		//nolint:gosec // not a credential leak - this is the request body POST /api/login's own contract requires
		body, err := json.Marshal(loginRequest{Email: email, Password: "whatever"})
		if err != nil {
			t.Fatalf("marshaling login request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", forwardedFor)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	const first, second = "203.0.113.7", "198.51.100.9"
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		if rec := post(fmt.Sprintf("nobody-%d@example.org", i), first); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d from %s = %d, want %d (body: %s)", i+1, first, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}
	if rec := post("nobody-overflow@example.org", first); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d from %s = %d, want %d", loginRateLimitMaxAttempts+1, first, rec.Code, http.StatusTooManyRequests)
	}
	if rec := post("someone-else@example.org", second); rec.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt from %s behind the same proxy = %d, want %d (body: %s)", second, rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

// TestPartialUpdateDecodersRefuseAnOversizedBody covers the three PATCH
// decoders #447 moved off their own json.NewDecoder onto decodeJSON. Each
// handler resolves its record before decoding, so the decoders are driven
// directly: the cap is theirs to honour, not the route's.
func TestPartialUpdateDecodersRefuseAnOversizedBody(t *testing.T) {
	t.Parallel()
	body := `{"name":"` + strings.Repeat("a", maxJSONBodyBytes) + `"}`

	for _, tc := range []struct {
		name   string
		decode func(http.ResponseWriter, *http.Request) bool
	}{
		{"account", func(w http.ResponseWriter, r *http.Request) bool {
			_, ok := decodeUpdateAccountRequest(w, r)
			return ok
		}},
		{"member", func(w http.ResponseWriter, r *http.Request) bool { _, ok := decodeUpdateMemberRequest(w, r); return ok }},
		{"reimbursement", func(w http.ResponseWriter, r *http.Request) bool {
			_, ok := decodeUpdateReimbursementRequest(w, r)
			return ok
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPatch, "/api/x/1", strings.NewReader(body))
			rec := httptest.NewRecorder()
			if tc.decode(rec, req) {
				t.Fatalf("decoder accepted a %d-byte body", len(body))
			}
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
			}
			if got := decodeError(t, rec); got.Code != "request_too_large" {
				t.Errorf("error code = %q, want %q", got.Code, "request_too_large")
			}
		})
	}
}
