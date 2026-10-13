package http

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// #495: the work register and login do is bounded three ways: an
// already-registered instance is refused before any hashing, the limiter
// counts an attempt while in flight, and a process-wide gate caps concurrent
// argon2id computations. Each is held to its number with deterministic seams:
// a probe stands in for argon2id, counts, and can park computations so a
// burst certainly overlaps. None sleeps to order events; the only clock is
// the gate's slot wait, shrunk where a test needs it to expire.

// hashProbe replaces auth's hash and verify. Every call is counted; while
// parking is set, a call blocks after announcing itself on entered until park
// is closed. verify always answers "wrong password".
type hashProbe struct {
	hashes   atomic.Int64
	verifies atomic.Int64
	entered  chan struct{}
	park     chan struct{}
	parking  atomic.Bool
}

func newHashProbe() *hashProbe {
	return &hashProbe{entered: make(chan struct{}, 256), park: make(chan struct{})}
}

func (p *hashProbe) hold() {
	p.entered <- struct{}{}
	if p.parking.Load() {
		<-p.park
	}
}

func (p *hashProbe) option() auth.Option {
	return auth.WithPasswordFuncs(
		func(string) (string, error) {
			p.hashes.Add(1)
			p.hold()
			return "not-a-real-hash", nil
		},
		func(string, string) (bool, error) {
			p.verifies.Add(1)
			p.hold()
			return false, nil
		},
	)
}

// awaitEntered blocks until n computations have announced themselves.
func (p *hashProbe) awaitEntered(n int) {
	for i := 0; i < n; i++ {
		<-p.entered
	}
}

// drain forgets announcements of finished computations, so a later
// awaitEntered counts only what follows.
func (p *hashProbe) drain() {
	for {
		select {
		case <-p.entered:
		default:
			return
		}
	}
}

func probedRouter(t *testing.T, opts ...auth.Option) http.Handler {
	t.Helper()
	sqlDB := testStoreDB(t)
	return New(testAssets(), testBuild, ledger.New(sqlDB), store.New(sqlDB), sqlDB, nil, testLogger(), auth.New(sqlDB, opts...), "", t.TempDir(), t.TempDir(), nil)
}

func postRegisterFrom(r http.Handler, email, password, ip string) *httptest.ResponseRecorder {
	//nolint:gosec // not a credential leak - this is the request body POST /api/register's own contract requires
	body, _ := json.Marshal(registerRequest{Email: email, Password: password})
	req := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewReader(body))
	req.RemoteAddr = net.JoinHostPort(ip, "1234")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// postLoginRaw is postLoginFrom without a *testing.T, for goroutines.
