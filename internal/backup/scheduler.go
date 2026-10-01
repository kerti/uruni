package backup

import (
	"context"
	"log/slog"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// schedulerInterval is how often RunScheduler wakes up to ask "does today
// have its dump yet". An hour, not 24h: a fixed 24h ticker anchored at
// process start drifts away from local midnight over repeated restarts and,
// worse, never re-aligns after downtime - ADR-013 already accepts
// "scheduled work pauses if the app is down - acceptable, restart resumes",
// but resuming promptly still matters. Hourly polling against
// WriteDailyIfNeeded's own idempotent "does today already have a dump"
// check is what makes a restart notice the gap within the hour rather than
// waiting up to another day for a fixed-offset timer to fire again.
const schedulerInterval = time.Hour

// RunScheduler is ADR-013's in-process scheduler: no cron library, no
// separate worker - a stdlib time.Ticker inside the same process, run as
// its own goroutine from `serve` (cmd/uruni/main.go) and stopped by ctx
// cancellation on shutdown, the same context signal.NotifyContext already
// wires up for the HTTP server's own graceful stop.
//
// Never overlaps itself, by construction rather than by a mutex: this is
// one goroutine running one sequential loop, and each tick's work
// (WriteDailyIfNeeded, then ApplyRetention) runs to completion - success or
// error - before the loop goes back to waiting on the channel. A slow tick
// simply delays when the next one starts; time.Ticker drops ticks it could
// not deliver rather than queuing them, so no second tick can ever begin
// while one is still running.
//
// Ordering with the boot-time format-version dump (main.go's own call to
// EnsureBootDump) is what keeps the two writers named in the issue - the
// scheduler and boot - from ever racing on the same file: main.go calls
// EnsureBootDump synchronously and only starts this goroutine afterward, so
// the very first thing RunScheduler's own immediate tick (below) can
// observe is a backupDir that boot's own dump, if any, has already finished
// writing.
func RunScheduler(ctx context.Context, q store.Querier, l *ledger.Ledger, uploadsDir, backupDir string, logger *slog.Logger) {
	tick := func() {
		name, written, err := WriteDailyIfNeeded(ctx, q, l, uploadsDir, backupDir, time.Now())
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

	// Fires once immediately, not just on the first hourly tick: a
	// treasurer who restarts the server after it was down for a few days
	// should not wait up to an hour for the day's dump. WriteDailyIfNeeded
	// is cheap to call when there is nothing to do (one directory listing,
	// no write) and is exactly as idempotent whether this call happens or
	// not, so calling it here costs nothing on the common case where
	// today's dump already exists.
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
