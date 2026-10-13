package auth

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

// Every argon2id computation this package runs (hashing a new password,
// verifying a login, verifying the restore-confirm password) goes through
// Auth.hash or Auth.verify and so one gate: a counting semaphore sized by
// maxConcurrentHashes. The rate limiter in internal/http bounds how often one
// caller may try; this bounds how much of the machine all callers together
// spend at once.
//
// Why a bound: one computation allocates argonMemoryKiB (19 MiB) and burns a
// core for tens of milliseconds (argonParallelism is 1). Without a ceiling, N
// simultaneous public requests hold N * 19 MiB and queue for CPU on a box that
// also runs SQLite and the report renderer. Past the core count, concurrency
// buys no throughput, only a higher memory peak.

// maxConcurrentHashesCap is the most computations that ever run at once,
// whatever the cores: 4 * 19 MiB = 76 MiB of argon2 memory worst case, which a
// small self-host VPS (the target, Tech-Design) carries without swapping.
const maxConcurrentHashesCap = 4

// defaultSlotWait is how long a request waits for a slot before ErrBusy. A
// computation takes tens of milliseconds, so two seconds covers a busy moment's
// queue many times over and still answers before a client or proxy gives up;
// past that the box is saturated, and holding more requests open only adds
// to it.
const defaultSlotWait = 2 * time.Second

// defaultMaxConcurrentHashes is min(GOMAXPROCS, maxConcurrentHashesCap), at
// least one: the work is single-threaded CPU, so a slot per core is the most
// that runs in parallel, and the cap bounds memory on a many-core host.
func defaultMaxConcurrentHashes() int {
	n := runtime.GOMAXPROCS(0)
	if n > maxConcurrentHashesCap {
		n = maxConcurrentHashesCap
	}
	if n < 1 {
		n = 1
	}
	return n
}

// Option configures New. Defaults are production values; options let a test
// pick a small ceiling and short wait, and observe or block the work.
type Option func(*Auth)

// WithMaxConcurrentHashes sets the semaphore's size. Values below one are
// ignored.
func WithMaxConcurrentHashes(n int) Option {
	return func(a *Auth) {
		if n >= 1 {
			a.slots = make(chan struct{}, n)
		}
	}
}

// WithSlotWait sets how long a computation waits for a slot before
// Hash/Verify answer ErrBusy.
func WithSlotWait(d time.Duration) Option {
	return func(a *Auth) { a.slotWait = d }
}

// WithPasswordFuncs replaces the argon2id computations: a test seam to count
// calls, observe concurrency, or park a computation holding its slot, without
// a real hash or its timing. Production never passes it.
func WithPasswordFuncs(hash func(password string) (string, error), verify func(password, encoded string) (bool, error)) Option {
	return func(a *Auth) {
		a.hashFn = hash
		a.verifyFn = verify
	}
}

// acquire takes a slot, waiting up to slotWait, and returns the function that
// gives it back. A free slot is taken without arming a timer. ErrBusy means
// the wait elapsed; a cancelled ctx (the client left) returns its own error,
// not busyness.
func (a *Auth) acquire(ctx context.Context) (release func(), err error) {
	release = func() { <-a.slots }

	select {
	case a.slots <- struct{}{}:
		return release, nil
	default:
	}

	timer := time.NewTimer(a.slotWait)
	defer timer.Stop()
	select {
	case a.slots <- struct{}{}:
		return release, nil
	case <-timer.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a password-hashing slot: %w", ctx.Err())
	}
}

// hash is hashPassword behind the gate.
func (a *Auth) hash(ctx context.Context, password string) (string, error) {
	release, err := a.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return a.hashFn(password)
}

// verify is verifyPassword behind the gate.
func (a *Auth) verify(ctx context.Context, password, encoded string) (bool, error) {
	release, err := a.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	return a.verifyFn(password, encoded)
}
