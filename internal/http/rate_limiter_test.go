package http

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// admitted reports whether one attempt on keys was admitted, keeping the
// reservation (the attempt stands, as a failed guess does).
func admitted(rl *rateLimiter, keys ...string) bool {
	_, ok := rl.reserve(keys...)
	return ok
}

func countOf(rl *rateLimiter, key string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if c, ok := rl.counters[key]; ok {
		return c.count
	}
	return 0
}

// TestRateLimiterAdmitsExactlyTheLimitThenRefuses: the limit-th attempt is
// admitted, the next is not.
func TestRateLimiterAdmitsExactlyTheLimitThenRefuses(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(3, time.Minute)

	for i := 1; i <= 3; i++ {
		if !admitted(rl, "k") {
			t.Fatalf("attempt %d refused, want admitted (limit is 3)", i)
		}
	}
	if admitted(rl, "k") {
		t.Fatal("attempt 4 admitted, want refused (limit reached)")
	}
	if got := countOf(rl, "k"); got != 3 {
		t.Errorf("count after a refused attempt = %d, want 3 - a refusal must not count", got)
	}
}

// TestRateLimiterKeysAreIndependent: attempts on one key never count against
// another (the login handler holds an IP and an identifier key).
func TestRateLimiterKeysAreIndependent(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(2, time.Minute)

	admitted(rl, "a")
	admitted(rl, "a")
	if admitted(rl, "a") {
		t.Error("third attempt on \"a\" admitted, want refused")
	}
	if !admitted(rl, "b") {
		t.Error("attempt on \"b\" refused, want admitted - \"a\"'s attempts must not leak into \"b\"'s counter")
	}
}

// TestRateLimiterReservesAllKeysOrNone: if one key is full, the other is not
// charged for a request that never ran.
func TestRateLimiterReservesAllKeysOrNone(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(2, time.Minute)

	admitted(rl, "id:x")
	admitted(rl, "id:x") // "id:x" is now full

	if admitted(rl, "ip:1", "id:x") {
		t.Fatal("reserve(ip:1, id:x) admitted with id:x full, want refused")
	}
	if got := countOf(rl, "ip:1"); got != 0 {
		t.Errorf("ip:1 count after a refused joint reserve = %d, want 0 - nothing may be charged on a refusal", got)
	}
	if got := countOf(rl, "id:x"); got != 2 {
		t.Errorf("id:x count = %d, want 2", got)
	}

	// And the success shape: both keys charged once.
	if !admitted(rl, "ip:2", "id:y") {
		t.Fatal("reserve(ip:2, id:y) refused, want admitted")
	}
	if countOf(rl, "ip:2") != 1 || countOf(rl, "id:y") != 1 {
		t.Errorf("counts after one joint reserve = %d, %d, want 1, 1", countOf(rl, "ip:2"), countOf(rl, "id:y"))
	}
}

// TestRateLimiterReleaseGivesTheAttemptBack: an attempt that never really
// happened (server busy) hands its slot back.
func TestRateLimiterReleaseGivesTheAttemptBack(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(2, time.Minute)

	res, ok := rl.reserve("ip:1", "id:x")
	if !ok {
		t.Fatal("reserve refused on a fresh limiter")
	}
	admitted(rl, "ip:1")
	res.release()
	res.release() // a second release is harmless

	if got := countOf(rl, "ip:1"); got != 1 {
		t.Errorf("ip:1 count after releasing one of two = %d, want 1", got)
	}
	if got := countOf(rl, "id:x"); got != 0 {
		t.Errorf("id:x count after releasing its only attempt = %d, want 0", got)
	}
	rl.mu.Lock()
	_, kept := rl.counters["id:x"]
	rl.mu.Unlock()
	if kept {
		t.Error("id:x still has a counter at zero, want it dropped")
	}
}

