package money

import "strconv"

// currencySymbolSpace is what separates "Rp" from the digits: U+00A0, the
// no-break space (UTF-8 bytes C2 A0), exactly as Intl.NumberFormat('id-ID', {style: 'currency',
// currency: 'IDR'}) writes it. A plain space would let a line break strand
// "Rp" from its figure, and would make the report print an amount differently
// from the app. Written as escaped bytes, never a raw one, so the source stays ASCII.
const currencySymbolSpace = "\xc2\xa0"

// FormatIDR renders an Amount the way the SPA does:
//
//	new Intl.NumberFormat('id-ID', {style: 'currency', currency: 'IDR',
//	    minimumFractionDigits: 0}).format(n)
//
// That is "Rp", U+00A0, then the digits grouped in threes with a dot, and a
// negative amount's minus sign in front of "Rp" ("-Rp 120.000"). The report
// (ADR-035) renders on the server with no JavaScript, so this is the second
// copy of that format; the golden test beside it pins the two together.
//
// It formats the decimal text and splits the sign off it, rather than negating
// the amount, so math.MinInt64 - which cannot be negated as an int64 - formats
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

	return sign + "Rp" + currencySymbolSpace + string(out)
}
