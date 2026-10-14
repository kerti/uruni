package http

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/store"
)

// #386: a session is bound to the login it was issued for. Deleting session rows
// is not enough on its own: with IdleTimeout set every authenticated request
// writes its session back, so a request in flight across a change puts the
// deleted row back (session_store.go, credentialFingerprint).

// signalBody is a request body that says when the handler first reads it and
// then blocks on the pipe: a request held mid-flight, past the session gate.
type signalBody struct {
	r       io.Reader
	started chan struct{}
	once    sync.Once
}

func (b *signalBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	return b.r.Read(p)
}

func sessionData(t *testing.T, e *userEnv, token string) (time.Time, map[string]any) {
	t.Helper()
	sess, err := store.New(e.db).GetSession(context.Background(), store.GetSessionParams{Token: token, ExpiresAt: time.Now().Unix()})
	if err != nil {
		t.Fatalf("GetSession(%q) = %v, want a row", token, err)
	}
	deadline, values, err := scs.GobCodec{}.Decode(sess.Data)
	if err != nil {
		t.Fatalf("decoding the session: %v", err)
	}
	return deadline, values
}

func sessionRowExists(e *userEnv, token string) bool {
	_, err := store.New(e.db).GetSession(context.Background(), store.GetSessionParams{Token: token, ExpiresAt: time.Now().Unix()})
	return err == nil
}

func TestRegisterAndLoginSessionsCarryTheCredentialFingerprint(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)
	ctx := context.Background()
	user, err := store.New(e.db).GetUserByEmail(ctx, store.GetUserByEmailParams{Normalized: userEmail, Exact: userEmail})
	if err != nil {
		t.Fatalf("GetUserByEmail() = %v", err)
	}

	for name, token := range map[string]string{"register": e.a, "login": e.b} {
		if !e.alive(t, token) {
			t.Errorf("the %s session does not open a gated route", name)
		}
		if _, values := sessionData(t, e, token); values[sessionKeyCredential] != credentialFingerprint(user) {
			t.Errorf("the %s session carries credential %v, want the user's fingerprint", name, values[sessionKeyCredential])
		}
	}
}

// The fingerprint moves with the password (the salt does) and with the email.
func TestTheFingerprintChangesWithThePasswordAndTheEmail(t *testing.T) {
	t.Parallel()
	u := store.User{Email: "a@example.org", PasswordHash: "hash-1"}
	same := credentialFingerprint(u)
	if got := credentialFingerprint(store.User{Email: " A@Example.org ", PasswordHash: "hash-1"}); got != same {
		t.Error("the fingerprint depends on how the email is spelled")
	}
	if credentialFingerprint(store.User{Email: "a@example.org", PasswordHash: "hash-2"}) == same {
		t.Error("a new password hash left the fingerprint unchanged")
	}
	if credentialFingerprint(store.User{Email: "b@example.org", PasswordHash: "hash-1"}) == same {
		t.Error("a new email left the fingerprint unchanged")
	}
	// The separator: moving a character across it is a different pair.
	if credentialFingerprint(store.User{Email: "b@example.org", PasswordHash: "hash-1"}) == credentialFingerprint(store.User{Email: "@example.org", PasswordHash: "hash-1b"}) {
		t.Error("the hash and the email run together")
	}
}

// A session from before the fingerprint existed has none, and is refused, not
// stamped: the treasurer signs in once more after the upgrade.
func TestASessionWithoutAFingerprintIsRefusedAndDestroyed(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	deadline, values := sessionData(t, e, e.b)
	delete(values, sessionKeyCredential)
	data, err := scs.GobCodec{}.Encode(deadline, values)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if _, err := store.New(e.db).UpsertSession(context.Background(), store.UpsertSessionParams{Token: e.b, Data: data, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("UpsertSession() = %v", err)
	}

	e.wantStatus(t, e.call(t, http.MethodGet, "/api/user", e.b, nil), http.StatusUnauthorized, "unauthenticated")
	if sessionRowExists(e, e.b) {
		t.Error("the refused session's row is still there")
	}
	// And the unstamped one was not stamped on the way: a second try is the same.
	e.wantStatus(t, e.call(t, http.MethodGet, "/api/user", e.b, nil), http.StatusUnauthorized, "unauthenticated")
	if !e.alive(t, e.a) {
		t.Error("refusing one session signed out another")
	}
}

func TestSessionStatusReadsARevokedSessionAsLoggedOut(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)
	e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", userPassword), http.StatusNoContent, "")
	// Put b's row back as a request in flight would: its data is the old one.
	resurrect(t, e, e.b, nil)

	rec := e.call(t, http.MethodGet, "/api/session", e.b, nil)
	e.wantStatus(t, rec, http.StatusOK, "")
	if !strings.Contains(rec.Body.String(), `"authenticated":false`) {
		t.Errorf("GET /api/session for a revoked session = %s, want authenticated:false", rec.Body.String())
	}
}

