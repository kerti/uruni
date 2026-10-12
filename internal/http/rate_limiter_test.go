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

// TestRateLimiterAdmitsExactlyTheLimitThenRefuses: the boundary itself - the
// limit-th attempt is still admitted, the one after it is not.
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

// TestRateLimiterKeysAreIndependent: attempts against one key must never
// count against a different one - the whole reason the login handler holds
// two independent keys per request (IP and identifier).
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

// TestRateLimiterReservesAllKeysOrNone: a request is reserved under its IP
// and its identifier together. If one is full the other must not be charged
// for a request that never ran.
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

	// And the success shape: both keys charged exactly once.
	if !admitted(rl, "ip:2", "id:y") {
		t.Fatal("reserve(ip:2, id:y) refused, want admitted")
	}
	if countOf(rl, "ip:2") != 1 || countOf(rl, "id:y") != 1 {
		t.Errorf("counts after one joint reserve = %d, %d, want 1, 1", countOf(rl, "ip:2"), countOf(rl, "id:y"))
	}
}

// TestRateLimiterReleaseGivesTheAttemptBack: an attempt that never really
// happened (the server was busy) hands its slot in the window back.
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

// TestRateLimiterReleaseAfterResetOrExpiryLeavesTheNewCounterAlone: a
// release must undo only the count it took. If a success wiped the key
// meanwhile, or the window rolled over, a late release has nothing to undo
// and must not eat into the attempts counted since.
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

// TestRateLimiterResetClearsTheCounter: reset must drop the count entirely,
// not merely permit one more attempt - a key that was at the limit is fully
// open again afterward, for the limit's full width.
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

// TestRateLimiterWindowExpiryReopensTheKey: once the fixed window has
// elapsed, the same key must be allowed again and start counting from
// zero, not from wherever it left off.
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

// TestRateLimiterSweepsExpiredEntries proves the map does not grow without
// bound: once other keys' windows have elapsed, the next call anywhere
// evicts them, rather than the map only ever growing for the life of the
// process.
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
// property at the limiter: with far more simultaneous callers than room,
// exactly `limit` are admitted - none lost to an unsynchronised
// read-modify-write, none let through because nobody had been counted yet.
// Run with -race, which make check does.
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

// TestRateLimiterIsRaceFreeUnderMixedConcurrentAccess exercises every
// method - reserve, release, reset - concurrently across overlapping
// keys. It asserts nothing about the final state (the exactness test above
// is for that); its only job is to give `go test -race` something to catch
// if any path reaches the map or a counter without holding mu.
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
