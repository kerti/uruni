package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// probe is the test seam for password_gate.go: it stands in for the argon2id
// computations, counts every call, tracks how many run at once, and can park
// a computation until the test lets it go - so the tests below observe the
// gate without a real hash and without a sleep standing in for ordering.
type probe struct {
	hashes    atomic.Int64
	verifies  atomic.Int64
	inFlight  atomic.Int64
	maxFlight atomic.Int64

	// started receives one value per computation that begins. park, when
	// non-nil, holds every computation until it is closed.
	started chan struct{}
	park    chan struct{}
}

func newProbe(buffer int) *probe {
	return &probe{started: make(chan struct{}, buffer)}
}

func (p *probe) enter() func() {
	n := p.inFlight.Add(1)
	for {
		m := p.maxFlight.Load()
		if n <= m || p.maxFlight.CompareAndSwap(m, n) {
			break
		}
	}
	p.started <- struct{}{}
	if p.park != nil {
		<-p.park
	}
	return func() { p.inFlight.Add(-1) }
}

func (p *probe) option() Option {
	return WithPasswordFuncs(
		func(string) (string, error) {
			p.hashes.Add(1)
			defer p.enter()()
			return "not-a-real-hash", nil
		},
		func(string, string) (bool, error) {
			p.verifies.Add(1)
			defer p.enter()()
			return false, nil
		},
	)
}

func newProbedAuth(t *testing.T, p *probe, opts ...Option) (*Auth, func(email string)) {
	t.Helper()
	sqlDB := newTestDB(t)
	a := New(sqlDB, append([]Option{p.option()}, opts...)...)
	insertUser := func(email string) {
		t.Helper()
		if _, err := a.q.CreateUser(context.Background(), store.CreateUserParams{Email: email, PasswordHash: "x", CreatedAt: 1}); err != nil {
			t.Fatalf("CreateUser(%q) = %v", email, err)
		}
	}
	return a, insertUser
}

// TestRegisterOnASetUpInstanceDoesNoHashing is #495's first criterion: once
// an account exists, a refused register costs a COUNT and nothing else.
func TestRegisterOnASetUpInstanceDoesNoHashing(t *testing.T) {
	t.Parallel()
	p := newProbe(8)
	a, _ := newProbedAuth(t, p)
	ctx := context.Background()

	if _, err := a.Register(ctx, "first@example.org", "correct-horse-battery"); err != nil {
		t.Fatalf("first Register() = %v, want no error", err)
	}
	if got := p.hashes.Load(); got != 1 {
		t.Fatalf("hashes after the first register = %d, want 1", got)
	}

	for _, email := range []string{"first@example.org", "second@example.org"} {
		if _, err := a.Register(ctx, email, "another-long-enough-password"); !errors.Is(err, ErrAlreadyRegistered) {
			t.Fatalf("Register(%q) on a set-up instance = %v, want ErrAlreadyRegistered", email, err)
		}
	}
	if got := p.hashes.Load(); got != 1 {
		t.Errorf("hashes after two refused registers = %d, want still 1 - a refusal must not hash", got)
	}
	if got := p.verifies.Load(); got != 0 {
		t.Errorf("verifies = %d, want 0", got)
	}
}

// TestRegisterTransactionCountStaysAuthoritative: the pre-check is only an
// optimisation. An account created after the pre-check passed - here, from
// inside the hash, which runs between the pre-check and the transaction -
// must still be refused by the count inside the transaction, and no second
// row may land.
func TestRegisterTransactionCountStaysAuthoritative(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	a := New(sqlDB, WithPasswordFuncs(func(password string) (string, error) {
		_, err := store.New(sqlDB).CreateUser(context.Background(), store.CreateUserParams{
			Email: "winner@example.org", PasswordHash: "x", CreatedAt: 1,
		})
		if err != nil {
			t.Errorf("creating the racing account: %v", err)
		}
		return hashPassword(password)
	}, verifyPassword))

	_, err := a.Register(context.Background(), "loser@example.org", "correct-horse-battery")
	if !errors.Is(err, ErrAlreadyRegistered) {
		t.Fatalf("Register() that lost the race = %v, want ErrAlreadyRegistered", err)
	}
	var count int
	if err := sqlDB.QueryRow("SELECT count(*) FROM user").Scan(&count); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	if count != 1 {
		t.Errorf("user rows = %d, want 1", count)
	}
}

