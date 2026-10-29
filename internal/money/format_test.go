package money

import (
	"math"
	"testing"
)

// TestFormatIDRMatchesTheSPA pins FormatIDR to the SPA's formatIDR
// (web/src/lib/money.ts, whose own test pins the same strings, ADR-035): the
// report and the app must never print one amount two ways. Both write the EYD
// form, "Rp" against the digits with no space (#724), and a negative amount's
// sign before "Rp". The int64 extremes, beyond what a JS number holds exactly,
// are checked separately below.
func TestFormatIDRMatchesTheSPA(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   Amount
		want string
	}{
		{"zero", 0, "Rp0"},
		{"single digit", 5, "Rp5"},
		{"three digits, no separator", 500, "Rp500"},
		{"first thousands separator", 2000, "Rp2.000"},
		{"five digits", 50000, "Rp50.000"},
		{"just under a million", 999999, "Rp999.999"},
		{"exactly a million", 1000000, "Rp1.000.000"},
		{"typical monthly total", 4250000, "Rp4.250.000"},
		{"over a billion", 1234567890, "Rp1.234.567.890"},
		{"negative one", -1, "-Rp1"},
		{"negative three digits", -500, "-Rp500"},
		{"negative with separator", -120000, "-Rp120.000"},
		{"negative million", -4250000, "-Rp4.250.000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatIDR(tt.in); got != tt.want {
				t.Errorf("FormatIDR(%d) = %q, want %q", int64(tt.in), got, tt.want)
			}
		})
	}
}

// TestFormatIDRInt64Extremes covers values Intl cannot represent exactly (a JS
// number is a float64). math.MinInt64 cannot be negated, so a sign-flip
// implementation would print it wrong.
func TestFormatIDRInt64Extremes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   Amount
		want string
	}{
		{"max int64", Amount(math.MaxInt64), "Rp9.223.372.036.854.775.807"},
		{"min int64", Amount(math.MinInt64), "-Rp9.223.372.036.854.775.808"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatIDR(tt.in); got != tt.want {
				t.Errorf("FormatIDR(%d) = %q, want %q", int64(tt.in), got, tt.want)
			}
		})
	}
}
