package backup

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// TestRunSchedulerWritesOnceThenStopsCleanlyOnCancel is the scheduler's own
// two promises: it writes today's dump (the immediate tick, scheduler.go's
// own comment on why it does not wait for the first hourly fire), and it
// returns - promptly, with no leaked goroutine spinning after ctx is
// cancelled - rather than blocking forever.
//
// Serial on purpose, no t.Parallel: it asserts 2s wall-clock deadlines, which a
// suite of parallel -race tests competing for the CPU could trip.
func TestRunSchedulerWritesOnceThenStopsCleanlyOnCancel(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)
	logger := testDiscardLogger()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunScheduler(ctx, q, l, uploadsDir, backupDir, logger)
		close(done)
	}()

	// The immediate tick (scheduler.go) runs synchronously before
	// RunScheduler ever reaches its select - waiting for it to have taken
	// effect only needs a moment for the goroutine to be scheduled, not a
	// full schedulerInterval.
	deadline := time.After(2 * time.Second)
	for {
		entries, err := os.ReadDir(backupDir)
		if err != nil {
			t.Fatalf("ReadDir() = %v, want no error", err)
		}
		if len(entries) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no dump appeared within 2s of starting RunScheduler")
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScheduler did not return within 2s of ctx cancellation")
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir() = %v, want no error", err)
	}
	if len(entries) != 1 {
		t.Errorf("backupDir has %d entries after stop, want exactly 1 (one immediate tick, no overlap)", len(entries))
	}
}
