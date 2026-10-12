package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// #500: the login limiter must not be a lever against the treasurer, must not
// be walked around with more addresses or a different spelling of her email,
// and must grow its lockouts when the same key keeps filling. Every test here
// moves a fake clock; none sleeps.

const (
	limitsEmail    = "treasurer@example.org"
	limitsPassword = "correct-horse-battery"
)

// fakeClock is the one clock the router's limiters read (newWithClock).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// limitsRouter is a router with a registered treasurer, a fake clock, and
// password hashing replaced by a stub (the ceiling test makes dozens of
// attempts; the limiter, not argon2id, is under test). trusted is the
// trusted-proxy list, nil for none.
func limitsRouter(t *testing.T, trusted []netip.Prefix) (http.Handler, *fakeClock) {
	t.Helper()
	clk := newFakeClock()
	sqlDB := testStoreDB(t)
	au := auth.New(sqlDB, auth.WithPasswordFuncs(
		func(p string) (string, error) { return "stub:" + p, nil },
		func(p, encoded string) (bool, error) { return encoded == "stub:"+p, nil },
	))
	r := newWithClock(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), au, "", t.TempDir(), t.TempDir(), trusted, clk.now)
	if rec := postRegister(t, r, limitsEmail, limitsPassword); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	return r, clk
}

func wantLogin(t *testing.T, r http.Handler, email, password, ip string, want int) {
	t.Helper()
	if rec := postLoginFrom(t, r, email, password, ip); rec.Code != want {
		t.Fatalf("login(%q) from %s = %d, want %d (body: %s)", email, ip, rec.Code, want, rec.Body.String())
	}
}

func wrongGuesses(t *testing.T, r http.Handler, email, ip string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		wantLogin(t, r, email, "not-the-password", ip, http.StatusUnauthorized)
	}
}

// TestLoginFromACleanIPIsNotBlockedWhileTheIdentifierIsUnderAttack is #500's
// first line. Strangers who know the treasurer's email fill the per-address
// budgets of several addresses; she logs in from an address with no failures
// and is let in. The attackers' own addresses stay locked, so the attack did
// cost them something.
func TestLoginFromACleanIPIsNotBlockedWhileTheIdentifierIsUnderAttack(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)

	attackers := []string{"198.51.100.1", "198.51.100.2", "198.51.100.3", "198.51.100.4"}
	for _, ip := range attackers {
		wrongGuesses(t, r, limitsEmail, ip, loginRateLimitMaxAttempts)
		wantLogin(t, r, limitsEmail, "not-the-password", ip, http.StatusTooManyRequests)
	}

	wantLogin(t, r, limitsEmail, limitsPassword, "203.0.113.20", http.StatusOK)
	// And an attacker's own address is not unlocked by her getting in.
	wantLogin(t, r, limitsEmail, limitsPassword, attackers[0], http.StatusTooManyRequests)
}

// TestDistributedAttackOnOneIdentifierIsStillBounded: more addresses do not
// buy more guesses. After loginIdentifierCeiling attempts in the hour no new
// address gets through, the right password included; when the hour is over
// the account opens again.
func TestDistributedAttackOnOneIdentifierIsStillBounded(t *testing.T) {
	t.Parallel()
	r, clk := limitsRouter(t, nil)

	for i := 0; i < loginIdentifierCeiling; i++ {
		wantLogin(t, r, limitsEmail, "not-the-password", fmt.Sprintf("198.51.100.%d", i+1), http.StatusUnauthorized)
	}
	wantLogin(t, r, limitsEmail, "not-the-password", "203.0.113.1", http.StatusTooManyRequests)
	wantLogin(t, r, limitsEmail, limitsPassword, "203.0.113.2", http.StatusTooManyRequests)

	// Other identifiers are not affected by this one's ceiling.
	wantLogin(t, r, "someone-else@example.org", "x", "203.0.113.3", http.StatusUnauthorized)

	clk.advance(loginIdentifierCeilingWindow)
	wantLogin(t, r, limitsEmail, limitsPassword, "203.0.113.2", http.StatusOK)
}

