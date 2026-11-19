package report

import (
	"testing"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
)

// ADR-051's copy: the walk's bridge line and a payout row's second line. The
// words are the maintainer's, approved 2026-11-19.
func TestWalkLinesShowTalanganOnlyWhenNonZeroAndSigned(t *testing.T) {
	t.Parallel()
	walk := ledger.ReportWalk{Full: true, StartOn: "2026-09-30", EndOn: "2026-10-31", Start: 300_000, Out: 450_000, PaidLater: 450_000, End: 300_000}
	labels := func(lines []Total) []string {
		var out []string
		for _, l := range lines {
			out = append(out, l.Label+"="+l.Value)
		}
		return out
	}

	got := labels(walkLines(walk))
	want := []string{"Saldo 30 September 2026=Rp300.000", "Total masuk=Rp0", "Total keluar=-Rp450.000", "Talangan dibayar kemudian=+Rp450.000", "Saldo 31 Oktober 2026=Rp300.000"}
	if len(got) != len(want) {
		t.Fatalf("lines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The other direction has its own words and its own, fixed sign, so a
	// reader never meets one label that is sometimes plus and sometimes minus.
	walk.Out, walk.PaidLater, walk.PaidEarlier, walk.End = 0, 0, 450_000, -150_000
	if got := walkLines(walk)[3]; got.Label != "Pelunasan talangan sebelumnya" || got.Value != "-Rp450.000" {
		t.Errorf("a payout for earlier spending = %+v, want Pelunasan talangan sebelumnya -Rp450.000", got)
	}

	// A period can hold both: each on its own line, later before earlier.
	walk.PaidLater, walk.PaidEarlier = 100_000, 450_000
	lines := walkLines(walk)
	if lines[3].Label != "Talangan dibayar kemudian" || lines[3].Value != "+Rp100.000" ||
		lines[4].Label != "Pelunasan talangan sebelumnya" || lines[4].Value != "-Rp450.000" {
		t.Errorf("both lines = %v, want +100.000 then -450.000", labels(lines))
	}

	walk.PaidLater, walk.PaidEarlier = 0, 0
	for _, l := range walkLines(walk) {
		if l.Label == "Talangan dibayar kemudian" || l.Label == "Pelunasan talangan sebelumnya" {
			t.Errorf("zero talangan lines show one: %+v", l)
		}
	}

	// Under a member or direction filter the walk is not Full: no balance, so
	// nothing to close, whatever the fields hold.
	walk.Full, walk.PaidLater, walk.PaidEarlier = false, 450_000, 450_000
	for _, l := range walkLines(walk) {
		if l.Label == "Talangan dibayar kemudian" || l.Label == "Pelunasan talangan sebelumnya" {
			t.Errorf("a walk that is not full shows a talangan line: %+v", l)
		}
	}
}

func TestPayoutNoteNamesTheOtherDate(t *testing.T) {
	t.Parallel()
	payout := &ledger.ReportPayout{FrontedOn: "2026-10-27", PaidOn: "2026-11-12"}
	for _, tc := range []struct {
		name string
		date string
		p    *ledger.ReportPayout
		want string
	}{
		{"placed on the day it was fronted", "2026-10-27", payout, "Dibayar 12 Nov"},
		{"placed on the day it was paid (a cash view)", "2026-11-12", payout, "Ditalangi 27 Okt"},
		{"fronted and paid on one day has no other date", "2026-11-05", &ledger.ReportPayout{FrontedOn: "2026-11-05", PaidOn: "2026-11-05"}, ""},
		{"no payout, no second line", "2026-10-27", nil, ""},
		{"across a year boundary the year is kept", "2026-12-28", &ledger.ReportPayout{FrontedOn: "2026-12-28", PaidOn: "2027-01-05"}, "Dibayar 5 Jan 2027"},
		{"a single-digit day and Mei", "2026-04-28", &ledger.ReportPayout{FrontedOn: "2026-04-28", PaidOn: "2026-05-03"}, "Dibayar 3 Mei"},
	} {
		if got := Text.payoutNote(tc.date, tc.p); got != tc.want {
			t.Errorf("%s: payoutNote = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The row is dated, listed and grouped by the day it is placed on: by month it
// sits under October, and the second line reaches the page and the PDF.
func TestNewPagePlacesAPayoutOnItsFrontingDayWithASecondLine(t *testing.T) {
	t.Parallel()
	name := "Jane"
	r := ledger.Report{
		FundName: "Kas", AsOf: "2026-10-31", Month: "2026-10", Months: []string{"2026-10"},
		Rows: []ledger.ReportRow{{
			Date: "2026-10-27",
			Entry: &ledger.ReportEntry{
				Line: ledger.ReportLineOut, Kind: ledger.TransactionKindReimbursement, Direction: ledger.ReportDirectionOut,
				Amount: 450_000, PurposeName: "Kas Utama", MemberName: &name,
				Payout: &ledger.ReportPayout{FrontedOn: "2026-10-27", PaidOn: "2026-11-12"},
			},
		}},
		Walk: ledger.ReportWalk{Full: true, StartOn: "2026-09-30", EndOn: "2026-10-31", Start: 300_000, Out: 450_000, PaidLater: 450_000, End: 300_000},
	}
	page := NewPage(r, false)
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(page.Rows))
	}
	row := page.Rows[0]
	if row.Date != "27 Oktober 2026" || row.Elsewhere != "Dibayar 12 Nov" || row.month != "2026-10" {
		t.Errorf("row = date %q, elsewhere %q, month %q; want 27 Oktober 2026, the payment line, 2026-10", row.Date, row.Elsewhere, row.month)
	}

	page.GroupRows(GroupByMonth, r.Walk)
	if len(page.Groups) != 1 || page.Groups[0].Heading != "Oktober 2026" || len(page.Groups[0].Rows) != 1 {
		t.Errorf("by month = %+v, want the payout under Oktober 2026", page.Groups)
	}
	page.GroupRows(GroupByKind, r.Walk)
	if page.Groups[1].ID != "out" || len(page.Groups[1].Rows) != 1 || page.Groups[1].Subtotal != money.FormatIDR(450_000) {
		t.Errorf("by kind = %+v, want the payout under Uang keluar, subtotal 450.000", page.Groups)
	}
	if len(page.Totals) != 5 || page.Totals[3].Label != "Talangan dibayar kemudian" {
		t.Errorf("Totals = %+v, want the Talangan line fourth of five", page.Totals)
	}

	// A cash view's row, sat on its payment day, names the fronting day.
	r.Rows[0].Date = "2026-11-12"
	if got := NewPage(r, false).Rows[0].Elsewhere; got != "Ditalangi 27 Okt" {
		t.Errorf("a row placed on its payment day says %q, want Ditalangi 27 Okt", got)
	}

	// The other rows have none.
	r.Rows[0].Entry.Payout = nil
	if got := NewPage(r, false).Rows[0].Elsewhere; got != "" {
		t.Errorf("a row with no payout says %q, want nothing", got)
	}
}