// TestRateLimiterReleaseAfterResetOrExpiryLeavesTheNewCounterAlone: a release
// undoes only the count it took. If a success wiped the key or the window
// rolled over, a late release must not eat into the attempts counted since.
func TestRateLimiterReleaseAfterResetOrExpiryLeavesTheNewCounterAlone(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(5, time.Minute)
	now := time.Now()
	rl.now = func() time.Time { return now }

	res, _ := rl.reserve("k")
	rl.reset("k")
	admitted(rl, "k")
	res.release()
	if got := countOf(rl, "k"); got != 1 {
		t.Errorf("count after a release that predates a reset = %d, want 1", got)
	}

	res, _ = rl.reserve("e")
	now = now.Add(time.Minute) // the window the reservation lives in has elapsed
	admitted(rl, "e")
	admitted(rl, "e")
	res.release()
	if got := countOf(rl, "e"); got != 2 {
		t.Errorf("count after a release that predates the window = %d, want 2", got)
	}
}

// TestRateLimiterResetClearsTheCounter: reset drops the count entirely, not
// permits one more attempt; a key at the limit is fully open again.
func TestRateLimiterResetClearsTheCounter(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(2, time.Minute)

	admitted(rl, "k")
	admitted(rl, "k")
	if admitted(rl, "k") {
		t.Fatal("third attempt admitted, want refused before reset")
	}

	rl.reset("k")
	for i := 1; i <= 2; i++ {
		if !admitted(rl, "k") {
			t.Fatalf("attempt %d refused right after reset, want the full width open again", i)
		}
	}
	if admitted(rl, "k") {
		t.Fatal("third attempt after reset admitted, want refused")
	}
}

// TestRateLimiterResetClearsEveryKeyGiven: a successful login resets its IP
// and identifier in one call.
func TestRateLimiterResetClearsEveryKeyGiven(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(5, time.Minute)
	admitted(rl, "ip:1", "id:x")
	rl.reset("ip:1", "id:x")
	if countOf(rl, "ip:1") != 0 || countOf(rl, "id:x") != 0 {
		t.Error("counters survive reset(ip, id), want both cleared")
	}
}

// TestRateLimiterWindowExpiryReopensTheKey: once the window has elapsed the
// key is allowed again, counting from zero.
func TestRateLimiterWindowExpiryReopensTheKey(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(2, time.Minute)
	now := time.Now()
	rl.now = func() time.Time { return now }

	admitted(rl, "k")
	admitted(rl, "k")
	if admitted(rl, "k") {
		t.Fatal("attempt admitted at the limit within the window, want refused")
	}

	now = now.Add(time.Minute) // exactly at the boundary: the window has elapsed
	if !admitted(rl, "k") {
		t.Fatal("attempt refused after the window elapsed, want admitted")
	}
	// The first attempt after expiry starts a new window at count 1.
	if got := countOf(rl, "k"); got != 1 {
		t.Errorf("count after the first post-expiry attempt = %d, want 1", got)
	}
}

// TestRateLimiterSweepsExpiredEntries: the map does not grow without bound;
// once other keys' windows have elapsed, the next call anywhere evicts them.
func TestRateLimiterSweepsExpiredEntries(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(5, time.Minute)
	now := time.Now()
	rl.now = func() time.Time { return now }

	for i := 0; i < 50; i++ {
		admitted(rl, string(rune('a'+i%26)))
	}
	rl.mu.Lock()
	before := len(rl.counters)
	rl.mu.Unlock()
	if before == 0 {
		t.Fatal("no counters were recorded")
	}

	now = now.Add(2 * time.Minute) // well past every key's window
	admitted(rl, "unrelated-key")  // any reserve sweeps as a side effect

	rl.mu.Lock()
	after := len(rl.counters)
	rl.mu.Unlock()
	// Only the key just reserved may remain.
	if after != 1 {
		t.Errorf("counters after sweeping = %d, want 1 (before: %d)", after, before)
	}
}

