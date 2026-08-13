// Package money is the one type every rupiah figure in Uruni passes through.
// It exists so the unit lives in the type rather than in a comment: an Amount
// is integer rupiah, never a float, never a count of something else (ADR-006,
// ADR-026).
//
// The surface is deliberately three methods wide. Amount is a defined integer
// type, so +, -, unary minus and all six comparisons already work correctly
// between two Amounts with no help from this package. The only thing the native
// operators get wrong is silent wraparound at the int64 boundary, and that is
// the one thing a trust core cannot tolerate — a wrapped balance is not an
// error, it is a confidently wrong number on the calmest screen in the app. So
// Add, Sub and Mul are the checked versions of operators that already exist,
// and nothing else is here.
//
// There is no Float64, and there will not be one. There is no Parse or Format
// either: the SPA formats client-side via Intl.NumberFormat (ADR-006), so the
// first real caller for either is M7's server-rendered report and M8's Excel
// export. When Format arrives it takes the currency from the fund and keeps the
// scale in one named constant — see ADR-006's exit plan for why that matters
// more than it looks.
package money

import (
	"errors"
	"fmt"
	"math"
)

// ErrOverflow reports that an operation's true result cannot be represented in
// an int64. Every returned overflow error wraps it, so callers match with
// errors.Is rather than by string.
//
// At Uruni's scale this is unreachable: a fund moving Rp 1-2M/month would need
// longer than recorded history to approach the int64 ceiling, and SQLite's own
// SUM() raises an error before a query could hand us a total anywhere near it.
// It is checked anyway for the same reason the schema puts a CHECK on every
// enum the Go code is the only writer of.
var ErrOverflow = errors.New("money: result overflows int64")

// Amount is a quantity of integer rupiah. The zero value is Rp 0 — an ordinary
// balance (an empty fund, an account that nets to zero), not a sentinel for
// "unset". Nothing in the schema or the PRD needs to distinguish the two.
type Amount int64

// Add returns a + b, or ErrOverflow if the true sum is not representable.
func (a Amount) Add(b Amount) (Amount, error) {
	sum := a + b
	// A sum that moved the wrong way is the wraparound: adding a positive
	// cannot decrease the total, and adding a negative cannot increase it.
	if (b > 0 && sum < a) || (b < 0 && sum > a) {
		return 0, fmt.Errorf("%w: %d + %d", ErrOverflow, a, b)
	}
	return sum, nil
}

// Sub returns a - b, or ErrOverflow if the true difference is not
// representable.
//
// This is not Add(-b), and the difference is load-bearing: negating
// math.MinInt64 wraps back to itself, so an Add-based implementation would
// return a confidently wrong answer for the one subtrahend it cannot negate.
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
// n is a plain int64 count, not an Amount, because multiplying money by money
// is meaningless here. The only multiplication in the domain is a dues rate
// times a number of periods.
func (a Amount) Mul(n int64) (Amount, error) {
	if a == 0 || n == 0 {
		return 0, nil
	}
	product := int64(a) * n
	// The division check catches every wraparound except the one case where the
	// check itself wraps: math.MinInt64 / -1 is defined in Go as math.MinInt64,
	// so it would agree with a and let the overflow through.
	if product/n != int64(a) || (int64(a) == math.MinInt64 && n == -1) {
		return 0, fmt.Errorf("%w: %d * %d", ErrOverflow, a, n)
	}
	return Amount(product), nil
}

// FromDB converts a value read from the database into an Amount.
//
// This pair, and not a sql.Scanner/driver.Valuer implementation, is the sqlc
// boundary (ADR-026): most of the amounts M3 reads are computed, aliased
// columns that sqlc override rules cannot reach, so one explicit conversion
// covers schema and computed columns uniformly. internal/store stays exactly
// what sqlc generates.
func FromDB(v int64) Amount { return Amount(v) }

// Int64 converts an Amount back for writing to the database.
func (a Amount) Int64() int64 { return int64(a) }
