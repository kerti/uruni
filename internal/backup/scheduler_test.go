package backup

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRunSchedulerWritesOnceThenStopsCleanlyOnCancel: the scheduler writes
// today's dump (the immediate tick, see scheduler.go) and returns promptly,
// with no leaked goroutine, once ctx is cancelled.
//
// Serial on purpose, no t.Parallel: it asserts 2s wall-clock deadlines that
// parallel -race tests competing for the CPU could trip.
func TestRunSchedulerWritesOnceThenStopsCleanlyOnCancel(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	logger := testDiscardLogger()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunScheduler(ctx, sqlDB, uploadsDir, backupDir, logger)
		close(done)
	}()

	// The immediate tick runs synchronously before RunScheduler reaches its select;
	// waiting for it needs only a moment for the goroutine to be scheduled, not a
	// schedulerInterval.
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