// loginWith is postLoginFrom carrying cookies, and returning the recorder so a
// test can read what came back.
func loginWith(t *testing.T, r http.Handler, email, password, ip string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(loginRequest{Email: email, Password: password}) //nolint:gosec // request body
	if err != nil {
		t.Fatalf("marshaling login request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.RemoteAddr = net.JoinHostPort(ip, "1234")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// requestCookie is a cookie as a browser sends it back: name and value only.
func requestCookie(name, value string) *http.Cookie {
	//nolint:gosec // G124: attributes are a response-side concern; the request header carries none
	return &http.Cookie{Name: name, Value: value}
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func wantLoginWith(t *testing.T, r http.Handler, email, password, ip string, want int, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := loginWith(t, r, email, password, ip, cookies...)
	if rec.Code != want {
		t.Fatalf("login(%q) from %s = %d, want %d (body: %s)", email, ip, rec.Code, want, rec.Body.String())
	}
	return rec
}

// deviceFor logs the treasurer in once and returns the device cookie it set.
func deviceFor(t *testing.T, r http.Handler, email, ip string) *http.Cookie {
	t.Helper()
	c := cookieNamed(wantLoginWith(t, r, email, limitsPassword, ip, http.StatusOK), deviceCookieName)
	if c == nil {
		t.Fatal("a successful login set no device cookie")
	}
	return requestCookie(c.Name, c.Value)
}

// TestADeviceCookieGetsPastTheSharedKeys: with her network key and the
// account-wide ceiling both full - the two ways a stranger can close the
// account to her - a browser carrying her device cookie still logs in, and a
// browser without one does not.
func TestADeviceCookieGetsPastTheSharedKeys(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)
	const home = "203.0.113.20"
	device := deviceFor(t, r, limitsEmail, home)

	// The ceiling: the rest of its fifty (the login above used one) from
	// other addresses.
	for i := 0; i < loginIdentifierCeiling-1; i++ {
		wantLogin(t, r, limitsEmail, "not-the-password", fmt.Sprintf("198.51.100.%d", i+1), http.StatusUnauthorized)
	}
	// Her own network key: strangers on her network (or one shared with her).
	wrongGuesses(t, r, "someone-else@example.org", home, loginRateLimitMaxAttempts)
	wantLogin(t, r, "someone-else@example.org", "x", home, http.StatusTooManyRequests)

	wantLogin(t, r, limitsEmail, limitsPassword, home, http.StatusTooManyRequests)           // no cookie: shut
	wantLogin(t, r, limitsEmail, limitsPassword, "203.0.113.99", http.StatusTooManyRequests) // no cookie, elsewhere: shut
	wantLoginWith(t, r, limitsEmail, limitsPassword, home, http.StatusOK, device)            // her browser: in
	wantLoginWith(t, r, limitsEmail, limitsPassword, "203.0.113.77", http.StatusOK, device)  // and from another network
}

// TestWrongPasswordsWithADeviceCookieLockOnlyThatDevice: the cookie is no way
// round the cost of a guess. Ten wrong passwords lock the device key - the right
// one is then refused too - and nothing else.
func TestWrongPasswordsWithADeviceCookieLockOnlyThatDevice(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)
	const home = "203.0.113.20"
	device := deviceFor(t, r, limitsEmail, home)

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		wantLoginWith(t, r, limitsEmail, "not-the-password", home, http.StatusUnauthorized, device)
	}
	wantLoginWith(t, r, limitsEmail, limitsPassword, home, http.StatusTooManyRequests, device)

	// Not the shared keys: the same network without the cookie, and another
	// network, are as open as they were.
	wantLogin(t, r, limitsEmail, "not-the-password", home, http.StatusUnauthorized)
	wantLogin(t, r, limitsEmail, limitsPassword, "198.51.100.5", http.StatusOK)
}

// TestADeviceKeyEscalates: the device key climbs the same ladder.
func TestADeviceKeyEscalates(t *testing.T) {
	t.Parallel()
	r, clk := limitsRouter(t, nil)
	const home = "203.0.113.20"
	device := deviceFor(t, r, limitsEmail, home)

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		wantLoginWith(t, r, limitsEmail, "not-the-password", home, http.StatusUnauthorized, device)
	}
	clk.advance(5 * time.Minute)
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		wantLoginWith(t, r, limitsEmail, "not-the-password", home, http.StatusUnauthorized, device)
	}
	clk.advance(5 * time.Minute)
	wantLoginWith(t, r, limitsEmail, limitsPassword, home, http.StatusTooManyRequests, device) // the second lock is 15 minutes
	clk.advance(10 * time.Minute)
	wantLoginWith(t, r, limitsEmail, limitsPassword, home, http.StatusOK, device)
}

