package http

import (
	"sync"
	"time"
)

// loginRateLimitMaxAttempts and loginRateLimitWindow are POST /api/login's
// fixed-window thresholds - the shape ADR-007 has promised since M1 without
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

// rateLimitCounter is one key's state: how many failures have been counted
// since resetAt - loginRateLimitWindow - was last (re)computed.
type rateLimitCounter struct {
	count   int
	resetAt time.Time
}

// rateLimiter is a fixed-window failure counter, safe for concurrent use.
// The login handler holds one instance and checks it independently by IP
// and by identifier (issue #115): a distributed guesser (many IPs, one
// email) and a single-account brute force (one IP, many emails) each trip a
// different key, so either alone would miss one of the two attacks.
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

// blocked reports whether key has already reached the limit within its
// current window. It records nothing itself - the handler calls it as a
// pure pre-check before doing any work for the request, so a caller already
// locked out never reaches auth.Authenticate's argon2id cost at all, and a
// mere check never itself counts as an attempt.
func (rl *rateLimiter) blocked(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	rl.sweepLocked(now)

	c, ok := rl.counters[key]
	return ok && c.count >= rl.limit
}

// recordFailure counts one failed attempt against key, opening a fresh
// window if none is open for it yet.
func (rl *rateLimiter) recordFailure(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	rl.sweepLocked(now)

	c, ok := rl.counters[key]
	if !ok {
		c = &rateLimitCounter{resetAt: now.Add(rl.window)}
		rl.counters[key] = c
	}
	c.count++
}

// reset clears key's counter entirely. A successful login wipes the slate
// for both keys it was checked under (issue #115: "a success clears the
// counter") rather than leaving a partial count to expire on its own -
// a treasurer who mistyped her password twice before getting it right
// should not still be two attempts closer to a lockout afterward.
func (rl *rateLimiter) reset(key string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.counters, key)
}

// sweepLocked drops every counter whose window has already elapsed. Called
// as a side effect of every blocked() and recordFailure() call, the same
// lazy-sweep-on-write shape session_store.go already uses for expired
// sessions (ADR-013's scope: "never a background ticker") - so the map
// cannot grow without bound over a long-running process even though
// nothing evicts a key still inside its own live window. mu must already be
// held.
func (rl *rateLimiter) sweepLocked(now time.Time) {
	for k, c := range rl.counters {
		if !now.Before(c.resetAt) {
			delete(rl.counters, k)
		}
	}
}
