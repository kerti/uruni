package http

import (
	"sync"
	"time"
)

// loginRateLimitMaxAttempts and loginRateLimitWindow are POST /api/login's
// (and restore confirm's) fixed-window thresholds - the shape ADR-007 has promised since M1 without
// ever naming it (issue #115). Deliberately plain, per Tech-Design.md's own
// "rate-limit specifics" deferral: a fixed window over a plain in-memory
// map, not a sliding window, a token bucket or a distributed store - there
// is exactly one process and, today, one account.
//
// Ten failures in five minutes is generous enough that a treasurer fumbling
// her own password a few times in a row is never the one who gets locked
// out, while still bounding a remote guesser to two attempts a minute -
// each of which argon2id (password.go's own parameters) already makes cost
// real CPU time. Named constants, not numbers inline, so a future retune -
// the PR body is where these numbers are meant to live, not a second ADR -
// touches exactly this block.
const (
	loginRateLimitMaxAttempts = 10
	loginRateLimitWindow      = 5 * time.Minute
)

// registerRateLimitMaxAttempts and registerRateLimitWindow are POST
// /api/register's, counted per client IP (#495). Register is a once-per-
// instance act, so no honest caller comes near ten attempts in five minutes;
// the number is the login one so there is a single figure to remember, and
// its own constants so the two can be retuned apart. Every attempt that
// reaches Register counts, whether it succeeds, is refused as invalid or is
// refused because an account exists: a refusal is cheap now (it no longer
// hashes), but a burst of them is still a caller worth slowing.
const (
	registerRateLimitMaxAttempts = 10
	registerRateLimitWindow      = 5 * time.Minute
)

// rateLimitCounter is one key's state: how many attempts have been reserved
// since resetAt - the window - was last (re)computed.
type rateLimitCounter struct {
	count   int
	resetAt time.Time
}

// rateLimiter is a fixed-window attempt counter, safe for concurrent use.
// The login handler holds one instance and reserves against it by IP and by
// identifier together (issue #115): a distributed guesser (many IPs, one
// email) and a single-account brute force (one IP, many emails) each trip a
// different key, so either alone would miss one of the two attacks.
//
// The counter counts attempts, not failures: reserve takes the attempt
// before the work it guards starts (#495), so an attempt still in flight is
// already inside the window. A limiter that only recorded after the work
// finished let any number of concurrent requests pass its check together,
// because none of them had been counted yet. Work that turns out not to have
// been a real attempt hands its reservation back (release); a success wipes
// the key (reset).
//
// One mutex over one map is the whole implementation - not sharded, not
// lock-free - matching the "deliberately plain" scope above. Login is not a
// hot path relative to the argon2id work each request already does under
// it.
type rateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	counters map[string]*rateLimitCounter

	// now stands in for time.Now in tests, so a window's expiry can be
	// exercised without a real sleep.
	now func() time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:    limit,
		window:   window,
		counters: make(map[string]*rateLimitCounter),
		now:      time.Now,
	}
}

// reservation is what a successful reserve hands back: the exact counters it
// incremented, so release undoes precisely those and nothing else.
type reservation struct {
	rl       *rateLimiter
	counters map[string]*rateLimitCounter
}

// reserve counts one attempt against every key at once, or none: if any key
// is already at the limit it returns false and changes nothing (a refused
// request must not use up the other key's budget). The check and the
// increments happen under one lock, so N concurrent callers on a key with
// room for k admit exactly k of them - the property the pre-#495 check-then-
// record pair could not give.
//
// The caller owns the reservation until it decides what the attempt was:
// keep it (a real failed guess: do nothing), release it (nothing was
// attempted: the server was busy, or broke), or reset the keys (success).
func (rl *rateLimiter) reserve(keys ...string) (*reservation, bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	rl.sweepLocked(now)

	for _, key := range keys {
		if c, ok := rl.counters[key]; ok && c.count >= rl.limit {
			return nil, false
		}
	}

	res := &reservation{rl: rl, counters: make(map[string]*rateLimitCounter, len(keys))}
	for _, key := range keys {
		c, ok := rl.counters[key]
		if !ok {
			c = &rateLimitCounter{resetAt: now.Add(rl.window)}
			rl.counters[key] = c
		}
		c.count++
		res.counters[key] = c
	}
	return res, true
}

// release gives the reservation back, as if the attempt had never been
// taken. It only touches a counter that is still the live one for its key:
// if the window expired or a success reset the key in the meantime, the
// attempt is already out of the books and a release must not eat into a
// newer window's count. Calling it twice is harmless.
func (res *reservation) release() {
	rl := res.rl
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for key, c := range res.counters {
		if rl.counters[key] != c {
			continue
		}
		if c.count > 0 {
			c.count--
		}
		if c.count == 0 {
			delete(rl.counters, key)
		}
	}
	res.counters = nil
}

// reset clears every key's counter entirely. A successful login wipes the
// slate for both keys it was reserved under (issue #115: "a success clears
// the counter") rather than leaving a partial count to expire on its own -
// a treasurer who mistyped her password twice before getting it right
// should not still be two attempts closer to a lockout afterward.
func (rl *rateLimiter) reset(keys ...string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for _, key := range keys {
		delete(rl.counters, key)
	}
}

// sweepLocked drops every counter whose window has already elapsed. Called
// as a side effect of every reserve() call, the same lazy-sweep-on-write
// shape session_store.go already uses for expired sessions (ADR-013's scope:
// "never a background ticker") - so the map cannot grow without bound over a
// long-running process even though nothing evicts a key still inside its own
// live window. mu must already be held.
func (rl *rateLimiter) sweepLocked(now time.Time) {
	for k, c := range rl.counters {
		if !now.Before(c.resetAt) {
			delete(rl.counters, k)
		}
	}
}
