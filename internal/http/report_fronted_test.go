package http

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/report"
)

// ADR-051 on the public page and the PDF: Rp450.000 fronted 27 Okt and paid out
// of Tunai 12 Nov is October's spending, with a Talangan line closing October's
// walk (+450.000) and November's (-450.000), and a second line on the row saying
// when and from where it was paid.
//
//	Sep 15  in 300.000   Kas Utama (Tunai)
//	Okt 27  Jane fronts 450.000 for Kas Utama, paid from Tunai on Nov 12
//	Nov  3  in 600.000   (the month's dues, as an in)
//
// The clock stands on 5 December: October and November are past months.
type frontedPage struct {
	reportFixture
	memberID int64
}

func newFrontedPage(t *testing.T) frontedPage {
	t.Helper()
	ctx := context.Background()
	f := newReportFixtureAt(t, "Kas RT 05", time.Date(2026, 12, 5, 3, 0, 0, 0, time.UTC))
	member, err := f.l.CreateMember(ctx, ledger.CreateMemberParams{FundID: f.fund.ID, Name: "Jane", JoinedOn: "2026-01-01"})
	if err != nil {
		t.Fatalf("CreateMember() = %v", err)
	}
	f.post(t, "in", 300_000, "2026-09-15")
	claim, err := f.l.CreateReimbursement(ctx, ledger.CreateReimbursementParams{
		FundID: f.fund.ID, MemberID: member.ID, PurposeID: f.mainID, Amount: 450_000, IncurredOn: "2026-10-27",
	})
	if err != nil {
		t.Fatalf("CreateReimbursement() = %v", err)
	}
	f.post(t, "in", 600_000, "2026-11-03")
	if _, err := f.l.SettleReimbursement(ctx, ledger.SettleReimbursementParams{
		FundID: f.fund.ID, ReimbursementID: claim.ID, AccountID: f.cashID, OccurredOn: "2026-11-12",
	}); err != nil {
		t.Fatalf("SettleReimbursement() = %v", err)
	}
	return frontedPage{reportFixture: f, memberID: member.ID}
}

// secondLine is the page's second line for a payout, as the app words it.
const secondLine = "Dibayar 12 Nov"

func TestReportCountsTheTalanganInTheMonthItWasFronted(t *testing.T) {
	t.Parallel()
	p := newFrontedPage(t)
	slug := "/report/" + p.fund.ReportSlug

	oct := p.get(t, slug+"?month=2026-10").Body.String()
	wantOct := []totalLine{
		{report.Text.WalkStart("30 September 2026"), money.FormatIDR(300_000)},
		{report.Text.TotalIn, walkSigned(0)},
		{report.Text.TotalOut, walkSigned(-(450_000))},
		{report.Text.WalkPaidLater, walkSigned(450_000)},
		{report.Text.WalkEnd("31 Oktober 2026"), money.FormatIDR(300_000)},
	}
	if got := totalsOf(t, oct); !equalLines(got, wantOct) {
		t.Errorf("October's walk = %v, want %v", got, wantOct)
	}
	out := groupSection(t, oct, "out")
	if !strings.Contains(out, "27 Oktober 2026") || !strings.Contains(out, money.FormatIDR(450_000)) || !strings.Contains(out, secondLine) {
		t.Errorf("October's Uang keluar does not hold the payout on 27 Oktober with %q:\n%s", secondLine, out)
	}
	if !strings.Contains(out, `<span class="tabular">`+money.FormatIDR(450_000)+`</span></h3>`) {
		t.Errorf("Uang keluar's subtotal is not the walk's 450.000:\n%s", out)
	}

	nov := p.get(t, slug+"?month=2026-11").Body.String()
	wantNov := []totalLine{
		{report.Text.WalkStart("31 Oktober 2026"), money.FormatIDR(300_000)},
		{report.Text.TotalIn, walkSigned(600_000)},
		{report.Text.TotalOut, walkSigned(0)},
		{report.Text.WalkPaidEarlier, walkSigned(-450_000)},
		{report.Text.WalkEnd("30 November 2026"), money.FormatIDR(450_000)},
	}
	if got := totalsOf(t, nov); !equalLines(got, wantNov) {
		t.Errorf("November's walk = %v, want %v", got, wantNov)
	}
	out = groupSection(t, nov, "out")
	if strings.Contains(out, "<li>") || strings.Contains(nov, secondLine) {
		t.Errorf("November's Uang keluar holds the October payout:\n%s", out)
	}
	if !strings.Contains(out, report.Text.NoRowsOut) {
		t.Errorf("November's Uang keluar does not say nothing went out:\n%s", out)
	}
}