// resurrect writes a session row back as a stale request's commit would. With
// values nil the row is the old session: the user and a fingerprint from before
// the change.
func resurrect(t *testing.T, e *userEnv, token string, values map[string]any) {
	t.Helper()
	if values == nil {
		//nolint:gosec // G101: a stand-in fingerprint, not a credential
		values = map[string]any{sessionKeyUserID: int64(1), sessionKeyCredential: "the-credential-before-the-change"}
	}
	data, err := scs.GobCodec{}.Encode(time.Now().Add(time.Hour), values)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if _, err := store.New(e.db).UpsertSession(context.Background(), store.UpsertSessionParams{Token: token, Data: data, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatalf("UpsertSession() = %v", err)
	}
}

// The race: a request holds its session open, the password is changed from
// another device, the held request finishes and its session commit puts the row
// back. The row is back, and the old cookie still gets 401.
func TestARequestInFlightCannotResurrectASessionAcrossAPasswordChange(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	body := &signalBody{r: pr, started: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPost, "/api/members", body)
	req.RemoteAddr = net.JoinHostPort("198.51.100.99", "1234")
	req.AddCookie(requestCookie("session", e.b))
	held := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.r.ServeHTTP(held, req)
	}()

	// Blocked reading the body: the session is loaded and the gate has passed.
	select {
	case <-body.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the held request never reached its handler")
	}

	// Meanwhile the treasurer changes the password from her own device. b's row
	// is deleted with it.
	oldB := e.b
	e.wantStatus(t, e.changePassword(t, e.a, "a-brand-new-secret", userPassword), http.StatusNoContent, "")
	if sessionRowExists(e, oldB) {
		t.Fatal("the change left the other session's row in place")
	}

	// The held request finishes; scs writes its (old) session back.
	if _, err := pw.Write([]byte("{}")); err != nil {
		t.Fatalf("releasing the held request: %v", err)
	}
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the held request never finished")
	}
	if !sessionRowExists(e, oldB) {
		t.Fatal("the row was not written back, so this test no longer exercises the race it exists for")
	}

	e.wantStatus(t, e.call(t, http.MethodGet, "/api/user", oldB, nil), http.StatusUnauthorized, "unauthenticated")
	if sessionRowExists(e, oldB) {
		t.Error("the refused session's row was left in place")
	}
	if !e.alive(t, e.a) {
		t.Error("the session that made the change was signed out")
	}
}

// The same for the session making the change: a request in flight on its OLD
// token writes that row back after the renewal.
func TestAStaleWriteBackOfTheRenewedSessionsOldTokenIsRefused(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)
	old := e.a

	e.wantStatus(t, e.changeEmail(t, e.a, "new.address@example.org", userPassword), http.StatusOK, "")
	_, values := sessionData(t, e, e.a)
	values[sessionKeyCredential] = "the-credential-before-the-change"
	resurrect(t, e, old, values)

	e.wantStatus(t, e.call(t, http.MethodGet, "/api/user", old, nil), http.StatusUnauthorized, "unauthenticated")
	if !e.alive(t, e.a) {
		t.Error("the renewed session was signed out")
	}
}

func TestACeilingBoundsWrongCurrentPasswordsFromRotatingAddresses(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	for i := 0; i < loginIdentifierCeiling; i++ {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		e.wantStatus(t, e.changePasswordFrom(t, ip, e.a, "a-brand-new-secret", "not-the-password"), http.StatusForbidden, "wrong_current_password")
	}
	e.wantStatus(t, e.changePasswordFrom(t, "203.0.113.200", e.a, "a-brand-new-secret", userPassword), http.StatusTooManyRequests, "too_many_requests")
	if e.loginStatus(t, userEmail, userPassword) != http.StatusOK {
		t.Error("the password was changed, or login was locked, by the ceiling")
	}
}

// A success clears the network key and leaves the ceiling, as login does.
func TestASuccessDoesNotClearTheCeiling(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)

	for i := 0; i < loginIdentifierCeiling-1; i++ {
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		e.wantStatus(t, e.changePasswordFrom(t, ip, e.a, "a-brand-new-secret", "not-the-password"), http.StatusForbidden, "")
	}
	e.wantStatus(t, e.changePasswordFrom(t, "203.0.113.201", e.a, "a-brand-new-secret", userPassword), http.StatusNoContent, "")
	e.wantStatus(t, e.changePasswordFrom(t, "203.0.113.202", e.a, "another-new-secret", "a-brand-new-secret"), http.StatusTooManyRequests, "too_many_requests")
}

