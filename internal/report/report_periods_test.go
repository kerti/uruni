package report

import (
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
)

// TestPeriodsText holds the one formatter behind a multi-month payment's row
// (ADR-048): three-letter months, consecutive months as a hyphened range that
// may cross the new year, runs separated by ", ", the year after a run when the
// next starts in another year. The expected strings are the approved copy with
// PeriodHyphen written as "-"; web/src/lib/dates.ts (formatPeriods) prints the
// same text in the app.
func TestPeriodsText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		periods []string
		want    string
	}{
		{"nothing", nil, ""},
		{"one month keeps its long name", []string{"2026-10"}, "Oktober 2026"},
		{"one month repeated", []string{"2026-10", "2026-10"}, "Oktober 2026"},
		{"two consecutive months", []string{"2026-05", "2026-06"}, "Mei-Jun 2026"},
		{"a run and a lone month", []string{"2026-05", "2026-06", "2026-10"}, "Mei-Jun, Okt 2026"},
		{"a run longer than two", []string{"2026-01", "2026-02", "2026-03", "2026-04"}, "Jan-Apr 2026"},
		{"the whole year", []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09", "2026-10", "2026-11", "2026-12"}, "Jan-Des 2026"},
		{"non-contiguous months", []string{"2026-01", "2026-03"}, "Jan, Mar 2026"},
		{"three separate months", []string{"2026-02", "2026-04", "2026-06"}, "Feb, Apr, Jun 2026"},
		{"a range crosses the new year", []string{"2025-11", "2025-12", "2026-01"}, "Nov 2025-Jan 2026"},
		{"twelve months ahead", []string{"2026-12", "2027-01", "2027-02", "2027-03", "2027-04", "2027-05", "2027-06", "2027-07", "2027-08", "2027-09", "2027-10", "2027-11"}, "Des 2026-Nov 2027"},
		{"arrears and months ahead", []string{"2026-08", "2026-10", "2026-11", "2026-12", "2027-01", "2027-07"}, "Agu, Okt 2026-Jan, Jul 2027"},
		{"a lone month, then a range across the year", []string{"2026-08", "2026-10", "2026-11", "2026-12", "2027-01"}, "Agu, Okt 2026-Jan 2027"},
		{"December and January only", []string{"2026-12", "2027-01"}, "Des 2026-Jan 2027"},
		{"two years in one range", []string{"2025-11", "2025-12", "2026-01", "2026-02"}, "Nov 2025-Feb 2026"},
		{"a range in each of two years", []string{"2025-10", "2025-11", "2026-02", "2026-03"}, "Okt-Nov 2025, Feb-Mar 2026"},
		{"December then a lone later month", []string{"2025-12", "2026-03"}, "Des 2025, Mar 2026"},
		{"a year skipped", []string{"2024-12", "2026-01"}, "Des 2024, Jan 2026"},
		{"any order", []string{"2026-10", "2026-05", "2026-06"}, "Mei-Jun, Okt 2026"},
		{"every short month name", []string{"2026-02", "2026-04", "2026-07", "2026-08", "2026-09"}, "Feb, Apr, Jul-Sep 2026"},
		{"not a month comes back as given", []string{"2026-13", "2026-01"}, "2026-13, 2026-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := strings.ReplaceAll(Text.periodsText(tt.periods), PeriodHyphen, "-"); got != tt.want {
				t.Errorf("periodsText(%v) = %q, want %q", tt.periods, got, tt.want)
			}
		})
	}
}

// TestGroupedDuesRowLabels: a payment's row says all its months in today's
// wording, a reversal group's in today's reversal wording, and a single month
// reads exactly as it did before groups.
func TestGroupedDuesRowLabels(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	tests := []struct {
		name  string
		entry ledger.ReportEntry
		want  string
	}{
		{"single month unchanged", ledger.ReportEntry{Kind: "dues", MemberName: str("Ani"), DuesPeriod: str("2026-06"), DuesPeriods: []string{"2026-06"}}, "Juni 2026 \u00b7 Ani"},
		{"several months", ledger.ReportEntry{Kind: "dues", MemberName: str("Ani"), DuesPeriod: str("2026-05"), DuesPeriods: []string{"2026-05", "2026-06", "2026-10"}}, "Mei-Jun, Okt 2026 \u00b7 Ani"},
		{"across a year", ledger.ReportEntry{Kind: "dues", MemberName: str("Ani"), DuesPeriod: str("2025-11"), DuesPeriods: []string{"2025-11", "2025-12", "2026-01"}}, "Nov 2025-Jan 2026 \u00b7 Ani"},
		{"reversal of several", ledger.ReportEntry{Kind: "adjustment", IsReversal: true, MemberName: str("Ani"), DuesPeriod: str("2026-01"), DuesPeriods: []string{"2026-01", "2026-03"}}, "Pembatalan \u00b7 Jan, Mar 2026 \u00b7 Ani"},
		{"reversal of one", ledger.ReportEntry{Kind: "adjustment", IsReversal: true, MemberName: str("Ani"), DuesPeriod: str("2026-06"), DuesPeriods: []string{"2026-06"}}, "Pembatalan \u00b7 Juni 2026 \u00b7 Ani"},
	}
	for _, tt := range tests {
		if got := strings.ReplaceAll(Text.entryLabel(tt.entry), PeriodHyphen, "-"); got != tt.want {
			t.Errorf("%s: label = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestPeriodsTextNeverBreaksInsideARange: a range's hyphen is U+2011, so a
// narrow column wraps between runs, never between "Jan-" and "Jul 2027".
func TestPeriodsTextNeverBreaksInsideARange(t *testing.T) {
	t.Parallel()
	got := Text.periodsText([]string{"2026-05", "2026-06"})
	if want := "Mei" + PeriodHyphen + "Jun 2026"; got != want {
		t.Errorf("periodsText = %q, want %q", got, want)
	}
	if strings.Contains(got, "-") {
		t.Errorf("periodsText = %q carries a breakable ASCII hyphen", got)
	}
}
