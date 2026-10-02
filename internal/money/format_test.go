package money

import (
	"math"
	"testing"
)

// TestFormatIDRMatchesIntl pins FormatIDR to the SPA's own formatter
// (web/src/lib/money.ts, ADR-035): the report and the app must never print one
// amount two ways.
//
// The expected strings were captured from Node's Intl, not typed from memory.
// To recapture them (for example after a Node/ICU upgrade changes the format):
//
//	node -e "const f=new Intl.NumberFormat('id-ID',{style:'currency',currency:'IDR',minimumFractionDigits:0}); for (const n of [0,5,500,2000,50000,999999,1000000,4250000,1234567890,-1,-500,-120000,-4250000]) console.log(n, JSON.stringify(f.format(n)), [...f.format(n)].map(c=>c.charCodeAt(0)>127?'\\\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'):c).join(''))"
//
// (captured with Node v22.15.0). The space after "Rp" is U+00A0 and a
// negative amount's sign sits before "Rp". The values are all within
// Number.MAX_SAFE_INTEGER, where Intl is exact; the int64 extremes are checked
// separately below against known digits.
func TestFormatIDRMatchesIntl(t *testing.T) {
	tests := []struct {
		name string
		in   Amount
		want string
	}{
		{"zero", 0, "Rp\xc2\xa00"},
		{"single digit", 5, "Rp\xc2\xa05"},
		{"three digits, no separator", 500, "Rp\xc2\xa0500"},
		{"first thousands separator", 2000, "Rp\xc2\xa02.000"},
		{"five digits", 50000, "Rp\xc2\xa050.000"},
		{"just under a million", 999999, "Rp\xc2\xa0999.999"},
		{"exactly a million", 1000000, "Rp\xc2\xa01.000.000"},
		{"typical monthly total", 4250000, "Rp\xc2\xa04.250.000"},
		{"over a billion", 1234567890, "Rp\xc2\xa01.234.567.890"},
		{"negative one", -1, "-Rp\xc2\xa01"},
		{"negative three digits", -500, "-Rp\xc2\xa0500"},
		{"negative with separator", -120000, "-Rp\xc2\xa0120.000"},
		{"negative million", -4250000, "-Rp\xc2\xa04.250.000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatIDR(tt.in); got != tt.want {
				t.Errorf("FormatIDR(%d) = %q, want %q", int64(tt.in), got, tt.want)
			}
		})
	}
}

// TestFormatIDRInt64Extremes covers the values Intl cannot represent exactly
// (a JS number is a float64). math.MinInt64 is the one int64 that cannot be
// negated, so a sign-flip implementation would print it wrong.
func TestFormatIDRInt64Extremes(t *testing.T) {
	tests := []struct {
		name string
		in   Amount
		want string
	}{
		{"max int64", Amount(math.MaxInt64), "Rp\xc2\xa09.223.372.036.854.775.807"},
		{"min int64", Amount(math.MinInt64), "-Rp\xc2\xa09.223.372.036.854.775.808"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatIDR(tt.in); got != tt.want {
				t.Errorf("FormatIDR(%d) = %q, want %q", int64(tt.in), got, tt.want)
			}
		})
	}
}