// TestPasswordWorkNeverExceedsTheConcurrencyLimit is #495's third test
// criterion: with far more callers than slots, the number of computations
// in flight reaches the limit and never passes it. Every computation parks
// until the test has seen the limit reached, so overlap is certain rather
// than hoped for.
func TestPasswordWorkNeverExceedsTheConcurrencyLimit(t *testing.T) {
	t.Parallel()
	const limit, callers = 3, 20
	p := newProbe(callers)
	p.park = make(chan struct{})
	// A wait far longer than the test: nobody may time out here.
	a, _ := newProbedAuth(t, p, WithMaxConcurrentHashes(limit), WithSlotWait(time.Minute))

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// An unknown email still verifies, against the decoy.
			if _, err := a.Authenticate(context.Background(), "nobody@example.org", "whatever-password"); !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("Authenticate() = %v, want ErrInvalidCredentials", err)
			}
		}()
	}

	for i := 0; i < limit; i++ {
		<-p.started
	}
	if got := p.inFlight.Load(); got != limit {
		t.Errorf("in flight with every slot taken = %d, want %d", got, limit)
	}
	close(p.park)
	wg.Wait()

	if got := p.maxFlight.Load(); got != limit {
		t.Errorf("max in flight = %d, want exactly %d (the limit, reached and never passed)", got, limit)
	}
	if got := p.verifies.Load(); got != callers {
		t.Errorf("verifies = %d, want %d - waiting callers must still be served", got, callers)
	}
}

// TestPasswordWorkWithNoFreeSlotAnswersErrBusy: the slot-timeout path, on
// both the verify side (Authenticate) and the hash side (Register). The
// holder parks until the end, so the wait can only end by timing out.
func TestPasswordWorkWithNoFreeSlotAnswersErrBusy(t *testing.T) {
	t.Parallel()
	p := newProbe(8)
	p.park = make(chan struct{})
	a, insertUser := newProbedAuth(t, p, WithMaxConcurrentHashes(1), WithSlotWait(10*time.Millisecond))
	insertUser("holder@example.org")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = a.Authenticate(context.Background(), "holder@example.org", "whatever-password")
	}()
	<-p.started // the only slot is now held

	_, err := a.Authenticate(context.Background(), "holder@example.org", "whatever-password")
	if !errors.Is(err, ErrBusy) {
		t.Errorf("Authenticate() with no free slot = %v, want ErrBusy", err)
	}
	_, err = a.Authenticate(context.Background(), "nobody@example.org", "whatever-password")
	if !errors.Is(err, ErrBusy) {
		t.Errorf("Authenticate(unknown email) with no free slot = %v, want ErrBusy, the same as for a known one", err)
	}
	if errors.Is(err, ErrInvalidCredentials) {
		t.Error("a busy refusal is wearing ErrInvalidCredentials - it would be counted as a wrong guess")
	}

	// Register's pre-check sees the holder's account, so give it a fresh
	// instance to reach the hash.
	p2 := newProbe(8)
	p2.park = make(chan struct{})
	b, _ := newProbedAuth(t, p2, WithMaxConcurrentHashes(1), WithSlotWait(10*time.Millisecond))
	regDone := make(chan struct{})
	go func() {
		defer close(regDone)
		_, _ = b.Register(context.Background(), "holder@example.org", "correct-horse-battery")
	}()
	<-p2.started
	if _, err := b.SetLogin(context.Background(), "holder@example.org", "correct-horse-battery"); !errors.Is(err, ErrBusy) {
		t.Errorf("SetLogin() with no free slot = %v, want ErrBusy", err)
	}

	close(p.park)
	close(p2.park)
	<-done
	<-regDone
}

// TestPasswordWorkReleasesItsSlotOnEveryPath: after a busy refusal and a
// finished computation the gate is whole again.
func TestPasswordWorkReleasesItsSlotOnEveryPath(t *testing.T) {
	t.Parallel()
	p := newProbe(8)
	a, _ := newProbedAuth(t, p, WithMaxConcurrentHashes(1), WithSlotWait(10*time.Millisecond))

	for i := 0; i < 5; i++ {
		if _, err := a.Authenticate(context.Background(), "nobody@example.org", "whatever-password"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: Authenticate() = %v, want ErrInvalidCredentials - a slot leaked", i, err)
		}
	}
}

// TestPasswordWorkWaitEndsWithTheRequestContext: a client that goes away
// while queued stops waiting, and is not reported as a busy server.
func TestPasswordWorkWaitEndsWithTheRequestContext(t *testing.T) {
	t.Parallel()
	p := newProbe(8)
	p.park = make(chan struct{})
	a, _ := newProbedAuth(t, p, WithMaxConcurrentHashes(1), WithSlotWait(time.Minute))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = a.Authenticate(context.Background(), "nobody@example.org", "whatever-password")
	}()
	<-p.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.Authenticate(ctx, "nobody@example.org", "whatever-password")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Authenticate() with a cancelled context = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrBusy) {
		t.Error("a cancelled request was reported as ErrBusy")
	}
	close(p.park)
	<-done
}

// TestDefaultConcurrencyLimitIsSmallAndNeverZero pins the sizing rule's
// bounds: at least one slot, and never more than the memory cap.
func TestDefaultConcurrencyLimitIsSmallAndNeverZero(t *testing.T) {
	t.Parallel()
	got := defaultMaxConcurrentHashes()
	if got < 1 || got > maxConcurrentHashesCap {
		t.Errorf("defaultMaxConcurrentHashes() = %d, want within [1, %d]", got, maxConcurrentHashesCap)
	}
	if capacity := cap(New(newTestDB(t)).slots); capacity != got {
		t.Errorf("New()'s slots = %d, want the default %d", capacity, got)
	}
}