// Shown only when non-zero: a month no talangan crosses has the walk it always
// had.
func TestReportTalanganLineIsHiddenWhenZero(t *testing.T) {
	t.Parallel()
	p := newFrontedPage(t)
	for _, month := range []string{"2026-09", "2026-12"} {
		for _, l := range totalsOf(t, p.get(t, "/report/"+p.fund.ReportSlug+"?month="+month).Body.String()) {
			if l.label == report.Text.WalkPaidLater {
				t.Errorf("%s carries a Talangan line: %v", month, l)
			}
		}
	}
}

// A range holding both days of the claim counts the payout once, in October's
// rows, and has no bridge; by month the payout sits under Oktober.
func TestReportRangeHoldingTheWholeClaimHasNoTalanganLineAndGroupsItInOctober(t *testing.T) {
	t.Parallel()
	p := newFrontedPage(t)
	base := "/report/" + p.fund.ReportSlug + "?from=2026-10-20&to=2026-11-30"

	for _, l := range totalsOf(t, p.get(t, base).Body.String()) {
		if l.label == report.Text.WalkPaidLater {
			t.Errorf("the range carries a Talangan line: %v", l)
		}
	}
	byMonth := p.get(t, base+"&group=month").Body.String()
	if oct := groupSection(t, byMonth, "2026-10"); !strings.Contains(oct, secondLine) || !strings.Contains(oct, "27 Oktober 2026") {
		t.Errorf("by month, the payout is not under Oktober 2026:\n%s", oct)
	}
	if nov := groupSection(t, byMonth, "2026-11"); strings.Contains(nov, secondLine) {
		t.Errorf("by month, the payout is also under November 2026:\n%s", nov)
	}

	// Cut between the claim's two days, the bridge is back.
	cut := totalsOf(t, p.get(t, "/report/"+p.fund.ReportSlug+"?from=2026-10-20&to=2026-11-10").Body.String())
	found := false
	for _, l := range cut {
		if l.label == report.Text.WalkPaidLater {
			found = true
			if l.value != walkSigned(450_000) {
				t.Errorf("Talangan over 20 Okt to 10 Nov = %s, want %s", l.value, walkSigned(450_000))
			}
		}
	}
	if !found {
		t.Errorf("a range cut between the two days has no Talangan line: %v", cut)
	}
}

// A pos filter reads the pos's own bridge; a member filter has no walk.
func TestReportTalanganLineUnderAPosAndAMemberFilter(t *testing.T) {
	t.Parallel()
	p := newFrontedPage(t)
	slug := "/report/" + p.fund.ReportSlug

	pos := totalsOf(t, p.get(t, slug+"?month=2026-10&purpose="+itoa(p.mainID)).Body.String())
	if len(pos) != 5 || pos[3].label != report.Text.WalkPaidLater || pos[3].value != walkSigned(450_000) {
		t.Errorf("Kas Utama's October walk = %v, want a Talangan line of 450.000 as its fourth", pos)
	}

	member := totalsOf(t, p.get(t, slug+"?month=2026-10&member="+itoa(p.memberID)).Body.String())
	want := []totalLine{{report.Text.TotalIn, money.FormatIDR(0)}, {report.Text.TotalOut, money.FormatIDR(450_000)}}
	if !equalLines(member, want) {
		t.Errorf("Jane's October = %v, want only %v: a member filter has no walk, so no bridge", member, want)
	}
}

// The PDF carries the same line, in the same place, and the same second line.
func TestReportPDFCarriesTheTalanganLineAndTheSecondLine(t *testing.T) {
	t.Parallel()
	p := newFrontedPage(t)
	rec := p.get(t, "/report/"+p.fund.ReportSlug+"/pdf?month=2026-10")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	runs := pdfText(t, rec.Body.Bytes())

	want := []string{
		report.Text.WalkStart("30 September 2026"), money.FormatIDR(300_000),
		report.Text.TotalIn, walkSigned(0),
		report.Text.TotalOut, walkSigned(-(450_000)),
		report.Text.WalkPaidLater, walkSigned(450_000),
		report.Text.WalkEnd("31 Oktober 2026"), money.FormatIDR(300_000),
	}
	at := slicesIndexFrom(runs, want[0])
	if at < 0 || at+len(want) > len(runs) {
		t.Fatalf("PDF has no walk starting at %q; runs:\n%s", want[0], strings.Join(runs, "\n"))
	}
	for i, w := range want {
		if runs[at+i] != w {
			t.Errorf("walk run %d = %q, want %q", i, runs[at+i], w)
		}
	}
	if !pdfHas(runs, secondLine) {
		t.Errorf("the PDF has no %q; runs:\n%s", secondLine, strings.Join(runs, "\n"))
	}

	nov := strings.Join(pdfText(t, p.get(t, "/report/"+p.fund.ReportSlug+"/pdf?month=2026-11").Body.Bytes()), "\n")
	if strings.Contains(nov, secondLine) {
		t.Error("November's PDF lists the October payout")
	}
	if !strings.Contains(nov, money.FormatIDR(-450_000)) {
		t.Errorf("November's PDF has no -450.000 Talangan line:\n%s", nov)
	}
}
