package demo

import (
	"time"

	"github.com/kerti/uruni/internal/tz"
)

// The demo's history is told in months counted back from the current one:
// month 0 is "this month", -1 the month before it, down to firstMonth. Nothing
// in the story names a calendar month, so a reset in any month tells the same
// story (ADR-045 decision 5).
const (
	firstMonth = -13 // M-13: the fund is set up
	thisMonth  = 0   // M0: the current month, compressed into its 1st through today

	// monthEnd is the day number of an entry that belongs at the end of its
	// month. Asking for the 31st is deliberate: it clamps to the month's own last
	// day, in every month, which is the clamping the seeder's test exercises.
	monthEnd = 31
)

// calendar turns "month offset M, day D" into a real date and instant in the
// fund's zone. It is the one place the story meets the clock.
//
// Day-level entries sit on a fixed day of their month, clamped to the month's
// length. In M0 a day is also capped at today's, so the month's entries are
// compressed into the 1st through today and nothing is dated after today. Both
// clamps are monotone (a later day never maps before an earlier one), so the
// order the story is written in survives them.
type calendar struct {
	loc      *time.Location
	now      time.Time
	thisYear int
	thisMon  time.Month
	todayDay int
}

// newCalendar reads now in the demo fund's zone (WIB), the zone its dates and
// months are read in (ADR-041).
func newCalendar(now time.Time) (calendar, error) {
	loc, err := tz.Load(tz.Jakarta)
	if err != nil {
		return calendar{}, err
	}
	local := now.In(loc)
	return calendar{loc: loc, now: now, thisYear: local.Year(), thisMon: local.Month(), todayDay: local.Day()}, nil
}

// first is the first day of month offset.
func (c calendar) first(offset int) time.Time {
	return time.Date(c.thisYear, c.thisMon, 1, 0, 0, 0, 0, c.loc).AddDate(0, offset, 0)
}

// period is the "YYYY-MM" dues period of month offset.
func (c calendar) period(offset int) string { return c.first(offset).Format("2006-01") }

// dayOf is the day number an entry at (offset, day) really gets: day clamped to
// the month's length, and in M0 to today.
func (c calendar) dayOf(offset, day int) int {
	first := c.first(offset)
	daysIn := first.AddDate(0, 1, -1).Day()
	day = min(day, daysIn)
	if offset == thisMonth {
		day = min(day, c.todayDay)
	}
	return day
}

// date is the "YYYY-MM-DD" of (offset, day).
func (c calendar) date(offset, day int) string {
	return c.first(offset).AddDate(0, 0, c.dayOf(offset, day)-1).Format(time.DateOnly)
}

// today is the "YYYY-MM-DD" of today in the fund's zone.
func (c calendar) today() string { return c.date(thisMonth, monthEnd) }

// instant is the moment the ledger's clock stands on while (offset, day) is
// written: midday in the fund's zone, except on today itself, where it is the
// real now (an entry stamped later than the boot that wrote it would be a lie
// about when it was made).
func (c calendar) instant(offset, day int) time.Time {
	d := c.first(offset).AddDate(0, 0, c.dayOf(offset, day)-1)
	if d.Format(time.DateOnly) == c.today() {
		return c.now
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, c.loc)
}
