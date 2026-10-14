package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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

// jsonLogger writes JSON records into buf, so a test can read back level and
// attributes instead of matching rendered text.
func jsonLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, nil))
}

// lockedBuffer is a bytes.Buffer safe to read while the scheduler goroutine is
// still writing to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestRunLoopSurvivesATickThatPanics: the first tick panics (the immediate one
// at start), and the loop must log it at error and still run the next. Without
// the recover the panic would take the process down: this goroutine sits
// outside chi's Recoverer.
func TestRunLoopSurvivesATickThatPanics(t *testing.T) {
	t.Parallel()
	var logs lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	var ticks atomic.Int32
	second := make(chan struct{})
	tick := func() {
		switch ticks.Add(1) {
		case 1:
			panic("boom")
		case 2:
			close(second)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runLoop(ctx, 5*time.Millisecond, tick, logger)
		close(done)
	}()

	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("no tick after the one that panicked: the loop died with it")
	}
	cancel()
	<-done

	var rec map[string]any
	line, _, _ := strings.Cut(logs.String(), "\n")
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("first log line %q is not JSON: %v", line, err)
	}
	if rec["level"] != "ERROR" {
		t.Errorf("panic logged at level %v, want ERROR", rec["level"])
	}
	if stack, _ := rec["stack"].(string); !strings.Contains(stack, "TestRunLoopSurvivesATickThatPanics") {
		t.Errorf("logged stack = %q, want it to name the panicking tick", stack)
	}
}

// TestSafeTickLogsTheTypeNotTheValue: ADR-022 keeps names and amounts out of
// the log, and a panic value built by the program could hold either. Only a
// runtime error, whose text the runtime wrote, is logged as is.
func TestSafeTickLogsTheTypeNotTheValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		panicWith func()
		wantIn    string
		wantNotIn string
	}{
		{
			name:      "a string built from data",
			panicWith: func() { panic("member Siti Aminah owes Rp 50000") },
			wantIn:    `"panic":"string"`,
			wantNotIn: "Siti",
		},
		{
			name:      "an error built from data",
			panicWith: func() { panic(fmt.Errorf("posting 12345 for %s", "Budi")) },
			wantIn:    `"panic":"*`, // some error type name, whichever fmt picked
			wantNotIn: "Budi",
		},
		{
			name: "a runtime error",
			panicWith: func() {
				s := []int{}
				_ = s[len(os.Args)+100]
			},
			wantIn: "index out of range",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			safeTick(tt.panicWith, jsonLogger(&buf))

			// The stack is exempt from the check: stack.WithoutArgs leaves source
			// paths and function names, with each frame's argument words dropped.
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("log %q is not JSON: %v", buf.String(), err)
			}
			delete(rec, "stack")
			attrs, _ := json.Marshal(rec)
			if !strings.Contains(string(attrs), tt.wantIn) {
				t.Errorf("log attrs = %s, want %q", attrs, tt.wantIn)
			}
			if tt.wantNotIn != "" && strings.Contains(string(attrs), tt.wantNotIn) {
				t.Errorf("log attrs = %s, must not contain %q", attrs, tt.wantNotIn)
			}
		})
	}
}

// TestRunLoopReturnsOnlyAfterTheTickInFlightFinishes is the join the shutdown
// path relies on: ctx cancelled mid-tick must not let runLoop return (and the
// database close) until that tick is done, and must return promptly after.
func TestRunLoopReturnsOnlyAfterTheTickInFlightFinishes(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	tick := func() {
		close(started)
		<-release
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runLoop(ctx, time.Hour, tick, testDiscardLogger())
		close(done)
	}()

	<-started
	cancel()
	select {
	case <-done:
		t.Fatal("runLoop returned while its tick was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runLoop did not return after the tick finished")
	}
}

// TestRunSchedulerCancelledBeforeStartWritesNoDump: a ctx already cancelled at
// start (SIGTERM during boot) makes the immediate tick stop at its first
// database read rather than begin a dump, and RunScheduler returns at once.
func TestRunSchedulerCancelledBeforeStartWritesNoDump(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()

	var logs lockedBuffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		RunScheduler(ctx, sqlDB, uploadsDir, backupDir, slog.New(slog.NewJSONHandler(&logs, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunScheduler did not return for a cancelled ctx")
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir() = %v, want no error", err)
	}
	if len(entries) != 0 {
		t.Errorf("backupDir has %d entries, want none (a cancelled ctx must not start a dump, and must leave no temp file)", len(entries))
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("shutdown interruption logged at error: %s", logs.String())
	}
}
