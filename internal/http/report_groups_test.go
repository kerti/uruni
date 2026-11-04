package http

import (
	"slices"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/report"
)

// By kind, the default: Uang masuk holds the three gifts, Uang keluar the
// spend, Lainnya the two moves. The two subtotals are the walk's Total masuk
// and Total keluar, so the groups and the totals cannot disagree (ADR-038).
func TestReportGroupsByKindWithSubtotalsThatAreTheWalksTotals(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	body := s.get(t, s.base).Body.String()

	totals := totalsOf(t, body)
	for _, g := range []struct {
		id, heading, subtotal string
		rows                  int
	}{
		{"in", report.Text.DirectionIn, money.FormatIDR(240_000), 3},
		{"out", report.Text.DirectionOut, money.FormatIDR(60_000), 1},
		{"other", report.Text.GroupOther, "", 2},
	} {
		sec := groupSection(t, body, g.id)
		if sec == "" {
			t.Fatalf("no %q group:\n%s", g.id, body)
		}
		if !strings.Contains(sec, `<span>`+g.heading+`</span>`) {
			t.Errorf("group %q is not headed %q:\n%s", g.id, g.heading, sec)
		}
		if n := strings.Count(sec, "<li>"); n != g.rows {
			t.Errorf("group %q rows = %d, want %d:\n%s", g.id, n, g.rows, sec)
		}
		if g.subtotal != "" && !strings.Contains(sec, `<span class="tabular">`+g.subtotal+`</span></h3>`) {
			t.Errorf("group %q subtotal is not %s:\n%s", g.id, g.subtotal, sec)
		}
		if g.subtotal == "" && strings.Contains(sec, `<span class="tabular">Rp`) {
			t.Errorf("Lainnya carries a subtotal; it mixes signs:\n%s", sec)
		}
	}
	// The subtotals are the walk's lines, to the rupiah.
	want := map[string]string{report.Text.TotalIn: money.FormatIDR(240_000), report.Text.TotalOut: money.FormatIDR(60_000)}
	for _, l := range totals {
		if v, ok := want[l.label]; ok && v != l.value {
			t.Errorf("%s = %s, want the group's subtotal %s", l.label, l.value, v)
		}
	}
	if !strings.Contains(groupSection(t, body, "out"), report.Text.DirectionOut+" </span>"+money.FormatIDR(60_000)) {
		t.Error("the spend is not under Uang keluar")
	}
	if !strings.Contains(groupSection(t, body, "other"), "Dipindah: Kas Utama \u2192 Duka") {
		t.Error("a move is not under Lainnya")
	}
}

// "Nothing went out" is an answer: an empty Uang keluar stays, saying so,
// and Lainnya with nothing in it is left out.
func TestReportByKindKeepsAnEmptySideAndDropsAnEmptyLainnya(t *testing.T) {
	t.Parallel()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 100_000, "2026-08-10")
	body := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-08").Body.String()

	out := groupSection(t, body, "out")
	if !strings.Contains(out, `<p class="empty">`+report.Text.NoRowsOut+`</p>`) || strings.Contains(out, "<li>") {
		t.Errorf("an empty Uang keluar does not say %q:\n%s", report.Text.NoRowsOut, out)
	}
	if !strings.Contains(out, money.FormatIDR(0)) {
		t.Errorf("an empty Uang keluar's subtotal is not Rp0:\n%s", out)
	}
	if groupSection(t, body, "other") != "" {
		t.Error("an empty Lainnya is shown")
	}
}

// By month: one group per month, headed by its name, every row of it in the
// ledger's order, no subtotal and no Lainnya. An unknown grouping is by kind.
func TestReportGroupsByMonth(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	body := s.get(t, s.base+"&group=month").Body.String()

	sec := groupSection(t, body, "2026-09")
	if !strings.Contains(sec, `<span>September 2026</span></h3>`) {
		t.Errorf("the month group is not headed September 2026:\n%s", sec)
	}
	if n := strings.Count(sec, "<li>"); n != 6 {
		t.Errorf("month group rows = %d, want all 6:\n%s", n, sec)
	}
	for _, id := range []string{"in", "out", "other"} {
		if groupSection(t, body, id) != "" {
			t.Errorf("a by-month page carries the %q group", id)
		}
	}
	if strings.Contains(sec, `<span class="tabular">Rp`) {
		t.Errorf("a month group carries a subtotal:\n%s", sec)
	}
	// The same rows either way; only their blocks differ.
	rowsOf := func(section string) []string {
		items := strings.Split(section, "<li>")[1:]
		for i, it := range items {
			items[i] = it[:strings.Index(it, "</li>")]
		}
		slices.Sort(items)
		return items
	}
	if !slices.Equal(rowsOf(txnSection(t, body)), rowsOf(txnSection(t, s.get(t, s.base).Body.String()))) {
		t.Error("grouping changed which rows are listed")
	}

	unknown := s.get(t, s.base+"&group=sideways").Body.String()
	if groupSection(t, unknown, "in") == "" || groupSection(t, unknown, "2026-09") != "" {
		t.Error("an unknown grouping is not by kind")
	}
}

// The PDF is laid out as the page is: by kind it carries the three headings
// and the empty line, by month the month's name.
func TestReportPDFFollowsTheGrouping(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	base := "/report/" + s.fund.ReportSlug + "/pdf?month=2026-09"

	byKind := strings.Join(pdfText(t, s.get(t, base).Body.Bytes()), "\n")
	for _, want := range []string{report.Text.DirectionIn, report.Text.DirectionOut, report.Text.GroupOther} {
		if !strings.Contains(byKind, want) {
			t.Errorf("the by-kind PDF has no %q heading", want)
		}
	}
	byMonth := strings.Join(pdfText(t, s.get(t, base+"&group=month").Body.Bytes()), "\n")
	if strings.Contains(byMonth, report.Text.GroupOther) {
		t.Error("the by-month PDF carries Lainnya")
	}
	if !strings.Contains(byMonth, "September 2026") {
		t.Error("the by-month PDF has no month heading")
	}
}