// A busy hasher is not a guess: any number of 503s leave the budget whole.
func TestServerBusyOnAChangeReleasesTheReservation(t *testing.T) {
	t.Parallel()
	parked := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	e := newUserEnv(t,
		auth.WithMaxConcurrentHashes(1),
		auth.WithSlotWait(20*time.Millisecond),
		auth.WithPasswordFuncs(
			func(p string) (string, error) { return "stub:" + p, nil },
			func(p, encoded string) (bool, error) {
				if p == "park" {
					once.Do(func() { close(parked) })
					<-release
				}
				return encoded == "stub:"+p, nil
			},
		),
	)

	// A login holds the only slot.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		loginWith(t, e.r, userEmail, "park", "192.0.2.77")
	}()
	<-parked

	for i := 0; i < loginRateLimitMaxAttempts+1; i++ {
		rec := e.changeEmail(t, e.a, "new@example.org", userPassword)
		e.wantStatus(t, rec, http.StatusServiceUnavailable, "server_busy")
	}
	close(release)
	wg.Wait()

	e.wantStatus(t, e.changeEmail(t, e.a, "new@example.org", "not-the-password"), http.StatusForbidden, "wrong_current_password")
	e.wantStatus(t, e.changeEmail(t, e.a, "new@example.org", userPassword), http.StatusOK, "")
}

// A broken database is not a guess either.
func TestADatabaseErrorOnAChangeReleasesTheReservation(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)
	if _, err := e.db.Exec(`CREATE TRIGGER refuse_user_update BEFORE UPDATE ON "user" BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatalf("creating the trigger: %v", err)
	}

	for i := 0; i < loginRateLimitMaxAttempts+1; i++ {
		e.wantStatus(t, e.changeEmail(t, e.a, "new@example.org", userPassword), http.StatusInternalServerError, "internal_error")
	}
	// Nothing was changed, and no other session was signed out by a rolled-back change.
	if !e.alive(t, e.b) {
		t.Error("a failed change signed another session out")
	}

	if _, err := e.db.Exec(`DROP TRIGGER refuse_user_update`); err != nil {
		t.Fatalf("dropping the trigger: %v", err)
	}
	e.wantStatus(t, e.changeEmail(t, e.a, "new@example.org", "not-the-password"), http.StatusForbidden, "wrong_current_password")
	e.wantStatus(t, e.changeEmail(t, e.a, "new@example.org", userPassword), http.StatusOK, "")
}

// The device cookie login issued is keyed to the old email's digest. After an
// email change it names nothing: it gives the new email no pass past a full
// shared key, and the old email logs in with nothing.
func TestAnEmailChangeLeavesTheOldDeviceCookieHarmless(t *testing.T) {
	t.Parallel()
	e := newUserEnv(t)
	c := cookieNamed(loginWith(t, e.r, userEmail, userPassword, "192.0.2.50"), deviceCookieName)
	if c == nil {
		t.Fatal("login set no device cookie")
	}
	device := requestCookie(c.Name, c.Value)

	const newEmail = "new.address@example.org"
	e.wantStatus(t, e.changeEmail(t, e.a, newEmail, userPassword), http.StatusOK, "")

	// It still signs in, as a browser with no cookie does.
	if got := loginWith(t, e.r, newEmail, userPassword, "192.0.2.51", device).Code; got != http.StatusOK {
		t.Errorf("login with the new email and the old device cookie = %d, want %d", got, http.StatusOK)
	}
	if got := loginWith(t, e.r, userEmail, userPassword, "192.0.2.52", device).Code; got != http.StatusUnauthorized {
		t.Errorf("login with the old email and the old device cookie = %d, want %d", got, http.StatusUnauthorized)
	}
	// And it is no way round the shared keys: with this network's key full, a
	// valid device cookie would be let in (TestADeviceCookieGetsPastTheSharedKeys);
	// this one is not.
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		wantLogin(t, e.r, newEmail, "not-the-password", "192.0.2.53", http.StatusUnauthorized)
	}
	if got := loginWith(t, e.r, newEmail, userPassword, "192.0.2.53", device).Code; got != http.StatusTooManyRequests {
		t.Errorf("login past a full network key with the old device cookie = %d, want %d", got, http.StatusTooManyRequests)
	}
}