// TestRateLimiterConcurrentReservesAdmitExactlyTheLimit is #495's core
// property: with far more simultaneous callers than room, exactly `limit` are
// admitted, none lost to an unsynchronised read-modify-write, none let
// through uncounted. Run with -race (make check does).
func TestRateLimiterConcurrentReservesAdmitExactlyTheLimit(t *testing.T) {
	t.Parallel()
	const limit, callers = 10, 200
	rl := newRateLimiter(limit, time.Minute)

	var wg sync.WaitGroup
	var ok atomic.Int64
	start := make(chan struct{})
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			<-start
			if admitted(rl, "ip:1", "id:x") {
				ok.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := ok.Load(); got != limit {
		t.Errorf("admitted = %d, want exactly %d", got, limit)
	}
	if countOf(rl, "ip:1") != limit || countOf(rl, "id:x") != limit {
		t.Errorf("counts = %d, %d, want %d each", countOf(rl, "ip:1"), countOf(rl, "id:x"), limit)
	}
}

// TestRateLimiterIsRaceFreeUnderMixedConcurrentAccess exercises reserve,
// release and reset concurrently across overlapping keys. It asserts nothing
// about final state (the test above does); it gives `go test -race` something
// to catch if a path reaches the map or a counter without holding mu.
func TestRateLimiterIsRaceFreeUnderMixedConcurrentAccess(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(5, 50*time.Millisecond)

	var wg sync.WaitGroup
	keys := []string{"ip-1", "ip-2", "identifier-1", "identifier-2"}
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := keys[g%len(keys)]
			other := keys[(g+1)%len(keys)]
			for i := 0; i < 25; i++ {
				switch i % 3 {
				case 0:
					if res, ok := rl.reserve(key, other); ok {
						res.release()
					}
				case 1:
					rl.reserve(key)
				case 2:
					rl.reset(key)
				}
			}
		}(g)
	}
	wg.Wait()
	t.Log("no race detected across mixed reserve/release/reset calls")
}

// ladderLimiter is an escalating limiter on a fake clock: 3 attempts fill a
// minute-long window, lockouts of 1m, 3m, 10m, strikes forgotten after 1h.
func ladderLimiter() (*rateLimiter, *time.Time) {
	rl := newEscalatingRateLimiter(3, time.Minute, []time.Duration{time.Minute, 3 * time.Minute, 10 * time.Minute}, time.Hour)
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return now }
	return rl, &now
}

func fill(rl *rateLimiter, key string) {
	for i := 0; i < 3; i++ {
		admitted(rl, key)
	}
}

func strikesOf(rl *rateLimiter, key string) int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if c, ok := rl.counters[key]; ok {
		return c.strikes
	}
	return 0
}

// TestRateLimiterLockoutsEscalate: each time the same key fills its window the
// lock is the next step of the ladder, and the last step repeats.
func TestRateLimiterLockoutsEscalate(t *testing.T) {
	t.Parallel()
	rl, now := ladderLimiter()

	for _, step := range []time.Duration{time.Minute, 3 * time.Minute, 10 * time.Minute, 10 * time.Minute} {
		fill(rl, "k")
		if admitted(rl, "k") {
			t.Fatalf("admitted while locked for %v", step)
		}
		*now = now.Add(step - time.Nanosecond)
		if admitted(rl, "k") {
			t.Fatalf("admitted a nanosecond before the %v lock ends", step)
		}
		*now = now.Add(time.Nanosecond)
		if !admitted(rl, "k") {
			t.Fatalf("refused at the end of the %v lock", step)
		}
		// That admitted attempt opened a fresh window at count 1; two more fill it.
		if got := countOf(rl, "k"); got != 1 {
			t.Fatalf("count after the lock ended = %d, want 1 (a fresh window)", got)
		}
		admitted(rl, "k")
		admitted(rl, "k")
	}
}

// TestRateLimiterStrikesDecayAfterAQuietPeriod: a key left alone for the decay
// period after its lockout ends starts the ladder again; one back sooner does
// not.
func TestRateLimiterStrikesDecayAfterAQuietPeriod(t *testing.T) {
	t.Parallel()

	t.Run("comes back inside the decay", func(t *testing.T) {
		t.Parallel()
		rl, now := ladderLimiter()
		fill(rl, "k") // strike 1, locked 1m
		*now = now.Add(time.Minute + time.Hour - time.Nanosecond)
		fill(rl, "k") // still remembered: strike 2, 3m
		if got := strikesOf(rl, "k"); got != 2 {
			t.Errorf("strikes = %d, want 2", got)
		}
	})

	t.Run("comes back after the decay", func(t *testing.T) {
		t.Parallel()
		rl, now := ladderLimiter()
		fill(rl, "k")
		fill(rl, "j")
		*now = now.Add(time.Minute + time.Hour) // exactly the end of the quiet period
		fill(rl, "k")
		if got := strikesOf(rl, "k"); got != 1 {
			t.Errorf("strikes after a full quiet period = %d, want 1 (back to the first rung)", got)
		}
		// And the first rung is the short one.
		*now = now.Add(time.Minute)
		if !admitted(rl, "k") {
			t.Error("refused after the first-rung lockout, want admitted")
		}
	})
}