func postLoginRaw(r http.Handler, email, password, ip string) *httptest.ResponseRecorder {
	//nolint:gosec // not a credential leak - this is the request body POST /api/login's own contract requires
	body, _ := json.Marshal(loginRequest{Email: email, Password: password})
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	req.RemoteAddr = net.JoinHostPort(ip, "1234")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestRefusedRegisterDoesNoHashing is #495's first criterion on the wire: with
// an account present, a second register answers 409 and never calls the hasher.
func TestRefusedRegisterDoesNoHashing(t *testing.T) {
	t.Parallel()
	p := newHashProbe()
	r := probedRouter(t, p.option())

	if rec := postRegisterFrom(r, "first@example.org", "correct-horse-battery", "203.0.113.1"); rec.Code != http.StatusCreated {
		t.Fatalf("first POST /api/register = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	for _, email := range []string{"first@example.org", "second@example.org"} {
		assertAPIError(t, postRegisterFrom(r, email, "another-long-enough-password", "203.0.113.1"), http.StatusConflict, "already_registered")
	}

	if got := p.hashes.Load(); got != 1 {
		t.Errorf("hashes = %d, want 1 - only the register that created the account may hash", got)
	}
	if got := p.verifies.Load(); got != 0 {
		t.Errorf("verifies = %d, want 0", got)
	}
}

// TestRegisterIsRateLimitedPerIP: ten attempts from one address, then 429;
// another address is unaffected.
func TestRegisterIsRateLimitedPerIP(t *testing.T) {
	t.Parallel()
	r := probedRouter(t, newHashProbe().option())

	const ip = "203.0.113.2"
	for i := 0; i < registerRateLimitMaxAttempts; i++ {
		rec := postRegisterFrom(r, fmt.Sprintf("someone-%d@example.org", i), "correct-horse-battery", ip)
		want := http.StatusConflict
		if i == 0 {
			want = http.StatusCreated
		}
		if rec.Code != want {
			t.Fatalf("attempt %d from %s = %d, want %d (body: %s)", i+1, ip, rec.Code, want, rec.Body.String())
		}
	}

	assertAPIError(t, postRegisterFrom(r, "overflow@example.org", "correct-horse-battery", ip), http.StatusTooManyRequests, "too_many_requests")
	assertAPIError(t, postRegisterFrom(r, "other@example.org", "correct-horse-battery", "203.0.113.3"), http.StatusConflict, "already_registered")
}

// TestConcurrentRegisterBurstIsHeldToTheCeiling: on a fresh instance every
// request passes the pre-check, so the limiter stops a burst hashing 30 times.
// Exactly the ceiling reaches the hash; the rest are 429 before it. Of those
// that hashed, the transaction lets exactly one create the account.
func TestConcurrentRegisterBurstIsHeldToTheCeiling(t *testing.T) {
	t.Parallel()
	const callers = 30
	p := newHashProbe()
	p.parking.Store(true)
	r := probedRouter(t, p.option(), auth.WithMaxConcurrentHashes(callers), auth.WithSlotWait(time.Minute))

	results := make(chan *httptest.ResponseRecorder, callers)
	for i := 0; i < callers; i++ {
		go func() {
			results <- postRegisterFrom(r, fmt.Sprintf("someone-%d@example.org", i), "correct-horse-battery", "203.0.113.4")
		}()
	}

	// The refused callers answer while the admitted ones are parked in the hash:
	// "counted while in flight".
	refused := callers - registerRateLimitMaxAttempts
	for i := 0; i < refused; i++ {
		if rec := <-results; rec.Code != http.StatusTooManyRequests {
			t.Errorf("refused caller got %d, want %d (body: %s)", rec.Code, http.StatusTooManyRequests, rec.Body.String())
		}
	}
	p.awaitEntered(registerRateLimitMaxAttempts)
	close(p.park)

	counts := map[int]int{}
	for i := 0; i < registerRateLimitMaxAttempts; i++ {
		counts[(<-results).Code]++
	}
	if got := p.hashes.Load(); got != registerRateLimitMaxAttempts {
		t.Errorf("hashes = %d, want exactly %d", got, registerRateLimitMaxAttempts)
	}
	if counts[http.StatusCreated] != 1 || counts[http.StatusConflict] != registerRateLimitMaxAttempts-1 {
		t.Errorf("admitted callers' statuses = %v, want one 201 and %d 409", counts, registerRateLimitMaxAttempts-1)
	}
}

// TestConcurrentWrongPasswordLoginsAreHeldToTheCeiling is #495's second
// criterion: 50 simultaneous wrong guesses at one account from one address
// admit exactly loginRateLimitMaxAttempts to the hash. Before the fix all 50
// passed together, none yet counted.
func TestConcurrentWrongPasswordLoginsAreHeldToTheCeiling(t *testing.T) {
	t.Parallel()
	const callers = 50
	p := newHashProbe()
	r := probedRouter(t, p.option(), auth.WithMaxConcurrentHashes(callers), auth.WithSlotWait(time.Minute))
	if rec := postRegisterFrom(r, "treasurer@example.org", "correct-horse-battery", "203.0.113.5"); rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d", rec.Code, http.StatusCreated)
	}
	p.drain()
	p.parking.Store(true)

	results := make(chan *httptest.ResponseRecorder, callers)
	for i := 0; i < callers; i++ {
		go func() {
			results <- postLoginRaw(r, "treasurer@example.org", "not-the-password", "203.0.113.6")
		}()
	}

	refused := callers - loginRateLimitMaxAttempts
	for i := 0; i < refused; i++ {
		if rec := <-results; rec.Code != http.StatusTooManyRequests {
			t.Errorf("refused caller got %d, want %d (body: %s)", rec.Code, http.StatusTooManyRequests, rec.Body.String())
		}
	}
	p.awaitEntered(loginRateLimitMaxAttempts)
	close(p.park)

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		if rec := <-results; rec.Code != http.StatusUnauthorized {
			t.Errorf("admitted caller got %d, want %d (body: %s)", rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}
	if got := p.verifies.Load(); got != loginRateLimitMaxAttempts {
		t.Errorf("verifies = %d, want exactly %d", got, loginRateLimitMaxAttempts)
	}
}

// TestHashingConcurrencyLimitHoldsAcrossLogins: more simultaneous logins than
// slots, each from its own address so the limiter admits all, never run more
// than the limit at once.
func TestHashingConcurrencyLimitHoldsAcrossLogins(t *testing.T) {
	t.Parallel()
	const limit, callers = 2, 8
	var inFlight, maxInFlight atomic.Int64
	entered := make(chan struct{}, callers)
	park := make(chan struct{})
	r := probedRouter(t,
		auth.WithMaxConcurrentHashes(limit),
		auth.WithSlotWait(time.Minute),
		auth.WithPasswordFuncs(
			func(string) (string, error) { return "not-a-real-hash", nil },
			func(string, string) (bool, error) {
				n := inFlight.Add(1)
				for {
					m := maxInFlight.Load()
					if n <= m || maxInFlight.CompareAndSwap(m, n) {
						break
					}
				}
				entered <- struct{}{}
				<-park
				inFlight.Add(-1)
				return false, nil
			},
		))

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := postLoginRaw(r, fmt.Sprintf("nobody-%d@example.org", i), "whatever", fmt.Sprintf("198.51.100.%d", i+1))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("login %d = %d, want %d (body: %s)", i, rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
		}()
	}
	for i := 0; i < limit; i++ {
		<-entered
	}
	if got := inFlight.Load(); got != limit {
		t.Errorf("in flight with every slot taken = %d, want %d", got, limit)
	}
	close(park)
	wg.Wait()

	if got := maxInFlight.Load(); got != limit {
		t.Errorf("max in flight = %d, want exactly %d", got, limit)
	}
}

// TestBusyHasherAnswers503ServerBusyAndIsNotCountedAsAnAttempt: when no slot
// frees in the wait, login and restore confirm answer 503 with their code and
// a Retry-After, and the refused request gives its attempt back: twelve busy
// answers from one address, ceiling ten, leave all ten wrong guesses to spend.
func TestBusyHasherAnswers503ServerBusyAndIsNotCountedAsAnAttempt(t *testing.T) {
	t.Parallel()
	p := newHashProbe()
	r := probedRouter(t, p.option(), auth.WithMaxConcurrentHashes(1), auth.WithSlotWait(5*time.Millisecond))
	reg := postRegisterFrom(r, "treasurer@example.org", "correct-horse-battery", "203.0.113.7")
	if reg.Code != http.StatusCreated {
		t.Fatalf("POST /api/register = %d, want %d", reg.Code, http.StatusCreated)
	}
	authed := withSessionCookie{Handler: r, token: sessionCookie(reg)}

	// Someone else's login takes the only slot and parks there.
	p.drain()
	p.parking.Store(true)
	holder := make(chan *httptest.ResponseRecorder, 1)
	go func() { holder <- postLoginRaw(r, "holder@example.org", "whatever", "192.0.2.99") }()
	p.awaitEntered(1)

	const ip = "203.0.113.8"
	for i := 0; i < loginRateLimitMaxAttempts+2; i++ {
		rec := postLoginRaw(r, "treasurer@example.org", "not-the-password", ip)
		assertAPIError(t, rec, http.StatusServiceUnavailable, "server_busy")
		if got := rec.Header().Get("Retry-After"); got == "" {
			t.Errorf("busy login %d has no Retry-After header", i)
		}
	}
	for i := 0; i < loginRateLimitMaxAttempts+2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/restore/confirm", bytes.NewReader([]byte(`{"token":"t","password":"not-the-password"}`)))
		req.RemoteAddr = net.JoinHostPort(ip, "1234")
		authed.ServeHTTP(rec, req)
		assertAPIError(t, rec, http.StatusServiceUnavailable, "server_busy")
	}

	p.parking.Store(false)
	close(p.park)
	if rec := <-holder; rec.Code != http.StatusUnauthorized {
		t.Fatalf("the parked login finished with %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Nothing above was a guess: all ten are still available, the eleventh is
	// refused.
	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		if rec := postLoginRaw(r, "treasurer@example.org", "not-the-password", ip); rec.Code != http.StatusUnauthorized {
			t.Fatalf("login %d after the busy spell = %d, want %d - busy answers were counted (body: %s)", i+1, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}
	assertAPIError(t, postLoginRaw(r, "treasurer@example.org", "not-the-password", ip), http.StatusTooManyRequests, "too_many_requests")

	for i := 0; i < loginRateLimitMaxAttempts; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/restore/confirm", bytes.NewReader([]byte(`{"token":"t","password":"not-the-password"}`)))
		req.RemoteAddr = net.JoinHostPort(ip, "1234")
		authed.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("restore confirm %d after the busy spell = %d, want %d (body: %s)", i+1, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/restore/confirm", bytes.NewReader([]byte(`{"token":"t","password":"not-the-password"}`)))
	req.RemoteAddr = net.JoinHostPort(ip, "1234")
	authed.ServeHTTP(rec, req)
	assertAPIError(t, rec, http.StatusTooManyRequests, "too_many_requests")
}

// TestBusyHasherOnRegisterAnswers503AndIsNotCounted: a register that cannot get
// a slot answers 503 and keeps its attempt.
func TestBusyHasherOnRegisterAnswers503AndIsNotCounted(t *testing.T) {
	t.Parallel()
	p := newHashProbe()
	p.parking.Store(true)
	r := probedRouter(t, p.option(), auth.WithMaxConcurrentHashes(1), auth.WithSlotWait(5*time.Millisecond))

	holder := make(chan *httptest.ResponseRecorder, 1)
	go func() { holder <- postRegisterFrom(r, "holder@example.org", "correct-horse-battery", "192.0.2.98") }()
	p.awaitEntered(1)

	const ip = "203.0.113.9"
	for i := 0; i < registerRateLimitMaxAttempts+2; i++ {
		rec := postRegisterFrom(r, "treasurer@example.org", "correct-horse-battery", ip)
		assertAPIError(t, rec, http.StatusServiceUnavailable, "server_busy")
		if got := rec.Header().Get("Retry-After"); got == "" {
			t.Errorf("busy register %d has no Retry-After header", i)
		}
	}

	p.parking.Store(false)
	close(p.park)
	if rec := <-holder; rec.Code != http.StatusCreated {
		t.Fatalf("the parked register finished with %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// The busy answers were handed back, so the address still has its ten attempts
	// (each now a cheap 409), and only the eleventh is refused.
	for i := 0; i < registerRateLimitMaxAttempts; i++ {
		if rec := postRegisterFrom(r, "treasurer@example.org", "correct-horse-battery", ip); rec.Code != http.StatusConflict {
			t.Fatalf("register %d after the busy spell = %d, want %d - busy answers were counted (body: %s)", i+1, rec.Code, http.StatusConflict, rec.Body.String())
		}
	}
	assertAPIError(t, postRegisterFrom(r, "treasurer@example.org", "correct-horse-battery", ip), http.StatusTooManyRequests, "too_many_requests")
}

// TestMapAuthErrorAnswersErrBusyWith503 pins the mapping on its own.
func TestMapAuthErrorAnswersErrBusyWith503(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	mapAuthError(rec, testLogger(), fmt.Errorf("verifying: %w", auth.ErrBusy))
	assertAPIError(t, rec, http.StatusServiceUnavailable, "server_busy")
}

var _ = sql.ErrNoRows
