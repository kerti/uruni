package demo

import (
	"testing"
	"time"
)

func wib(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("LoadLocation() = %v", err)
	}
	return loc
}

// Day-level entries sit on a fixed day of their month, clamped to the month's
// length, and in M0 to today. The month's end (day 31) lands on the real last
// day of every month, leap February included.
func TestCalendarClampsDaysToTheMonthAndToToday(t *testing.T) {
	t.Parallel()
	loc := wib(t)
	cases := []struct {
		name   string
		now    time.Time
		month  int
		day    int
		want   string
		wantOn string // the date today, as the calendar reads it
	}{
		{"month end of a 30-day month", time.Date(2026, 10, 26, 10, 0, 0, 0, loc), -1, monthEnd, "2026-09-30", "2026-10-26"},
		{"month end of a 31-day month", time.Date(2026, 10, 26, 10, 0, 0, 0, loc), -2, monthEnd, "2026-08-31", "2026-10-26"},
		{"month end of February", time.Date(2027, 3, 15, 10, 0, 0, 0, loc), -1, monthEnd, "2027-02-28", "2027-03-15"},
		{"month end of a leap February", time.Date(2028, 3, 15, 10, 0, 0, 0, loc), -1, monthEnd, "2028-02-29", "2028-03-15"},
		{"M0 is capped at today", time.Date(2026, 10, 9, 10, 0, 0, 0, loc), thisMonth, 20, "2026-10-09", "2026-10-09"},
		{"M0 before today is kept", time.Date(2026, 10, 9, 10, 0, 0, 0, loc), thisMonth, 4, "2026-10-04", "2026-10-09"},
		{"M0 on the 1st is the 1st", time.Date(2026, 10, 1, 0, 5, 0, 0, loc), thisMonth, 25, "2026-10-01", "2026-10-01"},
		{"the first month crosses the year", time.Date(2026, 1, 1, 0, 5, 0, 0, loc), firstMonth, 1, "2024-12-01", "2026-01-01"},
		{"29 February is today", time.Date(2028, 2, 29, 23, 59, 0, 0, loc), thisMonth, monthEnd, "2028-02-29", "2028-02-29"},
		{"31 December is today", time.Date(2026, 12, 31, 12, 0, 0, 0, loc), thisMonth, monthEnd, "2026-12-31", "2026-12-31"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cal, err := newCalendar(tc.now)
			if err != nil {
				t.Fatalf("newCalendar() = %v", err)
			}
			if got := cal.date(tc.month, tc.day); got != tc.want {
				t.Errorf("date(%d, %d) = %s, want %s", tc.month, tc.day, got, tc.want)
			}
			if got := cal.today(); got != tc.wantOn {
				t.Errorf("today() = %s, want %s", got, tc.wantOn)
			}
		})
	}
}

// The calendar reads now in the fund's zone, not the server's: 00:30 on 1 January
// in Jakarta is still 31 December in UTC.
func TestCalendarReadsTodayInTheFundsZone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 30, 0, 0, wib(t)).UTC()
	if now.Day() != 31 {
		t.Fatalf("the fixture instant is %v in UTC, want the 31st (the point of the test)", now)
	}
	cal, err := newCalendar(now)
	if err != nil {
		t.Fatalf("newCalendar() = %v", err)
	}
	if got := cal.today(); got != "2026-01-01" {
		t.Errorf("today() = %s, want 2026-01-01", got)
	}
	if got := cal.period(thisMonth); got != "2026-01" {
		t.Errorf("period(M0) = %s, want 2026-01", got)
	}
	if got := cal.period(firstMonth); got != "2024-12" {
		t.Errorf("period(M-13) = %s, want 2024-12", got)
	}
}

// An entry on a past day is stamped midday in the fund's zone, and one on today
// the real now, so nothing is stamped later than the boot that wrote it.
func TestCalendarInstants(t *testing.T) {
	t.Parallel()
	loc := wib(t)
	now := time.Date(2026, 10, 9, 10, 17, 0, 0, loc)
	cal, err := newCalendar(now)
	if err != nil {
		t.Fatalf("newCalendar() = %v", err)
	}
	if got, want := cal.instant(-1, 5), time.Date(2026, 9, 5, 12, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("instant(-1, 5) = %v, want %v", got, want)
	}
	if got := cal.instant(thisMonth, 20); !got.Equal(now) {
		t.Errorf("instant(M0, 20) on the 9th = %v, want the real now %v", got, now)
	}
	if got, want := cal.instant(thisMonth, 3), time.Date(2026, 10, 3, 12, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("instant(M0, 3) = %v, want %v", got, want)
	}
}