// TestADeviceCookieIsForItsOwnIdentifier: the cookie issued for the
// treasurer's email buys nothing for another one - that attempt is on the
// shared keys like any other.
func TestADeviceCookieIsForItsOwnIdentifier(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)
	const home = "203.0.113.20"
	device := deviceFor(t, r, limitsEmail, home)

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		wantLoginWith(t, r, "other@example.org", "x", home, http.StatusUnauthorized, device)
	}
	wantLoginWith(t, r, "other@example.org", "x", home, http.StatusTooManyRequests, device)
}

// TestAnUnknownExpiredOrForgedDeviceTokenIsNoDevice: each falls back to the
// three shared keys, so none of them can be used to dodge a lockout.
func TestAnUnknownExpiredOrForgedDeviceTokenIsNoDevice(t *testing.T) {
	t.Parallel()
	r, clk := limitsRouter(t, nil)
	const home = "203.0.113.20"
	device := deviceFor(t, r, limitsEmail, home)

	forged := requestCookie(deviceCookieName, "A"+device.Value[1:])
	garbage := requestCookie(deviceCookieName, "not-a-token")
	empty := requestCookie(deviceCookieName, "")

	wrongGuesses(t, r, limitsEmail, home, loginRateLimitMaxAttempts) // her network key and pair are now full
	for _, c := range []*http.Cookie{forged, garbage, empty} {
		wantLoginWith(t, r, limitsEmail, limitsPassword, home, http.StatusTooManyRequests, c)
	}

	clk.advance(deviceTokenTTL) // exactly expired
	wantLoginWith(t, r, limitsEmail, "not-the-password", "198.51.100.9", http.StatusUnauthorized, device)
	for i := 1; i < loginRateLimitMaxAttempts; i++ {
		wantLoginWith(t, r, limitsEmail, "not-the-password", "198.51.100.9", http.StatusUnauthorized, device)
	}
	// Ten on the shared pair key, not the device key: the eleventh is refused
	// and so is the right password with the (expired) cookie.
	wantLoginWith(t, r, limitsEmail, limitsPassword, "198.51.100.9", http.StatusTooManyRequests, device)
	// The same pair key refuses a request with no cookie at all: it was the
	// shared path that counted, not a device key.
	wantLogin(t, r, limitsEmail, limitsPassword, "198.51.100.9", http.StatusTooManyRequests)
}

