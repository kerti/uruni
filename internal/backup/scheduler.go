package backup

import (
	"context"
	"database/sql"
	"log/slog"
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
func RunScheduler(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, logger *slog.Logger) {
	tick := func() {
		name, written, err := WriteDailyIfNeeded(ctx, sqlDB, uploadsDir, backupDir, time.Now())
		if err != nil {
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

	// Fires once immediately, not only on the first hourly tick: a treasurer who
	// restarts after days down should not wait up to an hour for the day's dump.
	// WriteDailyIfNeeded is cheap when there is nothing to do (one directory
	// listing, no write) and idempotent either way.
	tick()

	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}
