package ledger

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The ledger reads one injected clock (WithClock). It flips a fund's day and
// month on the second its own zone crosses midnight.

// settableClock is a clock a test moves between calls.
type settableClock struct{ t time.Time }

func (c *settableClock) now() time.Time { return c.t }

func TestTheFundsDayAndMonthFlipExactlyAtItsMidnight(t *testing.T) {
	t.Parallel()
	for _, zone := range allZones {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()
			loc := zoneLoc(t, zone)
			midnight := time.Date(2026, time.October, 1, 0, 0, 0, 0, loc)
			clk := &settableClock{t: midnight.Add(-24 * time.Hour)}
			l := New(newTestDB(t), WithClock(clk.now))
			f := fundInZone(t, l, zone)
			ctx := context.Background()
			postEntry(t, l, f.fundID, f.cashID, f.mainID, "in", 10_000, "2026-09-30", nil)
			postOn1Oct := func() error {
				_, err := l.PostTransaction(ctx, PostTransactionParams{
					FundID: f.fundID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "in", Amount: 1_000, OccurredOn: "2026-10-01",
				})
				return err
			}

			// One second before midnight in the fund's zone: still 30 September.
			clk.t = midnight.Add(-time.Second)
			before, err := l.MonthlyReport(ctx, ReportParams{FundID: f.fundID})
			if err != nil {
				t.Fatalf("MonthlyReport() before midnight = %v", err)
			}
			if before.Month != "2026-09" || before.AsOf != "2026-09-30" || !before.Running {
				t.Errorf("before midnight: Month/AsOf/Running = %q/%q/%v, want 2026-09/2026-09-30/true", before.Month, before.AsOf, before.Running)
			}
			if b, err := l.Balances(ctx, f.fundID); err != nil || b.MonthIn != 10_000 {
				t.Errorf("Balances().MonthIn before midnight = %d (%v), want 10000: the running month is still September", b.MonthIn, err)
			}
			if err := postOn1Oct(); !errors.Is(err, ErrDateOutOfRange) {
				t.Errorf("posting on 1 October one second before midnight = %v, want ErrDateOutOfRange: it is not that day yet", err)
			}

			// At midnight it is 1 October: the month turns over and the day is allowed.
			clk.t = midnight
			after, err := l.MonthlyReport(ctx, ReportParams{FundID: f.fundID})
			if err != nil {
				t.Fatalf("MonthlyReport() at midnight = %v", err)
			}
			if after.Month != "2026-10" || after.AsOf != "2026-10-01" || !after.Running {
				t.Errorf("at midnight: Month/AsOf/Running = %q/%q/%v, want 2026-10/2026-10-01/true", after.Month, after.AsOf, after.Running)
			}
			if b, err := l.Balances(ctx, f.fundID); err != nil || b.MonthIn != 0 {
				t.Errorf("Balances().MonthIn at midnight = %d (%v), want 0: October has no income yet", b.MonthIn, err)
			}
			if err := postOn1Oct(); err != nil {
				t.Errorf("posting on 1 October at midnight = %v, want no error", err)
			}
		})
	}
}

func TestTheClockIsTheLedgersAndDefaultsToTheWall(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, time.October, 1, 3, 0, 0, 0, time.UTC)
	if got := New(newTestDB(t), WithClock(func() time.Time { return at })).Now(); !got.Equal(at) {
		t.Errorf("Now() with WithClock = %v, want %v", got, at)
	}

	before := time.Now()
	got := newTestLedger(t).Now()
	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("Now() by default = %v, want the wall clock", got)
	}
}
