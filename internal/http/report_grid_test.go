package http

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/report"
	"github.com/kerti/uruni/internal/store"
)

// gridBlock is the grid's markup alone, "" when the page draws none.
func gridBlock(body string) string { return gridBlockRE.FindString(body) }

func TestReportGridOpensTheDuesSectionAboveItsFilterAndTable(t *testing.T) {
	t.Parallel()
	s := newDuesScenario(t)
	body := s.get(t, s.base).Body.String()
	grid := gridBlock(body)
	if grid == "" {
		t.Fatalf("no grid in the dues section:\n%s", body)
	}
	section := body[strings.Index(body, `id="dues"`):]
	if g, f, tb := strings.Index(section, `class="grid"`), strings.Index(section, `action="#dues"`), strings.Index(section, `<ul class="rows dues">`); g < 0 || g > f || f > tb {
		t.Errorf("order in the dues section: grid at %d, filter at %d, table at %d; want grid first", g, f, tb)
	}
	for _, want := range []string{
		"Status iuran 12 bulan",
		"Januari 2026 \u2013 September 2026", // 2000 joins, the rate begins January 2026
		// The legend, in the four words.
		"Lunas", "Bayar sebagian", "Belum bayar", "Tidak ada iuran",
		// The hidden label of a cell, and of a column head.
		`<span class="sr-only">September 2026: Lunas</span>`,
		`<span class="sr-only">September 2026: Belum bayar</span>`,
		`<span class="sr-only">September 2026: Bayar sebagian</span>`,
		`<span aria-hidden="true">Jan</span><span class="sr-only">Januari 2026</span>`,
		`<span aria-hidden="true">Sep</span><span class="sr-only">September 2026</span>`,
	} {
		if !strings.Contains(grid, want) {
			t.Errorf("grid lacks %q:\n%s", want, grid)
		}
	}
	for _, class := range []string{"g-paid", "g-half", "g-hollow", "g-none"} {
		if !strings.Contains(grid, `class="`+class+`"`) {
			t.Errorf("grid draws no %s shape", class)
		}
	}
	// Status only: no amount and no count.
	if strings.Contains(grid, "Rp") || strings.Contains(strings.ToLower(grid), "tunggakan") {
		t.Errorf("grid carries an amount or an arrears count:\n%s", grid)
	}
}

