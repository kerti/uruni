// Package money is the one type every rupiah figure in Uruni passes through,
// so the unit lives in the type: an Amount is integer rupiah, never a float,
// never a count of something else (ADR-006, ADR-026).
//
// The surface is small. Amount is a defined integer type, so +, -, unary
// minus and the six comparisons already work between two Amounts. What the
// native operators get wrong is silent wraparound at the int64 boundary, which
// a trust core cannot tolerate: a wrapped balance is a confidently wrong
// number on the calmest screen. So Add, Sub and Mul are the checked versions,
// and FormatIDR (format.go) is the server-side twin of the SPA's formatter,
// for the one renderer without JavaScript: the public report (ADR-035).
//
// There is no Float64, and there will not be one. There is no Parse: the only
// place a human types a figure is the SPA. FormatIDR keeps the scale in one
// named place (see ADR-006's exit plan).
package money

import (
	"errors"
	"fmt"
	"math"
)

// ErrOverflow reports that an operation's true result cannot be represented in
// an int64. Every returned overflow error wraps it, so callers match with
// errors.Is.
//
// At Uruni's scale this is unreachable: a fund moving Rp 1-2M/month would
// outlast recorded history before nearing the int64 ceiling, and SQLite's
// SUM() raises an error before a query hands us a total near it. It is
// checked anyway, as the schema puts a CHECK on every enum Go alone writes.
var ErrOverflow = errors.New("money: result overflows int64")

// MaxAmount is the largest single figure Uruni accepts for any amount a person
// types or sends: Rp 999.999.999.999, twelve digits, inclusive (#499).
//
// Nothing below int64 bounds a ledger on its own. SQLite raises an
// integer-overflow error when an intermediate SUM passes int64, so two very
// large entries would break every balance for good, and immutable entries
// cannot be offset to repair it. The ceiling keeps every derived sum far
// inside int64: reaching it needs about 9.2 million entries at the ceiling
// (2^63 / MaxAmount). Twelve digits is also below 2^53, so the SPA's JS
// number carries the figure exactly (rule 1 at the edge). Rp 1 trillion is
// far beyond a community fund; the ceiling exists to make a slipped key
// harmless, not to model a real balance.
const MaxAmount Amount = 999_999_999_999

// Amount is a quantity of integer rupiah. The zero value is Rp 0, an ordinary
// balance (an empty fund, an account netting to zero), not a sentinel for
// "unset".
type Amount int64

// Add returns a + b, or ErrOverflow if the true sum is not representable.
func (a Amount) Add(b Amount) (Amount, error) {
	sum := a + b
	// A sum that moved the wrong way wrapped: adding a positive cannot decrease
	// the total, and adding a negative cannot increase it.
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, fmt.Errorf("%w: %d + %d", ErrOverflow, a, b)
	}
	return sum, nil
}

// Sub returns a - b, or ErrOverflow if the true difference is not
// representable.
//
// This is not Add(-b), and the difference is load-bearing: negating
// math.MinInt64 wraps to itself, so an Add-based version would return a
// confidently wrong answer for the one subtrahend it cannot negate.
func (a Amount) Sub(b Amount) (Amount, error) {
	diff := a - b
	// Mirror of Add: subtracting a negative cannot decrease the total, and
	// subtracting a positive cannot increase it.
	if (b < 0 && diff < a) || (b > 0 && diff > a) {
		return 0, fmt.Errorf("%w: %d - %d", ErrOverflow, a, b)
	}
	return diff, nil
}

// Mul returns a repeated n times, or ErrOverflow if the true product is not
// representable.
//
// n is a plain int64 count, not an Amount: money times money is meaningless.
// The domain's only multiplication is a dues rate times a number of periods.
func (a Amount) Mul(n int64) (Amount, error) {
	if a == 0 || n == 0 {
		return 0, nil
	}
	product := int64(a) * n
	// The division check catches every wraparound except where the check itself
	// wraps: math.MinInt64 / -1 is math.MinInt64 in Go, which would agree with a
	// and let the overflow through.
	if product/n != int64(a) || (int64(a) == math.MinInt64 && n == -1) {
		return 0, fmt.Errorf("%w: %d * %d", ErrOverflow, a, n)
	}
	return Amount(product), nil
}

// AboveCeiling reports whether a is larger than MaxAmount. It says nothing
// about a negative a: sign is a separate check each caller makes (a payment
// must be positive, a count not negative), and a negative amount is an
// invalid argument, not a too-large one.
func (a Amount) AboveCeiling() bool { return a > MaxAmount }

// FromDB converts a value read from the database into an Amount.
//
// This pair, not a sql.Scanner/driver.Valuer, is the sqlc boundary (ADR-026):
// many amounts M3 reads are computed, aliased columns sqlc override rules
// cannot reach, so one explicit conversion covers schema and computed columns
// alike. internal/store stays what sqlc generates.
func FromDB(v int64) Amount { return Amount(v) }

// Int64 converts an Amount back for writing to the database.
func (a Amount) Int64() int64 { return int64(a) }