// TestLoginSetsADeviceCookieWithTheSessionCookiesAttributes, and a later
// login with it refreshes it rather than minting another.
func TestLoginSetsADeviceCookieWithTheSessionCookiesAttributes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		baseURL string
		secure  bool
	}{
		{"https base URL", "https://uruni.example.org", true},
		{"plain http dev", "http://localhost:5173", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clk := newFakeClock()
			sqlDB := testStoreDB(t)
			au := auth.New(sqlDB, auth.WithPasswordFuncs(
				func(p string) (string, error) { return "stub:" + p, nil },
				func(p, encoded string) (bool, error) { return encoded == "stub:"+p, nil },
			))
			r := newWithClock(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), au, tc.baseURL, t.TempDir(), t.TempDir(), nil, clk.now)
			if rec := postRegister(t, r, limitsEmail, limitsPassword); rec.Code != http.StatusCreated {
				t.Fatalf("POST /api/register = %d", rec.Code)
			}

			rec := wantLoginWith(t, r, limitsEmail, limitsPassword, "203.0.113.20", http.StatusOK)
			session, device := cookieNamed(rec, "session"), cookieNamed(rec, deviceCookieName)
			if session == nil || device == nil {
				t.Fatalf("cookies = session %v, device %v, want both", session, device)
			}
			if device.HttpOnly != session.HttpOnly || !device.HttpOnly {
				t.Errorf("HttpOnly = %v, session's %v, want true for both", device.HttpOnly, session.HttpOnly)
			}
			if device.Secure != session.Secure || device.Secure != tc.secure {
				t.Errorf("Secure = %v, session's %v, want %v", device.Secure, session.Secure, tc.secure)
			}
			if device.SameSite != session.SameSite || device.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, session's %v, want Lax", device.SameSite, session.SameSite)
			}
			if device.Path != session.Path || device.Path != "/" {
				t.Errorf("Path = %q, session's %q, want /", device.Path, session.Path)
			}
			if device.MaxAge != int((30 * 24 * time.Hour).Seconds()) {
				t.Errorf("Max-Age = %d, want 30 days", device.MaxAge)
			}
			if len(device.Value) < 43 { // 32 random bytes, base64url without padding
				t.Errorf("token %q is shorter than 32 bytes of entropy", device.Value)
			}

			// Logged in again with the cookie: the same token is refreshed,
			// not replaced, and its lifetime restarts.
			clk.advance(10 * 24 * time.Hour)
			again := wantLoginWith(t, r, limitsEmail, limitsPassword, "203.0.113.20", http.StatusOK, requestCookie(device.Name, device.Value))
			refreshed := cookieNamed(again, deviceCookieName)
			if refreshed == nil || refreshed.Value != device.Value {
				t.Fatalf("second login set device cookie %v, want the same token refreshed", refreshed)
			}
			if !refreshed.Expires.Equal(clk.now().Add(deviceTokenTTL).Truncate(time.Second)) {
				t.Errorf("refreshed Expires = %v, want 30 days from the second login", refreshed.Expires)
			}
			clk.advance(25 * 24 * time.Hour) // 35 days after the first login, 25 after the refresh
			wantLoginWith(t, r, limitsEmail, limitsPassword, "203.0.113.20", http.StatusOK, requestCookie(device.Name, device.Value))
		})
	}
}

// TestAFailedLoginSetsNoDeviceCookie: the cookie is only ever the reward for a
// correct password.
func TestAFailedLoginSetsNoDeviceCookie(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)
	rec := wantLoginWith(t, r, limitsEmail, "not-the-password", "203.0.113.20", http.StatusUnauthorized)
	if c := cookieNamed(rec, deviceCookieName); c != nil {
		t.Errorf("a failed login set the device cookie %v", c)
	}
}

// TestLogoutLeavesTheDeviceCookie: it is not a session. Logout answers 204
// without touching it, and it still works afterward.
func TestLogoutLeavesTheDeviceCookie(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)
	const home = "203.0.113.20"
	device := deviceFor(t, r, limitsEmail, home)

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(device)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /api/logout = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if c := cookieNamed(rec, deviceCookieName); c != nil {
		t.Errorf("logout touched the device cookie: %v", c)
	}

	wrongGuesses(t, r, "someone-else@example.org", home, loginRateLimitMaxAttempts) // network shut
	wantLoginWith(t, r, limitsEmail, limitsPassword, home, http.StatusOK, device)
}

