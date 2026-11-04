package report

import (
	"net/url"
	"testing"
)

// readRange is the one rule the page and the PDF share (ADR-049): a range only
// when both days are real, each clamped into [first, today], swapped when they
// then run backwards; anything else is no range.
func TestReadRange(t *testing.T) {
	t.Parallel()
	const first, today = "2026-08-20", "2026-10-02"
	tests := []struct {
		name     string
		query    string
		from, to string
		ok       bool
	}{
		{"as given", "from=2026-09-03&to=2026-09-10", "2026-09-03", "2026-09-10", true},
		{"on the bounds", "from=2026-08-20&to=2026-10-02", "2026-08-20", "2026-10-02", true},
		{"one day", "from=2026-09-03&to=2026-09-03", "2026-09-03", "2026-09-03", true},
		{"from before the first day", "from=2026-08-19&to=2026-09-10", first, "2026-09-10", true},
		{"to after today", "from=2026-09-03&to=2026-10-03", "2026-09-03", today, true},
		{"swapped", "from=2026-09-10&to=2026-09-03", "2026-09-03", "2026-09-10", true},
		{"swapped, then clamped", "from=2027-01-01&to=2020-01-01", first, today, true},
		{"both ahead", "from=2027-01-01&to=2027-01-05", today, today, true},
		{"both behind", "from=2020-01-01&to=2020-01-05", first, first, true},
		{"no query", "", "", "", false},
		{"from alone", "from=2026-09-03", "", "", false},
		{"to alone", "to=2026-09-03", "", "", false},
		{"empty both", "from=&to=", "", "", false},
		{"unpadded", "from=2026-9-3&to=2026-09-10", "", "", false},
		{"not a date", "from=2026-02-30&to=2026-09-10", "", "", false},
		{"a month", "from=2026-09&to=2026-10", "", "", false},
		{"with a time", "from=2026-09-03T10:00&to=2026-09-10", "", "", false},
		{"period alone", "period=range", "", "", false},
		// Without JavaScript the form sends every field: Bulanan beside the old
		// dates is the way back to the month; Rentang tanggal keeps the range.
		{"no-JS Bulanan with dates", "period=&month=2026-09&from=2026-09-03&to=2026-09-10", "", "", false},
		{"no-JS Rentang tanggal", "period=range&month=2026-09&from=2026-09-03&to=2026-09-10", "2026-09-03", "2026-09-10", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			from, to, ok := readRange(q, first, today)
			if from != tt.from || to != tt.to || ok != tt.ok {
				t.Errorf("readRange(%q) = %q, %q, %v; want %q, %q, %v", tt.query, from, to, ok, tt.from, tt.to, tt.ok)
			}
		})
	}
}

// A range is named the same way wherever it appears, and its file is named
// from the two ISO days.
func TestRangeCopy(t *testing.T) {
	t.Parallel()
	if got, want := Text.rangeText("2026-10-15", "2026-11-03"), "15 Oktober 2026 \u2013 3 November 2026"; got != want {
		t.Errorf("rangeText = %q, want %q", got, want)
	}
	if got, want := Text.PDFStatementRange(Text.rangeText("2026-10-15", "2026-11-03")), "Laporan kas 15 Oktober 2026 \u2013 3 November 2026"; got != want {
		t.Errorf("PDFStatementRange = %q, want %q", got, want)
	}
	if got, want := Text.PDFFilenameRange("2026-10-15", "2026-11-03"), "laporan-kas-2026-10-15_2026-11-03.pdf"; got != want {
		t.Errorf("PDFFilenameRange = %q, want %q", got, want)
	}
	if got, want := Text.DuesMonthCaption(Text.monthName("2026-11")), "Iuran bulan November 2026"; got != want {
		t.Errorf("DuesMonthCaption = %q, want %q", got, want)
	}
}

// A one-day range names its day once.
func TestRangeTextOfOneDay(t *testing.T) {
	t.Parallel()
	if got, want := Text.rangeText("2026-11-04", "2026-11-04"), "4 November 2026"; got != want {
		t.Errorf("rangeText(one day) = %q, want %q", got, want)
	}
	if got, want := Text.rangeText("2026-10-15", "2026-11-03"), "15 Oktober 2026 \u2013 3 November 2026"; got != want {
		t.Errorf("rangeText = %q, want %q", got, want)
	}
}
