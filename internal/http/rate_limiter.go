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

// Escalation (#500). A key that fills its window is locked, and the lock grows
// each time the same key fills a window again: loginLockouts, in order, then
// the last one for every lockout after. The first step equals the window, so
// the first lockout is exactly what the fixed window always gave; an attacker
// who simply waits it out and tries again meets 15 minutes, then an hour -
// ten guesses an hour per key instead of ten every five minutes.
//
// The strikes are forgotten after lockoutDecay of quiet, counted from the end
// of the last lockout (a key that earned an hour is not an hour-per-lockout
// key forever), or at once by a success, which deletes the key (reset). A day
// is long enough that a persistent guesser never drops back down the ladder
// between visits and short enough that a treasurer who locked herself out on
// Monday starts from five minutes again on Wednesday. Every strike lives in
// the key's own counter, so it is swept with it: a key costs memory until its
// window, its lockout and its decay have all passed, and no longer.
var loginLockouts = []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour}

const lockoutDecay = 24 * time.Hour

// The per-identifier ceiling (#500). The identifier is counted three ways by
// login: per client network, per identifier together with that network, and
// per identifier alone. The first two are the 10-per-5-minutes above and are
// what a lockout of the treasurer would have to come through; the third is
// this - a flat, looser ceiling across every network at once.
//
// It exists because the pair key alone leaves single-account brute force
// bounded only by how many addresses the attacker owns: each fresh address
// brings a fresh ten. Fifty an hour makes the account's guess rate a number
// no attacker can multiply with more addresses. It is deliberately not
// escalating and not renewed by a success - it only slows, and the worst it
// can ever cost is the rest of one hour. Setting it well above anything a
// person typing her own password reaches is what keeps it from being the lock
// the pair key was introduced to remove; a device cookie
// (device_tokens.go) is what keeps her out of it when it does trip.
const (
	loginIdentifierCeiling       = 50
	loginIdentifierCeilingWindow = time.Hour
)

// limitRule is one key's budget: how many attempts a window admits, and what
// filling it costs.
type limitRule struct {
	limit  int
	window time.Duration

	// ladder is the lockout after each consecutive strike (the last step
	// repeats). Empty means no escalation: a full window simply stays closed
	// until it ends, the fixed window this limiter began as.
	ladder []time.Duration
	// decay is how long after a lockout ends the strikes are remembered.
	decay time.Duration
}

// keyRule pairs a key with the rule that governs it. One reserve can span
// keys with different rules (login's ceiling is looser than its pair key),
// still all-or-none under one lock.
type keyRule struct {
	key  string
	rule limitRule
}

// rateLimitCounter is one key's state: how many attempts have been reserved
// in the current window, whether the key is locked, and how many lockouts in
// a row it has earned.
type rateLimitCounter struct {
	rule  limitRule
	count int
	// resetAt ends the window. lockedUntil, when set, ends the lockout the
	// window filling caused; a locked key refuses everything until then.
	resetAt     time.Time
	lockedUntil time.Time

	strikes      int
	strikesUntil time.Time
}

func (c *rateLimitCounter) locked(now time.Time) bool {
	return now.Before(c.lockedUntil)
}

// rateLimiter is an attempt counter with fixed windows and escalating
// lockouts, safe for concurrent use. The login handler holds one instance and
// reserves against several keys together (issue #115): a distributed guesser
// (many IPs, one email) and a single-account brute force (one IP, many
// emails) each trip a different key, so either alone would miss one of the
// two attacks.
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
	rule     limitRule
	counters map[string]*rateLimitCounter

	// now stands in for time.Now in tests, so a window's expiry can be
	// exercised without a real sleep.
	now func() time.Time
}

// newRateLimiter is the plain fixed window: limit attempts per window, then
// closed until the window ends.
func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		rule:     limitRule{limit: limit, window: window},
		counters: make(map[string]*rateLimitCounter),
		now:      time.Now,
	}
}

// newEscalatingRateLimiter is newRateLimiter whose lockouts grow with each
// repeat (see loginLockouts) and are forgotten after decay of quiet.
func newEscalatingRateLimiter(limit int, window time.Duration, ladder []time.Duration, decay time.Duration) *rateLimiter {
	rl := newRateLimiter(limit, window)
	rl.rule.ladder = ladder
	rl.rule.decay = decay
	return rl
}

// reservation is what a successful reserve hands back: the exact counters it
// incremented, so release undoes precisely those and nothing else.
type reservation struct {
	rl       *rateLimiter
	counters map[string]*rateLimitCounter
}

// reserve counts one attempt against every key at once, under the limiter's
// own rule, or none. See reserveRules.
func (rl *rateLimiter) reserve(keys ...string) (*reservation, bool) {
	rules := make([]keyRule, len(keys))
	for i, key := range keys {
		rules[i] = keyRule{key: key, rule: rl.rule}
	}
	return rl.reserveRules(rules...)
}