// TestRateLimiterResetForgetsStrikes: a success deletes the key and its
// ladder position with it.
func TestRateLimiterResetForgetsStrikes(t *testing.T) {
	t.Parallel()
	rl, now := ladderLimiter()
	fill(rl, "k")
	*now = now.Add(time.Minute)
	fill(rl, "k") // strike 2
	rl.reset("k")
	fill(rl, "k")
	if got := strikesOf(rl, "k"); got != 1 {
		t.Errorf("strikes after reset and one more fill = %d, want 1", got)
	}
}

// TestRateLimiterReleaseUndoesTheLockoutItCaused: handing back the attempt
// that filled the window reopens the key and returns the strike; a busy server
// must not walk a key up the ladder.
func TestRateLimiterReleaseUndoesTheLockoutItCaused(t *testing.T) {
	t.Parallel()
	rl, _ := ladderLimiter()
	admitted(rl, "k")
	admitted(rl, "k")
	res, ok := rl.reserve("k") // the third: fills the window
	if !ok {
		t.Fatal("third attempt refused")
	}
	if admitted(rl, "k") {
		t.Fatal("fourth attempt admitted, want locked")
	}
	res.release()
	if got := strikesOf(rl, "k"); got != 0 {
		t.Errorf("strikes after releasing the filling attempt = %d, want 0", got)
	}
	if !admitted(rl, "k") {
		t.Error("key still locked after the attempt that locked it was released")
	}
}

// TestRateLimiterSweepKeepsStrikesThenEvicts: a key with a strike outlives its
// window and lockout (the memory escalation needs) and is gone after decay.
func TestRateLimiterSweepKeepsStrikesThenEvicts(t *testing.T) {
	t.Parallel()
	rl, now := ladderLimiter()
	fill(rl, "k")
	admitted(rl, "quiet") // never fills: gone with its window

	size := func() int {
		rl.mu.Lock()
		defer rl.mu.Unlock()
		return len(rl.counters)
	}

	*now = now.Add(30 * time.Minute) // window and lockout over, decay not
	admitted(rl, "probe")            // any reserve sweeps
	rl.mu.Lock()
	_, kept := rl.counters["k"]
	_, quietKept := rl.counters["quiet"]
	rl.mu.Unlock()
	if !kept {
		t.Error("struck key swept before its decay, want kept")
	}
	if quietKept {
		t.Error("never-locked key survived its window")
	}

	*now = now.Add(2 * time.Hour)
	admitted(rl, "probe2")
	if got := size(); got != 1 {
		t.Errorf("counters after the decay = %d, want 1 (just the probe)", got)
	}
}

// TestRateLimiterKeysCarryTheirOwnRules: one reserve can span keys with
// different limits, all-or-none.
func TestRateLimiterKeysCarryTheirOwnRules(t *testing.T) {
	t.Parallel()
	rl := newRateLimiter(2, time.Minute)
	loose := limitRule{limit: 5, window: time.Hour}

	for i := 0; i < 2; i++ {
		if _, ok := rl.reserveRules(keyRule{"tight", rl.rule}, keyRule{"loose", loose}); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if _, ok := rl.reserveRules(keyRule{"tight", rl.rule}, keyRule{"loose", loose}); ok {
		t.Fatal("third attempt admitted with the tight key full")
	}
	if got := countOf(rl, "loose"); got != 2 {
		t.Errorf("loose count = %d, want 2 - a refused request must not charge it", got)
	}
	for i := 0; i < 3; i++ {
		if _, ok := rl.reserveRules(keyRule{"loose", loose}); !ok {
			t.Fatalf("loose attempt %d refused", i+3)
		}
	}
	if _, ok := rl.reserveRules(keyRule{"loose", loose}); ok {
		t.Error("sixth loose attempt admitted, want refused at its limit of 5")
	}
}