func TestReportGridIsAbsentUnderTwoMonths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	f.post(t, "in", 10_000, "2026-10-01")
	q := store.New(f.db)
	tier, err := q.CreateDuesTier(ctx, store.CreateDuesTierParams{FundID: f.fund.ID, Name: "Madya", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateDuesTier() = %v", err)
	}
	// The first rate begins in the running month: one month owes, so one column.
	if _, err := q.CreateDuesRate(ctx, store.CreateDuesRateParams{TierID: tier.ID, Amount: 25_000, EffectiveFrom: "2026-10", CreatedAt: 1}); err != nil {
		t.Fatalf("CreateDuesRate() = %v", err)
	}
	m, err := q.CreateMember(ctx, store.CreateMemberParams{FundID: f.fund.ID, Name: "Ani", JoinedOn: "2000-01-01", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateMember() = %v", err)
	}
	if _, err := q.CreateMemberTier(ctx, store.CreateMemberTierParams{FundID: f.fund.ID, MemberID: m.ID, TierID: &tier.ID, EffectiveFrom: "2026-10", CreatedAt: 1}); err != nil {
		t.Fatalf("CreateMemberTier() = %v", err)
	}
	body := f.get(t, "/report/"+f.fund.ReportSlug).Body.String()
	if !strings.Contains(body, `id="dues"`) {
		t.Fatalf("no dues section; the test needs one:\n%s", body)
	}
	if gridBlock(body) != "" || strings.Contains(body, "Status iuran 12 bulan") {
		t.Errorf("a one-month grid was drawn:\n%s", body)
	}
	pdf := f.get(t, "/report/"+f.fund.ReportSlug+"/pdf")
	if pdfHas(pdfText(t, pdf.Body.Bytes()), report.Text.GridHeading) {
		t.Error("the PDF drew a one-month grid")
	}
}

func TestReportGridIgnoresEveryFilter(t *testing.T) {
	t.Parallel()
	s := newDuesScenario(t)
	want := gridBlock(s.get(t, s.base).Body.String())
	if want == "" {
		t.Fatal("no grid on the unfiltered page")
	}
	members, err := store.New(s.db).ListMembersByFund(context.Background(), s.fund.ID)
	if err != nil {
		t.Fatalf("ListMembersByFund() = %v", err)
	}
	for _, query := range []string{
		"&dues=unpaid", "&dues=partial", "&dues=paid",
		fmt.Sprintf("&member=%d", members[0].ID), "&dir=out", fmt.Sprintf("&purpose=%d", s.mainID),
		fmt.Sprintf("&dues=unpaid&member=%d&dir=in", members[1].ID),
	} {
		if got := gridBlock(s.get(t, s.base+query).Body.String()); got != want {
			t.Errorf("grid under %q differs from the unfiltered one:\n%s\nwant:\n%s", query, got, want)
		}
	}
}

func TestReportGridCaptionIsOnlyOnAPastMonth(t *testing.T) {
	t.Parallel()
	s := newDuesScenario(t)
	const caption = "Status per 2 Oktober 2026" // reportNow, in the fund's zone
	if body := s.get(t, s.base).Body.String(); !strings.Contains(gridBlock(body), caption) {
		t.Errorf("a past month's grid lacks %q", caption)
	}
	running := gridBlock(s.get(t, "/report/"+s.fund.ReportSlug+"?month=2026-10").Body.String())
	if running == "" {
		t.Fatal("no grid on the running month")
	}
	if strings.Contains(running, "Status per") {
		t.Errorf("the running month's grid carries a caption:\n%s", running)
	}
	// The running month passes its own month and ends there.
	if !strings.Contains(running, "Januari 2026 \u2013 Oktober 2026") {
		t.Errorf("the running month's range is wrong:\n%s", running)
	}
}

// The CSP allows styles only from the nonce'd <style>: no style attribute
// anywhere on the page, the grid's shapes included.
func TestReportGridUsesNoInlineStyleAttributes(t *testing.T) {
	t.Parallel()
	s := newDuesScenario(t)
	body := s.get(t, s.base).Body.String()
	if strings.Contains(body, " style=") {
		t.Errorf("the page carries an inline style attribute (the CSP refuses it)")
	}
}

func TestReportPDFDrawsTheGridWithoutSplittingARowAcrossPages(t *testing.T) {
	t.Parallel()
	s := newDuesScenario(t)
	q := store.New(s.db)
	ctx := context.Background()
	tiers, err := q.ListDuesTiersByFund(ctx, s.fund.ID)
	if err != nil || len(tiers) == 0 {
		t.Fatalf("ListDuesTiersByFund() = %v, %v", tiers, err)
	}
	const extra = 70 // fills more than a page of grid rows
	var names []string
	for i := range extra {
		name := fmt.Sprintf("Warga %03d", i)
		m, err := q.CreateMember(ctx, store.CreateMemberParams{FundID: s.fund.ID, Name: name, JoinedOn: "2000-01-01", CreatedAt: 1})
		if err != nil {
			t.Fatalf("CreateMember() = %v", err)
		}
		if _, err := q.CreateMemberTier(ctx, store.CreateMemberTierParams{
			FundID: s.fund.ID, MemberID: m.ID, TierID: &tiers[0].ID, EffectiveFrom: "2000-01", CreatedAt: 1,
		}); err != nil {
			t.Fatalf("CreateMemberTier() = %v", err)
		}
		names = append(names, name)
	}
	rec := s.get(t, "/report/"+s.fund.ReportSlug+"/pdf?month=2026-09")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET pdf = %d", rec.Code)
	}
	pages := pdfPages(t, rec.Body.Bytes())
	first := -1
	for i, p := range pages {
		if pdfHas(p, report.Text.GridHeading) {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatalf("no page carries %q", report.Text.GridHeading)
	}
	gridPages := pages[first:]
	if len(gridPages) < 2 {
		t.Fatalf("%d grid page(s) for %d members, want the roster to run over", len(gridPages), extra+5)
	}
	if !pdfHas(gridPages[0], report.Text.GridRange("Januari 2026", "September 2026")) || !pdfHas(gridPages[0], report.Text.GridAsOf("2 Oktober 2026")) {
		t.Errorf("first grid page lacks its range or its caption: %v", gridPages[0])
	}
	seen := map[string]int{}
	for i, p := range gridPages {
		if !pdfHas(p, "Jan") || !pdfHas(p, "Sep") {
			t.Errorf("grid page %d lacks the repeated column heads: %v", i+1, p)
		}
		for _, r := range p {
			seen[r]++
		}
	}
	for _, name := range append(names, "Ani", "Budi") {
		if seen[name] != 1 {
			t.Errorf("%q is drawn %d times in the grid, want once (a row is whole and on one page)", name, seen[name])
		}
	}
	last := gridPages[len(gridPages)-1]
	for _, word := range []string{report.Text.DuesPaidStatus, report.Text.DuesPartial, report.Text.DuesUnpaid, report.Text.GridNone} {
		if !pdfHas(last, word) {
			t.Errorf("the legend word %q is not with the grid's last rows; page: %v", word, last)
		}
	}
}

// A grid with fewer than twelve months ends at the content edge, not where a
// twelve-month grid would: the last month's head is drawn inside the last
// column before the right margin. September's report on the dues scenario has
// nine months (January to September).
func TestReportPDFGridEndsAtTheContentEdge(t *testing.T) {
	t.Parallel()
	s := newDuesScenario(t)
	rec := s.get(t, "/report/"+s.fund.ReportSlug+"/pdf?month=2026-09")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET pdf = %d", rec.Code)
	}
	// The A4 page's geometry in mm, as internal/report lays it out: an 18mm
	// margin each side (a 174mm content width) and 9mm grid columns.
	const (
		ptPerMM  = 72 / 25.4
		marginX  = 18.0
		contentW = 174.0
		gridColW = 9.0
	)
	right := (marginX + contentW) * ptPerMM
	lastCol := right - gridColW*ptPerMM

	for _, page := range pdfPagesAt(t, rec.Body.Bytes()) {
		var onGridPage bool
		for _, r := range page {
			if r.text == report.Text.GridHeading {
				onGridPage = true
			}
		}
		if !onGridPage {
			continue
		}
		var sep *pdfRunX
		for i, r := range page {
			if r.text == report.Text.MonthHeads[8] { // Sep, the last column
				sep = &page[i]
			}
		}
		if sep == nil {
			t.Fatalf("the grid page has no %q head: %v", report.Text.MonthHeads[8], page)
		}
		if sep.x < lastCol || sep.x >= right {
			t.Errorf("the last month's head is drawn at x=%.1fpt, want inside the last column [%.1f, %.1f) - the grid does not end at the content edge", sep.x, lastCol, right)
		}
		return
	}
	t.Fatalf("no page carries %q", report.Text.GridHeading)
}