// reserveRules counts one attempt against every key at once, or none: if any
// key is locked it returns false and changes nothing (a refused request must
// not use up the other keys' budgets). The check and the increments happen
// under one lock, so N concurrent callers on a key with room for k admit
// exactly k of them - the property the pre-#495 check-then-record pair could
// not give.
//
// The attempt that fills a key's window is admitted and locks the key for
// whatever its next strike costs; everything after it is refused until the
// lock ends, and the first attempt after that opens a fresh window.
//
// The caller owns the reservation until it decides what the attempt was:
// keep it (a real failed guess: do nothing), release it (nothing was
// attempted: the server was busy, or broke), or reset the keys (success).
func (rl *rateLimiter) reserveRules(rules ...keyRule) (*reservation, bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	rl.sweepLocked(now)

	live := make([]*rateLimitCounter, len(rules))
	for i, kr := range rules {
		c, ok := rl.counters[kr.key]
		if !ok {
			continue
		}
		if c.locked(now) {
			return nil, false
		}
		live[i] = c
	}

	res := &reservation{rl: rl, counters: make(map[string]*rateLimitCounter, len(rules))}
	for i, kr := range rules {
		c := live[i]
		// A window that has run out, or a lockout that has ended, starts a
		// new counter - new, not reset in place, so a reservation still
		// holding the old one cannot release into the new window. Strikes
		// travel across while they have not decayed.
		if c == nil || !now.Before(c.resetAt) || !c.lockedUntil.IsZero() {
			fresh := &rateLimitCounter{rule: kr.rule, resetAt: now.Add(kr.rule.window)}
			if c != nil && now.Before(c.strikesUntil) {
				fresh.strikes, fresh.strikesUntil = c.strikes, c.strikesUntil
			}
			c = fresh
			rl.counters[kr.key] = c
		}
		c.count++
		if c.count >= c.rule.limit {
			c.lock(now)
		}
		res.counters[kr.key] = c
	}
	return res, true
}

// lock closes a counter whose window just filled. Without a ladder it stays
// closed until the window ends; with one, for the next step of the ladder,
// and the strike is remembered for decay after that.
func (c *rateLimitCounter) lock(now time.Time) {
	if len(c.rule.ladder) == 0 {
		c.lockedUntil = c.resetAt
		return
	}
	step := min(c.strikes, len(c.rule.ladder)-1)
	c.lockedUntil = now.Add(c.rule.ladder[step])
	c.strikes++
	c.strikesUntil = c.lockedUntil.Add(c.rule.decay)
}

// release gives the reservation back, as if the attempt had never been
// taken. It only touches a counter that is still the live one for its key:
// if the window expired or a success reset the key in the meantime, the
// attempt is already out of the books and a release must not eat into a
// newer window's count. Calling it twice is harmless.
//
// If the attempt handed back was the one that filled the window, the lockout
// it caused is undone with it, strike included: a key must not be locked, or
// climb the ladder, because of an attempt that never ran.
func (res *reservation) release() {
	rl := res.rl
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	for key, c := range res.counters {
		if rl.counters[key] != c {
			continue
		}
		if c.count > 0 {
			c.count--
		}
		if c.count < c.rule.limit && c.locked(now) {
			c.lockedUntil = time.Time{}
			if len(c.rule.ladder) > 0 {
				c.strikes--
				if c.strikes == 0 {
					c.strikesUntil = time.Time{}
				}
			}
		}
		if c.count == 0 && c.strikes == 0 {
			delete(rl.counters, key)
		}
	}
	res.counters = nil
}

// reset clears every key's counter entirely, strikes included. A successful
// login wipes the slate for the keys it was reserved under (issue #115: "a
// success clears the counter") rather than leaving a partial count to expire
// on its own - a treasurer who mistyped her password twice before getting it
// right should not still be two attempts closer to a lockout afterward.
func (rl *rateLimiter) reset(keys ...string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for _, key := range keys {
		delete(rl.counters, key)
	}
}

// sweepLocked drops every counter whose window, lockout and strike memory
// have all elapsed. Called as a side effect of every reserve() call, the same
// lazy-sweep-on-write shape session_store.go already uses for expired
// sessions (ADR-013's scope: "never a background ticker") - so the map cannot
// grow without bound over a long-running process even though nothing evicts
// a key still inside its own live window. mu must already be held.
func (rl *rateLimiter) sweepLocked(now time.Time) {
	for k, c := range rl.counters {
		if !now.Before(c.resetAt) && !now.Before(c.lockedUntil) && !now.Before(c.strikesUntil) {
			delete(rl.counters, k)
		}
	}
}
