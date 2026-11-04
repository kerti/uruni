package http

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/report"
	"github.com/kerti/uruni/internal/store"
)

// ADR-050 on the public report: a waived month reads Diputihkan in neutral ink,
// the dues filter can isolate it, the grid has its own shape, and the waiver's
// note - someone's misfortune - is on neither the page nor the PDF.

// waiverNote is distinctive enough that its presence anywhere in a response, in
// part, is a leak.
const waiverNote = "Rumah terbakar habis dan harus mengungsi"

// newWaivedScenario is the dues scenario's September 2026 plus Joko (owes
// September, nothing paid, waived) and Kiki (paid 10.000 of 25.000, then waived).
func newWaivedScenario(t *testing.T) duesScenario {
	t.Helper()
	ctx := context.Background()
	s := newDuesScenario(t)
	q := store.New(s.db)
	tiers, err := q.ListDuesTiersByFund(ctx, s.fund.ID)
	if err != nil || len(tiers) != 1 {
		t.Fatalf("ListDuesTiersByFund() = %v, %v, want the scenario's one tier", tiers, err)
	}
	var waived []int64
	for _, name := range []string{"Joko", "Kiki"} {
		m, err := q.CreateMember(ctx, store.CreateMemberParams{FundID: s.fund.ID, Name: name, JoinedOn: "2000-01-01", CreatedAt: 1})
		if err != nil {
			t.Fatalf("CreateMember(%s) = %v", name, err)
		}
		if _, err := q.CreateMemberTier(ctx, store.CreateMemberTierParams{
			FundID: s.fund.ID, MemberID: m.ID, TierID: &tiers[0].ID, EffectiveFrom: "2000-01", CreatedAt: 1,
		}); err != nil {
			t.Fatalf("CreateMemberTier(%s) = %v", name, err)
		}
		waived = append(waived, m.ID)
	}
	if _, err := s.l.PostDuesPayments(ctx, ledger.PostDuesPaymentsParams{
		FundID: s.fund.ID, AccountID: s.cashID, MemberID: waived[1], OccurredOn: "2026-09-05",
		Periods: []ledger.PeriodAmount{{DuesPeriod: "2026-09", Amount: 10_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments(Kiki) = %v", err)
	}
	note := waiverNote
	for _, id := range waived {
		if _, err := s.l.WaiveDues(ctx, ledger.WaiveDuesParams{FundID: s.fund.ID, MemberID: id, Periods: []string{"2026-09"}, Note: &note}); err != nil {
			t.Fatalf("WaiveDues() = %v", err)
		}
	}
	return s
}

func TestReportShowsAWaivedMonthAsDiputihkanInNeutralInk(t *testing.T) {
	t.Parallel()
	s := newWaivedScenario(t)
	rec := s.get(t, s.base)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	sec := duesSection(t, rec.Body.String())
	if got, want := strings.Join(duesNames(sec), ","), "Ani,Budi,Cici,Dedi,Eka,Joko,Kiki"; got != want {
		t.Errorf("members = %q, want %q", got, want)
	}
	// Two badges, neutral class, the word, a radio-style icon: not paid, not partial.
	if n := strings.Count(sec, `<span class="badge waived"><svg`); n != 2 {
		t.Errorf("waived badges = %d, want 2 (Joko and Kiki):\n%s", n, sec)
	}
	if !strings.Contains(sec, `</svg>Diputihkan</span>`) {
		t.Errorf("no Diputihkan badge text:\n%s", sec)
	}
	// Kiki's payment stays visible, and her month is not counted as paid or partial.
	if !strings.Contains(sec, "Dibayar: Rp10.000") {
		t.Errorf("a partly paid waived month lost its payment:\n%s", sec)
	}
	if n := strings.Count(sec, `class="badge paid"`); n != 2 {
		t.Errorf("paid badges = %d, want the scenario's 2 (waived is never Lunas)", n)
	}
	if n := strings.Count(sec, `class="badge partial"`); n != 1 {
		t.Errorf("partial badges = %d, want the scenario's 1 (Kiki's waived month is not Bayar sebagian)", n)
	}
	// Neutral ink: the stylesheet gives .badge.waived the muted pair.
	if css := rec.Body.String(); !strings.Contains(css, ".badge.waived{background:var(--muted);color:var(--muted-foreground)}") {
		t.Error("the waived badge is not in the muted ink")
	}
}

func TestReportDuesFilterHasADiputihkanOptionAndLunasNeverIncludesIt(t *testing.T) {
	t.Parallel()
	s := newWaivedScenario(t)
	for _, tc := range []struct{ value, want string }{
		{"waived", "Joko,Kiki"},
		{"paid", "Ani,Dedi,Eka"},
		{"unpaid", "Budi"},
		{"partial", "Cici"},
	} {
		body := s.get(t, s.base+"&dues="+tc.value).Body.String()
		if got := strings.Join(duesNames(duesSection(t, body)), ","); got != tc.want {
			t.Errorf("dues=%s members = %q, want %q", tc.value, got, tc.want)
		}
		if !strings.Contains(body, `<option value="`+tc.value+`" selected>`) {
			t.Errorf("dues=%s option is not marked selected", tc.value)
		}
	}
	body := s.get(t, s.base).Body.String()
	if !strings.Contains(body, `<option value="waived">Diputihkan</option>`) {
		t.Errorf("the dues filter has no Diputihkan option:\n%s", regexp.MustCompile(`(?s)<select name="dues">.*?</select>`).FindString(body))
	}
}

func TestReportGridDrawsAWaivedMonthAsARingWithADot(t *testing.T) {
	t.Parallel()
	s := newWaivedScenario(t)
	grid := gridBlock(s.get(t, s.base).Body.String())
	for _, want := range []string{
		`<circle class="g-waived-ring" cx="7" cy="7" r="5.5"/><circle class="g-waived-dot" cx="7" cy="7" r="2"/>`,
		`<span class="sr-only">September 2026: Diputihkan</span>`,
		`</svg>Diputihkan</li>`, // the legend
	} {
		if !strings.Contains(grid, want) {
			t.Errorf("grid lacks %q:\n%s", want, grid)
		}
	}
	// One shape per waived cell plus the legend's: Joko, Kiki, and the legend.
	if n := strings.Count(grid, `class="g-waived-ring"`); n != 3 {
		t.Errorf("waived rings in the grid = %d, want 3 (two cells and the legend)", n)
	}
}

func TestReportNeverCarriesAWaiversNote(t *testing.T) {
	t.Parallel()
	s := newWaivedScenario(t)
	words := strings.Fields(waiverNote)
	leak := func(where, body string) {
		t.Helper()
		for _, piece := range append([]string{waiverNote}, words[:3]...) {
			if len(piece) > 3 && strings.Contains(body, piece) {
				t.Errorf("%s carries the waiver's note (%q)", where, piece)
			}
		}
	}
	for _, query := range []string{"", "&dues=waived", "&dues=paid", "&group=month"} {
		leak("the page"+query, s.get(t, s.base+query).Body.String())
	}
	leak("the range page", s.get(t, "/report/"+s.fund.ReportSlug+"?from=2026-09-01&to=2026-09-30").Body.String())

	pdf := s.get(t, "/report/"+s.fund.ReportSlug+"/pdf?month=2026-09")
	if pdf.Code != http.StatusOK {
		t.Fatalf("PDF = %d, want 200", pdf.Code)
	}
	runs := pdfText(t, pdf.Body.Bytes())
	leak("the PDF", strings.Join(runs, "\n"))
	// The control: the PDF does say Diputihkan, so the absence above is the note's.
	if !pdfHas(runs, report.Text.DuesWaived) {
		t.Errorf("PDF has no %q run; runs:\n%s", report.Text.DuesWaived, strings.Join(runs, "\n"))
	}
}