// TestRestoreConfirmIgnoresTheDeviceCookie: restore confirm stays on its
// network key; a device cookie neither opens nor shortcuts it.
func TestRestoreConfirmIgnoresTheDeviceCookie(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	token := decodeInspectToken(t, postRestoreInspect(t, r, downloadRealBackupZip(t, r)))

	with := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.AddCookie(requestCookie(deviceCookieName, "anything"))
		r.ServeHTTP(w, req)
	})
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		if rec := postRestoreConfirm(t, with, token, "wrong-guess"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}
	assertAPIError(t, postRestoreConfirm(t, with, token, fixturePassword), http.StatusTooManyRequests, "too_many_requests")
}

// TestLoginLockoutsEscalateAndDecay drives escalation through the handler:
// 5 minutes for the first fill of one pair, 15 for the second, an hour for the
// third and every later one, and a quiet day afterward starts it over.
func TestLoginLockoutsEscalateAndDecay(t *testing.T) {
	t.Parallel()
	r, clk := limitsRouter(t, nil)
	const ip = "198.51.100.7"

	fill := func() { wrongGuesses(t, r, limitsEmail, ip, loginRateLimitMaxAttempts) }
	locked := func() {
		t.Helper()
		wantLogin(t, r, limitsEmail, "not-the-password", ip, http.StatusTooManyRequests)
	}

	fill() // strike 1: 5 minutes
	clk.advance(5*time.Minute - time.Second)
	locked() // still inside 5m
	clk.advance(time.Second)

	fill() // strike 2: 15 minutes
	clk.advance(5 * time.Minute)
	locked() // 5m is no longer enough
	clk.advance(10*time.Minute - time.Second)
	locked() // still inside 15m
	clk.advance(time.Second)

	fill() // strike 3: an hour
	clk.advance(15 * time.Minute)
	locked() // 15m is no longer enough
	clk.advance(45*time.Minute - time.Second)
	locked() // still inside 1h
	clk.advance(time.Second)

	fill() // strike 4: still an hour, the ladder does not run out
	clk.advance(time.Hour - time.Second)
	locked() // the last step repeats
	clk.advance(time.Second)

	// Quiet for lockoutDecay after the last lockout ends: back to 5 minutes.
	clk.advance(lockoutDecay)
	fill()
	clk.advance(5 * time.Minute)
	wantLogin(t, r, limitsEmail, "not-the-password", ip, http.StatusUnauthorized)
}

// TestASuccessfulLoginForgetsTheEscalation: the strikes a pair has earned go
// with it when the right password comes in.
func TestASuccessfulLoginForgetsTheEscalation(t *testing.T) {
	t.Parallel()
	r, clk := limitsRouter(t, nil)
	const ip = "198.51.100.7"

	wrongGuesses(t, r, limitsEmail, ip, loginRateLimitMaxAttempts) // strike 1
	clk.advance(5 * time.Minute)
	wantLogin(t, r, limitsEmail, limitsPassword, ip, http.StatusOK) // success clears pair and network

	wrongGuesses(t, r, limitsEmail, ip, loginRateLimitMaxAttempts) // would be strike 2 (15m) without the success
	clk.advance(5 * time.Minute)
	wantLogin(t, r, limitsEmail, limitsPassword, ip, http.StatusOK)
}

// TestIPv6AddressesInOneSlash64ShareAKey: an attacker holding a /64 does not
// get a fresh budget per address; another /64 is a different client.
func TestIPv6AddressesInOneSlash64ShareAKey(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)

	// Ten attempts, each from a different address in 2001:db8:0:1::/64 and
	// each at a different identifier, so only the network key can be what fills.
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		wantLogin(t, r, fmt.Sprintf("nobody-%d@example.org", i), "x", fmt.Sprintf("2001:db8:0:1:%x::1", i+1), http.StatusUnauthorized)
	}
	wantLogin(t, r, "nobody-z@example.org", "x", "2001:db8:0:1:ffff:ffff:ffff:ffff", http.StatusTooManyRequests)
	// The neighbouring /64 and an unrelated IPv6 network are untouched.
	wantLogin(t, r, "nobody-z@example.org", "x", "2001:db8:0:2::1", http.StatusUnauthorized)
	wantLogin(t, r, "nobody-z@example.org", "x", "2001:db9:0:1::1", http.StatusUnauthorized)
}

