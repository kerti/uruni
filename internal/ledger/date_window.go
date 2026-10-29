package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// The plausible date window (#632). Every business date and period a caller
// supplies has to sit inside it: a static floor, the year 2000, which the
// schema also enforces as a CHECK on every such column (a restore enforces only
// that half), and a ceiling that moves with the clock and is read in the fund's
// time zone (ADR-041). Both ends are inclusive and there is no grace day.
//
// The window exists for the slipped thumb and the typed year (a payment dated
// 2062, a join date of 0202), not as an accounting rule: a date inside it is
// the treasurer's to choose.

const (
	// minDay and minMonth are the floor. The schema's CHECKs carry the same two
	// literals; internal/db's TestDateFloorLiteralsAreTheFloor pins them to
	// these.
	minDay   = "2000-01-01"
	minMonth = "2000-01"

	// duesMonthsAhead is how far past the current month a dues payment may name a
	// period: the dues form offers exactly this many (a year is the longest anyone
	// pays ahead).
	duesMonthsAhead = 12
	// rateMonthsAhead is how far ahead a rate's start month may be: a rise agreed
	// in advance. A pending rate is shown on the Kategori screen, which is why it
	// may reach further than a tier start (ADR-040).
	rateMonthsAhead = 24
)

// ErrDateOutOfRange is returned by every write that takes a date or a month
// outside the plausible window. The message names the field and the bound.
//
// It does not wrap ErrInvalidArgument: the date is well-formed, and the
// treasurer needs the one thing to change (the year she mistyped).
//
// Only a date the caller supplies is checked against a ceiling. A date the
// ledger inherits (ADR-033's correction pair takes the corrected row's own
// occurred_on) is not, so a correction is never refused for a date she cannot
// edit (ADR-041); the floor still applies, as the schema's CHECK. A reversal
// supplies no date (ADR-042) and checks neither.
var ErrDateOutOfRange = refuse("ledger: date is outside the plausible window",
	ClassInvalid, "date_out_of_range",
	"The date is outside the range Uruni accepts.")

// noCeiling is the ceiling for a check that wants the shape and the floor only:
// the largest date the strict layout can write.
const (
	noCeiling      = "9999-12-31"
	noCeilingMonth = "9999-12"
)

// dateWindow is the ceilings of one fund at one instant, in the fund's zone.
type dateWindow struct {
	// today is the fund's calendar day, "YYYY-MM-DD".
	today string
}

// windowAt reads the instant in loc, the fund's zone: the one place a ceiling's
// "today" comes from (ADR-041). now is the ledger's clock, read by the caller.
func windowAt(now time.Time, loc *time.Location) dateWindow {
	return dateWindow{today: now.In(loc).Format(time.DateOnly)}
}

// FundToday is the fund's calendar day at now, "YYYY-MM-DD", read in its zone
// (ADR-041). It is for a read that needs the same "today" a write would, such as
// the page's reversible flag (ADR-042).
func FundToday(f store.Fund, now time.Time) (string, error) {
	loc, err := FundLocation(f)
	if err != nil {
		return "", err
	}
	return windowAt(now, loc).today, nil
}

// windowFor reads the fund's zone from its row, through q, and "today" from the
// ledger's clock. A caller outside a transaction passes l.q; one inside passes
// the transaction's querier.
func (l *Ledger) windowFor(ctx context.Context, q store.Querier, fundID int64) (dateWindow, error) {
	fund, err := q.GetFund(ctx, fundID)
	if err != nil {
		return dateWindow{}, fmt.Errorf("fetching fund: %w", err)
	}
	loc, err := FundLocation(fund)
	if err != nil {
		return dateWindow{}, err
	}
	return windowAt(l.now(), loc), nil
}

// entry is the ceiling of a date money moved on: a transaction, a claim, a
// settlement, a reversal, a reconciliation fix, an envelope opening or closing.
// It already happened, so at most today.
func (w dateWindow) entry() string { return w.today }

