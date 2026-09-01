package http

import (
	"sync"
	"testing"
	"time"
)

// TestRateLimiterBlocksAtTheLimitNotBeforeIt: exactly limit-1 failures must
// still be allowed, and the limit-th failure is what actually trips it -
// the boundary itself, not just "eventually blocks".
func TestRateLimiterBlocksAtTheLimitNotBeforeIt(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)

	for i := 0; i < 2; i++ {
		if rl.blocked("k") {
			t.Fatalf("blocked(\"k\") after %d failures = true, want false (limit is 3)", i)
		}
		rl.recordFailure("k")
	}
	if rl.blocked("k") {
		t.Fatal("blocked(\"k\") after 2 failures = true, want false")
	}

	rl.recordFailure("k") // the 3rd failure
	if !rl.blocked("k") {
		t.Fatal("blocked(\"k\") after 3 failures = false, want true (limit reached)")
	}
}

// TestRateLimiterKeysAreIndependent: failures against one key must never
// count against a different one - the whole reason the login handler holds
// two independent keys per request (IP and identifier).
func TestRateLimiterKeysAreIndependent(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)

	rl.recordFailure("a")
	rl.recordFailure("a")
	if !rl.blocked("a") {
		t.Error("blocked(\"a\") = false, want true")
	}
	if rl.blocked("b") {
		t.Error("blocked(\"b\") = true, want false - \"a\"'s failures must not leak into \"b\"'s counter")
	}
}

// TestRateLimiterResetClearsTheCounter: reset must drop the count entirely,
// not merely permit one more attempt - a key that was at the limit is fully
// open again afterward, for the limit's full width.
func TestRateLimiterResetClearsTheCounter(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)

	rl.recordFailure("k")
	rl.recordFailure("k")
	if !rl.blocked("k") {
		t.Fatal("blocked(\"k\") = false, want true before reset")
	}

	rl.reset("k")
	if rl.blocked("k") {
		t.Fatal("blocked(\"k\") = true, want false immediately after reset")
	}

	rl.recordFailure("k")
	if rl.blocked("k") {
		t.Fatal("blocked(\"k\") = true after 1 failure post-reset, want false (limit is 2)")
	}
	rl.recordFailure("k")
	if !rl.blocked("k") {
		t.Fatal("blocked(\"k\") = false after 2 failures post-reset, want true")
	}
}

// TestRateLimiterWindowExpiryReopensTheKey: once the fixed window has
// elapsed, the same key must be allowed again and start counting from
// zero, not from wherever it left off.
func TestRateLimiterWindowExpiryReopensTheKey(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	now := time.Now()
	rl.now = func() time.Time { return now }

	rl.recordFailure("k")
	rl.recordFailure("k")
	if !rl.blocked("k") {
		t.Fatal("blocked(\"k\") = false, want true within the window")
	}

	now = now.Add(time.Minute) // exactly at the boundary: the window has elapsed
	if rl.blocked("k") {
		t.Fatal("blocked(\"k\") = true after the window elapsed, want false")
	}

	// A fresh failure after expiry must start a new window at count 1, not
	// resume accumulating past the old window's count.
	rl.recordFailure("k")
	if rl.blocked("k") {
		t.Fatal("blocked(\"k\") = true after 1 failure in the new window, want false (limit is 2)")
	}
}

// TestRateLimiterSweepsExpiredEntries proves the map does not grow without
// bound: once other keys' windows have elapsed, the next call anywhere
// evicts them, rather than the map only ever growing for the life of the
// process.
func TestRateLimiterSweepsExpiredEntries(t *testing.T) {
	rl := newRateLimiter(5, time.Minute)
	now := time.Now()
	rl.now = func() time.Time { return now }

	for i := 0; i < 50; i++ {
		rl.recordFailure(string(rune('a' + i%26)))
	}
	rl.mu.Lock()
	before := len(rl.counters)
	rl.mu.Unlock()
	if before == 0 {
		t.Fatal("no counters were recorded")
	}

	now = now.Add(2 * time.Minute) // well past every key's window
	rl.blocked("unrelated-key")    // any call sweeps as a side effect

	rl.mu.Lock()
	after := len(rl.counters)
	rl.mu.Unlock()
	// "unrelated-key" itself was just created by the blocked() call above
	// (a miss still allocates nothing - blocked() only reads - so it must
	// not appear at all), and every one of the 50 keys from before the
	// sweep must be gone.
	if after != 0 {
		t.Errorf("counters after sweeping = %d, want 0 (before: %d)", after, before)
	}
}

// TestRateLimiterConcurrentFailuresAreCountedExactly is the concurrency-
// safety requirement (issue #115): N goroutines each recording one failure
// against the same key must land exactly N, with none lost to an
// unsynchronized read-modify-write. Run with -race, which make check does.
func TestRateLimiterConcurrentFailuresAreCountedExactly(t *testing.T) {
	const n = 200
	rl := newRateLimiter(n+1, time.Minute) // above n, so nothing is refused mid-run

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			rl.recordFailure("shared-key")
		}()
	}
	wg.Wait()

	rl.mu.Lock()
	got := rl.counters["shared-key"].count
	rl.mu.Unlock()
	if got != n {
		t.Errorf("count after %d concurrent recordFailure() calls = %d, want %d", n, got, n)
	}
}

// TestRateLimiterIsRaceFreeUnderMixedConcurrentAccess exercises every
// method - blocked, recordFailure, reset - concurrently across overlapping
// keys. It asserts nothing about the final state (that is what the
// exactness test above is for); its only job is to give `go test -race`
// something to catch if any path reaches the map or a counter without
// holding mu.
func TestRateLimiterIsRaceFreeUnderMixedConcurrentAccess(t *testing.T) {
	rl := newRateLimiter(5, 50*time.Millisecond)

	var wg sync.WaitGroup
	keys := []string{"ip-1", "ip-2", "identifier-1", "identifier-2"}
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := keys[g%len(keys)]
			for i := 0; i < 25; i++ {
				switch i % 3 {
				case 0:
					rl.blocked(key)
				case 1:
					rl.recordFailure(key)
				case 2:
					rl.reset(key)
				}
			}
		}(g)
	}
	wg.Wait()
	t.Log("no race detected across mixed blocked/recordFailure/reset calls")
}