// TestIPv4MappedIPv6IsTheIPv4Address: a dual-stack listener reports an IPv4
// client as ::ffff:a.b.c.d; it must count with, not beside, plain a.b.c.d.
func TestIPv4MappedIPv6IsTheIPv4Address(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		ip := "203.0.113.7"
		if i%2 == 1 {
			ip = "::ffff:203.0.113.7"
		}
		wantLogin(t, r, fmt.Sprintf("nobody-%d@example.org", i), "x", ip, http.StatusUnauthorized)
	}
	wantLogin(t, r, "nobody-z@example.org", "x", "::ffff:203.0.113.7", http.StatusTooManyRequests)
	wantLogin(t, r, "nobody-z@example.org", "x", "203.0.113.7", http.StatusTooManyRequests)
	// A different IPv4 address is a different client (IPv4 is keyed whole).
	wantLogin(t, r, "nobody-z@example.org", "x", "203.0.113.8", http.StatusUnauthorized)
}

// TestForwardedIPv6ClientIsKeyedByItsSlash64: behind a trusted proxy the
// /64 reduction applies to the forwarded client, not the proxy.
func TestForwardedIPv6ClientIsKeyedByItsSlash64(t *testing.T) {
	t.Parallel()
	trusted := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	r, _ := limitsRouter(t, trusted)

	post := func(i int, forwarded string) int {
		body, err := json.Marshal(loginRequest{Email: fmt.Sprintf("nobody-%d@example.org", i), Password: "x"}) //nolint:gosec // request body
		if err != nil {
			t.Fatalf("marshaling login request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("X-Forwarded-For", forwarded)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		if got := post(i, fmt.Sprintf("2001:db8:0:1:%x::9", i+1)); got != http.StatusUnauthorized {
			t.Fatalf("forwarded attempt %d = %d, want %d", i+1, got, http.StatusUnauthorized)
		}
	}
	if got := post(99, "2001:db8:0:1:aaaa::1"); got != http.StatusTooManyRequests {
		t.Errorf("another address in the same /64 behind the proxy = %d, want %d", got, http.StatusTooManyRequests)
	}
	if got := post(98, "2001:db8:0:2::1"); got != http.StatusUnauthorized {
		t.Errorf("an address in another /64 behind the proxy = %d, want %d", got, http.StatusUnauthorized)
	}
}

// TestNetworkOf is the reduction itself, edge cases included.
func TestNetworkOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"203.0.113.7", "203.0.113.7"},
		{"::ffff:203.0.113.7", "203.0.113.7"},
		{"2001:db8:0:1:aaaa:bbbb:cccc:dddd", "2001:db8:0:1::/64"},
		{"2001:db8:0:1::1", "2001:db8:0:1::/64"},
		{"2001:db8:0:2::1", "2001:db8:0:2::/64"},
		{"fe80::1%eth0", "fe80::/64"},
		{"::1", "::/64"},
		{"invalid IP", "invalid IP"},
		{"not-an-address", "not-an-address"},
	} {
		if got := networkOf(tc.in); got != tc.want {
			t.Errorf("networkOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLoginEmailIsTrimmedAndCaseInsensitive: a capitalised or padded email is
// the same account (no spurious "wrong password") and the same limiter key (no
// fresh budget for each spelling).
func TestLoginEmailIsTrimmedAndCaseInsensitive(t *testing.T) {
	t.Parallel()
	r, _ := limitsRouter(t, nil)

	wantLogin(t, r, "  Treasurer@Example.ORG\t", limitsPassword, "198.51.100.7", http.StatusOK)

	// Ten wrong guesses spelled ten ways fill one pair, not ten.
	spellings := []string{
		"treasurer@example.org", "Treasurer@example.org", "TREASURER@EXAMPLE.ORG", " treasurer@example.org",
		"treasurer@example.org ", "\tTreasurer@Example.org\n", "tREASURER@example.org", "treasurer@EXAMPLE.org",
		"TREASURER@example.org", "treasurer@Example.Org",
	}
	for _, s := range spellings {
		wantLogin(t, r, s, "not-the-password", "198.51.100.8", http.StatusUnauthorized)
	}
	wantLogin(t, r, "treasurer@example.org", "not-the-password", "198.51.100.8", http.StatusTooManyRequests)
}

// TestRegisterStoresAndReturnsTheNormalisedEmail: the wire shape shows what is
// stored.
func TestRegisterStoresAndReturnsTheNormalisedEmail(t *testing.T) {
	t.Parallel()
	r, sqlDB := testRouterAndDB(t)

	rec := postRegister(t, r, "  Treasurer@Example.ORG ", "correct-horse-battery")
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got userResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding register response: %v", err)
	}
	if got.Email != "treasurer@example.org" {
		t.Errorf("register response email = %q, want the normalised form", got.Email)
	}
	var stored string
	if err := sqlDB.QueryRow(`SELECT email FROM "user"`).Scan(&stored); err != nil || stored != "treasurer@example.org" {
		t.Errorf("stored email = %q, %v, want treasurer@example.org", stored, err)
	}
}

// TestRegisterIPv6Slash64SharesALimit: register's own limiter is keyed by the
// same network reduction. Invalid attempts count, so ten refused ones from
// one /64 close it to the eleventh from a third address in it.
func TestRegisterIPv6Slash64SharesALimit(t *testing.T) {
	t.Parallel()
	r, _ := testRouterAndDB(t)

	post := func(ip string) int {
		body, err := json.Marshal(registerRequest{Email: "not-an-email", Password: "x"}) //nolint:gosec // request body
		if err != nil {
			t.Fatalf("marshaling register request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewReader(body))
		req.RemoteAddr = net.JoinHostPort(ip, "1234")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < registerRateLimitMaxAttempts; i++ {
		if got := post(fmt.Sprintf("2001:db8:0:1:%x::1", i+1)); got != http.StatusBadRequest {
			t.Fatalf("register attempt %d = %d, want %d", i+1, got, http.StatusBadRequest)
		}
	}
	if got := post("2001:db8:0:1:ffff::1"); got != http.StatusTooManyRequests {
		t.Errorf("register from another address in the /64 = %d, want %d", got, http.StatusTooManyRequests)
	}
	if got := post("2001:db8:0:2::1"); got != http.StatusBadRequest {
		t.Errorf("register from another /64 = %d, want %d", got, http.StatusBadRequest)
	}
}

// TestRestoreConfirmIPv6Slash64SharesALimit: restore confirm counts under the
// same reduction.
func TestRestoreConfirmIPv6Slash64SharesALimit(t *testing.T) {
	t.Parallel()
	r := testRouter(t)
	setUpFund(t, r)
	token := decodeInspectToken(t, postRestoreInspect(t, r, downloadRealBackupZip(t, r)))

	from := func(ip string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req.RemoteAddr = net.JoinHostPort(ip, "1234")
			r.ServeHTTP(w, req)
		})
	}
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		if rec := postRestoreConfirm(t, from(fmt.Sprintf("2001:db8:0:1:%x::1", i+1)), token, "wrong-guess"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}
	assertAPIError(t, postRestoreConfirm(t, from("2001:db8:0:1:ffff::1"), token, "wrong-guess"), http.StatusTooManyRequests, "too_many_requests")
	if rec := postRestoreConfirm(t, from("2001:db8:0:2::1"), token, "wrong-guess"); rec.Code != http.StatusUnauthorized {
		t.Errorf("restore confirm from another /64 = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
