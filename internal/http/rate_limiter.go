package http

import (
	"sync"
	"time"
)

// loginRateLimitMaxAttempts and loginRateLimitWindow are POST /api/login's
// (and restore confirm's) fixed-window thresholds (ADR-007). Deliberately
// plain, per Tech-Design.md's "rate-limit specifics" deferral: a fixed window
// over an in-memory map, not a sliding window, token bucket or distributed
// store, since there is one process and, today, one account.
//
// Ten failures in five minutes means a treasurer fumbling her password is
// never the one locked out, while a remote guesser is bounded to two attempts
// a minute, each costing real argon2id CPU time (password.go). Named
// constants so a retune touches exactly this block.
const (
	loginRateLimitMaxAttempts = 10
	loginRateLimitWindow      = 5 * time.Minute
)

// registerRateLimitMaxAttempts and registerRateLimitWindow are POST
// /api/register's, counted per client IP (#495). Register is a once-per-
// instance act, so no honest caller nears ten attempts in five minutes; the
// figure matches login's for one number to remember, in separate constants so
// they can be retuned apart. Every attempt that reaches Register counts,
// whether it succeeds, is invalid, or is refused because an account exists:
// a refusal is cheap (it no longer hashes), but a burst is worth slowing.
const (
	registerRateLimitMaxAttempts = 10
	registerRateLimitWindow      = 5 * time.Minute
)

// Escalation (#500). A key that fills its window is locked, and the lock
// grows each time the same key fills a window again: loginLockouts in order,
// then the last for every later lockout. The first step equals the window, so
// the first lockout is what the fixed window always gave; an attacker who
// waits it out meets 15 minutes, then an hour: ten guesses an hour per key
// instead of ten every five minutes.
//
// Strikes are forgotten after lockoutDecay of quiet, counted from the end of
// the last lockout, or at once by a success, which deletes the key (reset). A
// day is long enough that a persistent guesser never drops down the ladder
// between visits, and short enough that a treasurer locked out on Monday
// starts from five minutes on Wednesday. Every strike lives in the key's
// counter and is swept with it: a key costs memory until its window, lockout
// and decay have all passed.
var loginLockouts = []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour}

const lockoutDecay = 24 * time.Hour

// The per-identifier ceiling (#500). login counts an identifier three ways:
// per client network, per identifier with that network, and per identifier
// alone. The first two are the 10-per-5-minutes above, the path a lockout of
// the treasurer would come through; the third is this flat, looser ceiling
// across every network.
//
// The pair key alone bounds single-account brute force only by how many
// addresses the attacker owns: each fresh address brings a fresh ten. Fifty
// an hour makes the account's guess rate a number more addresses cannot
// multiply. It is not escalating and not renewed by a success; the worst it
// costs is the rest of one hour. It sits well above what a person typing her
// password reaches, so it is not the lock the pair key was introduced to
// remove; a device cookie (device_tokens.go) keeps her out of it when it
// trips.
const (
	loginIdentifierCeiling       = 50
	loginIdentifierCeilingWindow = time.Hour
)

// limitRule is one key's budget: attempts a window admits, and what filling it
// costs.
type limitRule struct {
	limit  int
	window time.Duration

	// ladder is the lockout after each consecutive strike (the last repeats).
	// Empty means no escalation: a full window stays closed until it ends.
	ladder []time.Duration
	// decay is how long after a lockout ends the strikes are remembered.
	decay time.Duration
}

// keyRule pairs a key with its rule. One reserve can span keys with different
// rules (login's ceiling is looser than its pair key), still all-or-none
// under one lock.
type keyRule struct {
	key  string
	rule limitRule
}

// rateLimitCounter is one key's state: attempts reserved in the current
// window, whether it is locked, and how many lockouts in a row it has earned.
type rateLimitCounter struct {
	rule  limitRule
	count int
	// resetAt ends the window. lockedUntil, when set, ends the lockout the filled
	// window caused; a locked key refuses everything until then.
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
// reserves against several keys together (#115): a distributed guesser (many
// IPs, one email) and a single-account brute force (one IP, many emails)
// trip different keys.
//
// It counts attempts, not failures: reserve takes the attempt before the work
// it guards starts (#495), so an attempt in flight is already in the window.
// A limiter that recorded after the work let any number of concurrent
// requests pass its check together. Work that was not a real attempt hands
// its reservation back (release); a success wipes the key (reset).
//
// One mutex over one map, not sharded or lock-free: login is not a hot path
// next to the argon2id work each request does.
type rateLimiter struct {
	mu       sync.Mutex
	rule     limitRule
	counters map[string]*rateLimitCounter

	// now stands in for time.Now in tests, so expiry needs no real sleep.
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

// reserve counts one attempt against every key at once under the limiter's
// rule, or none. See reserveRules.
func (rl *rateLimiter) reserve(keys ...string) (*reservation, bool) {
	rules := make([]keyRule, len(keys))
	for i, key := range keys {
		rules[i] = keyRule{key: key, rule: rl.rule}
	}
	return rl.reserveRules(rules...)
}

// reserveRules counts one attempt against every key at once, or none: if any
// key is locked it returns false and changes nothing (a refused request must
// not use up the other keys' budgets). The check and increments happen under
// one lock, so N concurrent callers on a key with room for k admit exactly k;
// the pre-#495 check-then-record pair could not guarantee that.
//
// The attempt that fills a key's window is admitted and locks the key for
// its next strike's cost; everything after is refused until the lock ends,
// and the first attempt after that opens a fresh window.
//
// The caller owns the reservation until it decides what the attempt was: keep
// it (a real failed guess), release it (nothing was attempted: the server was
// busy, or broke), or reset the keys (success).
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
		// A window that has run out, or a lockout that has ended, starts a new
		// counter (not reset in place, so a reservation holding the old one cannot
		// release into the new window). Undecayed strikes travel across.
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

// lock closes a counter whose window just filled: closed until the window
// ends, or, with a ladder, for the next step, with the strike remembered for
// decay.
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

// release gives the reservation back as if the attempt had never been taken.
// It only touches a counter still live for its key: if the window expired or
// a success reset the key meanwhile, the attempt is already out of the books
// and a release must not eat into a newer window. Calling it twice is
// harmless.
//
// If the attempt handed back filled the window, the lockout it caused is
// undone with it, strike included: a key must not be locked, or climb the
// ladder, because of an attempt that never ran.
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
// login wipes the slate for its keys (#115) rather than leaving a partial
// count to expire: a treasurer who mistyped twice should not stay two
// attempts closer to a lockout.
func (rl *rateLimiter) reset(keys ...string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for _, key := range keys {
		delete(rl.counters, key)
	}
}

// sweepLocked drops every counter whose window, lockout and strike memory
// have all elapsed. Called by every reserve(), the lazy-sweep-on-write shape
// of session_store.go (ADR-013: never a background ticker), so the map cannot
// grow without bound. mu must be held.
func (rl *rateLimiter) sweepLocked(now time.Time) {
	for k, c := range rl.counters {
		if !now.Before(c.resetAt) && !now.Before(c.lockedUntil) && !now.Before(c.strikesUntil) {
			delete(rl.counters, k)
		}
	}
}