// yearAhead is the ceiling of a member's join or leaving date, and of a location's
// retirement: at most today plus a year, the same day next year, or the month's
// last day when that day does not exist (29 February reads as 28 February). The
// join date reaches ahead for someone who has said they are joining.
func (w dateWindow) yearAhead() string {
	t, err := time.Parse(time.DateOnly, w.today)
	if err != nil {
		return w.today // unreachable: today was formatted by time.DateOnly
	}
	next := t.AddDate(1, 0, 0)
	if next.Day() != t.Day() {
		next = time.Date(t.Year()+1, t.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	}
	return next.Format(time.DateOnly)
}

// month is the fund's current month, "YYYY-MM".
func (w dateWindow) month() string { return w.today[:len(duesPeriodLayout)] }

// dues is the ceiling of a dues payment's period: the current month plus
// duesMonthsAhead.
func (w dateWindow) dues() string { return addMonths(w.month(), duesMonthsAhead) }

// rate is the ceiling of a rate's start month: the current month plus
// rateMonthsAhead.
func (w dateWindow) rate() string { return addMonths(w.month(), rateMonthsAhead) }

// addMonths is the "YYYY-MM" n months after month (n may be negative), by
// calendar arithmetic on the first of the month, which never overflows.
func addMonths(month string, n int) string {
	t, err := time.Parse(duesPeriodLayout, month)
	if err != nil {
		return month // callers pass a month they formatted themselves
	}
	return t.AddDate(0, n, 0).Format(duesPeriodLayout)
}

// validateOccurredOn rejects anything that is not a real YYYY-MM-DD calendar
// date (ErrInvalidArgument), then anything before the floor or after ceiling
// (ErrDateOutOfRange). field is the value's JSON key: the message names it, and
// so does the FieldError (#530).
//
// time.Parse with this strict layout already refuses "2026-02-30"; the
// round-trip comparison is a cheap second defense, not load-bearing today.
// Dates are fixed-width, so the bound comparisons are string comparisons.
func validateOccurredOn(field, s, ceiling string) error {
	t, err := time.Parse(occurredOnLayout, s)
	if err != nil {
		return invalidField(field, "%s %q is not a valid calendar date: %v", field, s, err)
	}
	if t.Format(occurredOnLayout) != s {
		return invalidField(field, "%s %q is not a valid calendar date", field, s)
	}
	if s < minDay {
		return fmt.Errorf("%w: %s is before %s", ErrDateOutOfRange, field, minDay)
	}
	if s > ceiling {
		return fmt.Errorf("%w: %s is after %s", ErrDateOutOfRange, field, ceiling)
	}
	return nil
}

// checkPeriodShape rejects anything that is not a real "YYYY-MM" calendar
// month, wrapping ErrInvalidArgument: time.Parse with this strict layout refuses
// an out-of-range month ("2026-13"), and the round-trip comparison catches a
// short value like "2026-1" that Parse would accept. It is the whole check for a
// READ (the dues status, the arrears count), which has no window: asking about a
// month is not writing a date.
func checkPeriodShape(field, s string) error {
	t, err := time.Parse(duesPeriodLayout, s)
	if err != nil {
		return invalidField(field, "%s %q is not a valid \"YYYY-MM\" period: %v", field, s, err)
	}
	if t.Format(duesPeriodLayout) != s {
		return invalidField(field, "%s %q is not a valid \"YYYY-MM\" period", field, s)
	}
	return nil
}

// validateDuesPeriod is checkPeriodShape plus the window of a WRITE: a month
// before the floor or after ceiling is ErrDateOutOfRange.
func validateDuesPeriod(field, s, ceiling string) error {
	if err := checkPeriodShape(field, s); err != nil {
		return err
	}
	if s < minMonth {
		return fmt.Errorf("%w: %s is before %s", ErrDateOutOfRange, field, minMonth)
	}
	if s > ceiling {
		return fmt.Errorf("%w: %s is after %s", ErrDateOutOfRange, field, ceiling)
	}
	return nil
}
