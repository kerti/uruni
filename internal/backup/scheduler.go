package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// schedulerInterval is how often RunScheduler asks "does today have its dump
// yet". An hour, not 24h: a fixed 24h ticker anchored at process start drifts
// from local midnight over restarts and never re-aligns after downtime
// (ADR-013 accepts that scheduled work pauses when the app is down, but
// resuming promptly matters). Hourly polling against WriteDailyIfNeeded's
// idempotent "does today have a dump" check makes a restart notice the gap
// within the hour.
const schedulerInterval = time.Hour

// RunScheduler is ADR-013's in-process scheduler: no cron library or separate
// worker, a stdlib time.Ticker in a goroutine started from `serve`
// (cmd/uruni/main.go) and stopped by the ctx cancellation that also stops the
// HTTP server.
//
// It never overlaps itself, by construction not a mutex: one goroutine runs
// one sequential loop, and each tick's work (WriteDailyIfNeeded, then
// ApplyRetention) completes before the loop waits again. A slow tick delays
// the next; time.Ticker drops undeliverable ticks rather than queue them.
//
// Ordering with the boot-time dump keeps the two writers from racing on one
// file: main.go calls EnsureBootDump synchronously and starts this goroutine
// afterward, so RunScheduler's immediate tick sees a backupDir that boot's
// dump has finished writing.
//
// RunScheduler returns only after ctx is cancelled and the tick in flight (if
// any) has finished, so a caller that waits for it knows nothing is still
// writing a dump. serve waits, bounded, before it closes the database. A
// cancelled ctx stops a tick at its next database read; a dump already being
// written is not interrupted, and it goes through temp file + rename
// (WriteFileAtomic), so an abandoned write never leaves a half-written dump.
//
// A tick that panics is recovered and logged (safeTick) and the loop goes on:
// this goroutine sits outside chi's Recoverer, so an unrecovered panic here
// would take the whole process down, request handlers and all.
func RunScheduler(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, logger *slog.Logger) {
	tick := func() {
		name, written, err := WriteDailyIfNeeded(ctx, sqlDB, uploadsDir, backupDir, time.Now())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// Shutdown cut the tick short; the next boot's dump picks the day up.
				// Any other error, even one that lands as shutdown starts, is a real
				// failure and logged as one.
				logger.Info("scheduled backup dump interrupted by shutdown")
				return
			}
			logger.Error("scheduled backup dump failed", "error", err)
			return
		}
		if !written {
			return
		}
		logger.Info("wrote daily backup dump", "name", name)
		if err := ApplyRetention(backupDir, DailyRetention, PreRestoreRetention); err != nil {
			logger.Error("backup retention failed", "error", err)
		}
	}

	runLoop(ctx, schedulerInterval, tick, logger)
}

// runLoop is RunScheduler's loop over an injectable tick and interval, the seam
// the tests use to make a tick panic or an interval short.
func runLoop(ctx context.Context, interval time.Duration, tick func(), logger *slog.Logger) {
	// Fires once immediately, not only on the first hourly tick: a treasurer who
	// restarts after days down should not wait up to an hour for the day's dump.
	// WriteDailyIfNeeded is cheap when there is nothing to do (one directory
	// listing, no write) and idempotent either way.
	safeTick(tick, logger)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// select picks at random when both are ready; a cancelled ctx wins.
			if ctx.Err() != nil {
				return
			}
			safeTick(tick, logger)
		}
	}
}

// safeTick runs one tick and turns a panic into an error log with the stack, so
// the next tick still runs. The panic value is logged only when the runtime
// raised it (an index out of range, a nil dereference: text that cannot carry
// ledger data); any other value is reduced to its type, because a value built
// by the program could hold a name or an amount and ADR-022 keeps those out of
// the log. The stack says where it happened either way.
func safeTick(tick func(), logger *slog.Logger) {
	defer func() {
		v := recover()
		if v == nil {
			return
		}
		what := fmt.Sprintf("%T", v)
		var rerr runtime.Error
		if err, ok := v.(error); ok && errors.As(err, &rerr) {
			what = rerr.Error()
		}
		logger.Error("scheduled backup tick panicked", "panic", what, "stack", stackWithoutArgs(debug.Stack()))
	}()
	tick()
}

// stackWithoutArgs keeps a goroutine stack's function names and file:line
// positions and drops each frame's argument words. debug.Stack prints those as
// raw hex, and an int64 argument is an amount (ADR-022).
func stackWithoutArgs(stack []byte) string {
	lines := strings.Split(string(stack), "\n")
	for i, line := range lines {
		if line == "" || strings.HasPrefix(line, "\t") || !strings.HasSuffix(line, ")") {
			continue
		}
		if open := strings.LastIndex(line, "("); open > 0 {
			lines[i] = line[:open] + "(...)"
		}
	}
	return strings.Join(lines, "\n")
}
