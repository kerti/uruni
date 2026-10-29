package money

import "strconv"

// FormatIDR renders an Amount as the SPA's formatIDR (web/src/lib/money.ts)
// does: "Rp" against the digits with no space, the digits grouped in threes
// with a dot, and a negative amount's minus in front of "Rp" ("-Rp120.000").
// That is the EYD form (#724); the SPA builds it by hand rather than through
// Intl's currency style, whose spacing differs between browser engines. The
// report (ADR-035) renders server-side with no JavaScript, so this is the
// second copy of that format; the golden test beside it pins the two together.
//
// It formats the decimal text and splits the sign off it, rather than negating
// the amount, so math.MinInt64, which cannot be negated as an int64, formats
// correctly instead of wrapping.
func FormatIDR(a Amount) string {
	digits := strconv.FormatInt(int64(a), 10)
	sign := ""
	if digits[0] == '-' {
		sign, digits = "-", digits[1:]
	}

	// Group from the right, three at a time.
	out := make([]byte, 0, len(digits)+len(digits)/3)
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, digits[i])
	}

	return sign + "Rp" + string(out)
}
