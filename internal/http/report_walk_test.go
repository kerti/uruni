package http

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// walkPage is the report as ADR-038 reads it, in one fund:
//
//	Aug 20  in   7.000   Kas Utama
//	Aug 20  in  20.000   Duka, a named contribution from Budi
//	Sep  1  opening 100.000 on a location added mid-life (Rekening Bank)
//	Sep  3  in 200.000   Kas Utama
//	Sep  8  reversal of Budi's Aug 20 contribution (a LATER month than the gift)
//	Sep 10  adjustment out 4.000, posted on its own
//	Sep 12  out   6.000  Kas Utama
//	Sep 14  Pindah pos 5.000, Kas Utama -> Duka
//
// September starts on 27.000 and ends on 297.000:
// 27.000 + 100.000 + (200.000 - 20.000) - 6.000 - 4.000. Kas Utama starts on
// 7.000 and ends on 292.000; Duka starts on 20.000 and ends on 5.000.
type walkPage struct {
	reportFixture
	budi, duka int64
	base       string
}

func newWalkPage(t *testing.T) walkPage {
	t.Helper()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	q := store.New(f.db)
	w := walkPage{reportFixture: f}

	budi, err := q.CreateMember(ctx, store.CreateMemberParams{FundID: f.fund.ID, Name: "Budi", CreatedAt: 1})
	if err != nil {
		t.Fatalf("CreateMember() = %v", err)
	}
	w.budi = budi.ID
	env, err := f.l.OpenIncidental(ctx, ledger.OpenIncidentalParams{FundID: f.fund.ID, Occasion: "Duka", OpenedOn: "2026-08-01"})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v", err)
	}
	w.duka = env.PurposeID

	f.post(t, "in", 7_000, "2026-08-20")
	gift, err := f.l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: w.duka, MemberID: &w.budi, Direction: "in", Amount: 20_000, OccurredOn: "2026-08-20",
	})
	if err != nil {
		t.Fatalf("PostTransaction(contribution) = %v", err)
	}
	if _, err := f.l.CreateAccount(ctx, ledger.CreateAccountParams{
		FundID: f.fund.ID, Kind: "bank", Name: "Rekening Bank",
		OpeningBalance: &ledger.OpeningBalance{Amount: 100_000, OccurredOn: "2026-09-01"},
	}); err != nil {
		t.Fatalf("CreateAccount() = %v", err)
	}
	f.post(t, "in", 200_000, "2026-09-03")
	if _, err := f.l.ReverseDuesPayment(ctx, ledger.ReverseDuesPaymentParams{FundID: f.fund.ID, TransactionID: gift.ID, OccurredOn: "2026-09-08"}); err != nil {
		t.Fatalf("ReverseDuesPayment() = %v", err)
	}
	if _, err := f.l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 4_000, OccurredOn: "2026-09-10", IsAdjustment: true,
	}); err != nil {
		t.Fatalf("PostTransaction(adjustment) = %v", err)
	}
	f.post(t, "out", 6_000, "2026-09-12")
	if _, err := f.l.PostPurposeMove(ctx, ledger.PostPurposeMoveParams{
		FundID: f.fund.ID, FromPurposeID: f.mainID, ToPurposeID: w.duka, AccountID: f.cashID, Amount: 5_000, OccurredOn: "2026-09-14",
	}); err != nil {
		t.Fatalf("PostPurposeMove() = %v", err)
	}

	w.base = "/report/" + f.fund.ReportSlug + "?month=2026-09"
	return w
}

func TestReportShowsTheMonthAsAWalkFromStartToEnd(t *testing.T) {
	w := newWalkPage(t)
	body := w.get(t, w.base).Body.String()

	want := []totalLine{
		{reportText.WalkStart("31 Agustus 2026"), money.FormatIDR(27_000)},
		{reportText.RowOpening, money.FormatIDR(100_000)},
		{reportText.TotalIn, money.FormatIDR(180_000)}, // 200.000 less the reversed 20.000
		{reportText.TotalOut, money.FormatIDR(6_000)},
		{reportText.RowAdjustment, money.FormatIDR(-4_000)},
		{reportText.WalkEnd("30 September 2026"), money.FormatIDR(297_000)},
	}
	if got := totalsOf(t, body); !equalLines(got, want) {
		t.Errorf("walk = %v, want %v", got, want)
	}
	if strings.Contains(body, "Bersih") {
		t.Error("the page still carries Bersih; the two dated balances say it")
	}
	// The closing line is the one that is emphasised.
	if n := strings.Count(body, `<div class="last">`); n != 1 {
		t.Errorf("emphasised lines = %d, want 1 (the closing balance)", n)
	}
	if !strings.Contains(body, `<div class="last"><dt>`+reportText.WalkEnd("30 September 2026")) {
		t.Error("the emphasised line is not the closing balance")
	}
}

// A month with no opening and no adjustment shows neither line.
func TestReportWalkHidesSaldoAwalAndPenyesuaianWhenZero(t *testing.T) {
	w := newWalkPage(t)
	got := totalsOf(t, w.get(t, "/report/"+w.fund.ReportSlug+"?month=2026-08").Body.String())

	want := []totalLine{
		{reportText.WalkStart("31 Juli 2026"), money.FormatIDR(0)},
		{reportText.TotalIn, money.FormatIDR(27_000)},
		{reportText.TotalOut, money.FormatIDR(0)},
		{reportText.WalkEnd("31 Agustus 2026"), money.FormatIDR(27_000)},
	}
	if !equalLines(got, want) {
		t.Errorf("walk = %v, want %v", got, want)
	}
}

// The running month ends on today, and says so with today's date.
func TestReportWalkOfTheRunningMonthEndsOnToday(t *testing.T) {
	w := newWalkPage(t)
	w.post(t, "in", 1_000, "2026-10-01")
	// Dated after today (the clock reads 2 Oct): still in the walk.
	w.post(t, "out", 250, "2026-10-20")

	got := totalsOf(t, w.get(t, "/report/"+w.fund.ReportSlug).Body.String())
	want := []totalLine{
		{reportText.WalkStart("30 September 2026"), money.FormatIDR(297_000)},
		{reportText.TotalIn, money.FormatIDR(1_000)},
		{reportText.TotalOut, money.FormatIDR(250)},
		{reportText.WalkEnd("2 Oktober 2026"), money.FormatIDR(297_750)},
	}
	if !equalLines(got, want) {
		t.Errorf("walk = %v, want %v", got, want)
	}
	if body := w.get(t, "/report/"+w.fund.ReportSlug).Body.String(); !strings.Contains(txnSection(t, body), "20 Oktober 2026") {
		t.Error("a row dated after today is not listed in the running month")
	}
}

// A balance has no meaning for one member or one direction: only Total masuk
// and Total keluar, with or without a pos beside it.
func TestReportWalkUnderAFilterIsOnlyMasukAndKeluar(t *testing.T) {
	w := newWalkPage(t)
	tests := []struct {
		name    string
		query   string
		in, out int64
	}{
		{"member", "&member=" + itoa(w.budi), -20_000, 0},
		{"direction in", "&dir=in", 180_000, 0},
		{"direction out", "&dir=out", 0, 6_000},
		{"member with a purpose", "&member=" + itoa(w.budi) + "&purpose=" + itoa(w.duka), -20_000, 0},
		{"direction with a purpose", "&dir=in&purpose=" + itoa(w.mainID), 200_000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := w.get(t, w.base+tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if got := totalsOf(t, body); !equalLines(got, inOut(tt.in, tt.out)) {
				t.Errorf("totals = %v, want %v", got, inOut(tt.in, tt.out))
			}
			for _, absent := range []string{"Saldo 31 Agustus", "Saldo 30 September", reportText.WalkMoved} {
				if strings.Contains(body, absent) {
					t.Errorf("a filtered page carries %q: a balance has no meaning for a slice of the fund", absent)
				}
			}
		})
	}
}

// A reversal filters as Uang masuk (and counts there as a minus); it no longer
// shows under Uang keluar. An opening and a Penyesuaian are neither, so a
// direction or member filter hides them.
func TestReportReversalFiltersAsMasukAndOpeningAndPenyesuaianAreHiddenByFilters(t *testing.T) {
	w := newWalkPage(t)
	reversal := "Pembatalan \u00b7 Budi"

	all := txnSection(t, w.get(t, w.base).Body.String())
	in := txnSection(t, w.get(t, w.base+"&dir=in").Body.String())
	out := txnSection(t, w.get(t, w.base+"&dir=out").Body.String())
	byMember := txnSection(t, w.get(t, w.base+"&member="+itoa(w.budi)).Body.String())

	if !strings.Contains(all, reversal) || !strings.Contains(in, reversal) {
		t.Errorf("the reversal is missing from the unfiltered list or under dir=in:\n%s", in)
	}
	if strings.Contains(out, reversal) {
		t.Errorf("the reversal is listed under dir=out:\n%s", out)
	}
	// Its amount is still the money that left the fund.
	if !strings.Contains(in, "-"+money.FormatIDR(20_000)) {
		t.Errorf("the reversal row does not read as money out of the fund:\n%s", in)
	}

	for _, want := range []string{reportText.RowOpening, reportText.RowAdjustment} {
		if !strings.Contains(all, `<span class="label-line">`+want+`</span>`) {
			t.Errorf("unfiltered list has no %q row:\n%s", want, all)
		}
	}
	for name, section := range map[string]string{"dir=in": in, "dir=out": out, "member": byMember} {
		for _, hidden := range []string{reportText.RowOpening, reportText.RowAdjustment} {
			if strings.Contains(section, `<span class="label-line">`+hidden+`</span>`) {
				t.Errorf("%s list shows a %q row, want it hidden:\n%s", name, hidden, section)
			}
		}
	}
}

// A standalone adjustment rendered blank before; it now reads Penyesuaian.
func TestReportStandaloneAdjustmentIsLabelledPenyesuaian(t *testing.T) {
	w := newWalkPage(t)
	section := txnSection(t, w.get(t, w.base).Body.String())

	if n := strings.Count(section, `<span class="label-line">Penyesuaian</span>`); n != 1 {
		t.Errorf("Penyesuaian rows = %d, want 1:\n%s", n, section)
	}
	if !strings.Contains(section, "-"+money.FormatIDR(4_000)) {
		t.Errorf("the adjustment's amount is missing:\n%s", section)
	}
}

// The statement's Ringkasan reads the same walk as the page, in the same
// order and words, and draws only characters the font has: a negative figure
// is an ASCII hyphen, never U+2212.
func TestReportPDFCarriesTheSameWalk(t *testing.T) {
	w := newWalkPage(t)
	rec := w.get(t, "/report/"+w.fund.ReportSlug+"/pdf?month=2026-09")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	runs := pdfText(t, rec.Body.Bytes())

	want := []string{
		reportText.WalkStart("31 Agustus 2026"), money.FormatIDR(27_000),
		reportText.RowOpening, money.FormatIDR(100_000),
		reportText.TotalIn, money.FormatIDR(180_000),
		reportText.TotalOut, money.FormatIDR(6_000),
		reportText.RowAdjustment, money.FormatIDR(-4_000),
		reportText.WalkEnd("30 September 2026"), money.FormatIDR(297_000),
	}
	at := slicesIndexFrom(runs, reportText.WalkStart("31 Agustus 2026"))
	if at < 0 || at+len(want) > len(runs) {
		t.Fatalf("PDF has no walk starting at %q; runs:\n%s", want[0], strings.Join(runs, "\n"))
	}
	for i, wantRun := range want {
		if runs[at+i] != wantRun {
			t.Errorf("walk run %d = %q, want %q", i, runs[at+i], wantRun)
		}
	}
	if strings.Contains(strings.Join(runs, ""), reportTextBersih) {
		t.Error("the PDF still carries Bersih")
	}

	for _, name := range []string{"fonts/NotoSans-Regular.ttf", "fonts/NotoSans-Bold.ttf"} {
		raw, err := reportFonts.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		has := fontRunes(t, raw)
		for _, run := range runs {
			for _, r := range run {
				if !has[r] {
					t.Errorf("%s has no glyph for U+%04X in %q", name, r, run)
				}
			}
		}
	}
}

// reportTextBersih is the word the walk replaced; spelled here only so the
// test can assert its absence.
const reportTextBersih = "Bersih"

func slicesIndexFrom(runs []string, want string) int {
	for i, r := range runs {
		if r == want {
			return i
		}
	}
	return -1
}

// A purpose filter walks that pos: its own balance at both ends, and Dipindah
// for the money moved in or out of it. Same words and order as the whole fund.
func TestReportPurposeFilterShowsThatPosAsAWalk(t *testing.T) {
	w := newWalkPage(t)
	tests := []struct {
		name    string
		purpose int64
		want    []totalLine
	}{
		{"Kas Utama", w.mainID, []totalLine{
			{reportText.WalkStart("31 Agustus 2026"), money.FormatIDR(7_000)},
			{reportText.RowOpening, money.FormatIDR(100_000)},
			{reportText.TotalIn, money.FormatIDR(200_000)},
			{reportText.TotalOut, money.FormatIDR(6_000)},
			{reportText.RowAdjustment, money.FormatIDR(-4_000)},
			{reportText.WalkMoved, money.FormatIDR(-5_000)},
			{reportText.WalkEnd("30 September 2026"), money.FormatIDR(292_000)},
		}},
		// No opening and no adjustment, so neither line; the reversal nets the
		// 20.000 given in August against Total masuk.
		{"Duka", w.duka, []totalLine{
			{reportText.WalkStart("31 Agustus 2026"), money.FormatIDR(20_000)},
			{reportText.TotalIn, money.FormatIDR(-20_000)},
			{reportText.TotalOut, money.FormatIDR(0)},
			{reportText.WalkMoved, money.FormatIDR(5_000)},
			{reportText.WalkEnd("30 September 2026"), money.FormatIDR(5_000)},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := w.get(t, w.base+"&purpose="+itoa(tt.purpose)).Body.String()
			if got := totalsOf(t, body); !equalLines(got, tt.want) {
				t.Errorf("walk = %v, want %v", got, tt.want)
			}
			if n := strings.Count(body, `<div class="last">`); n != 1 {
				t.Errorf("emphasised lines = %d, want 1 (the closing balance)", n)
			}
		})
	}

	// A pos with no move in the month shows no Dipindah line.
	got := totalsOf(t, w.get(t, "/report/"+w.fund.ReportSlug+"?month=2026-08&purpose="+itoa(w.duka)).Body.String())
	for _, l := range got {
		if l.label == reportText.WalkMoved {
			t.Errorf("August has no move for Duka but the walk shows %v", got)
		}
	}
}

// The walk's ends say "Saldo 30 September 2026": the header's Saldo per pos
// already spends the word "per".
func TestReportWalkEndsCarryNoPer(t *testing.T) {
	w := newWalkPage(t)
	if got := reportText.WalkStart("30 September 2026"); got != "Saldo 30 September 2026" {
		t.Errorf("WalkStart = %q, want %q", got, "Saldo 30 September 2026")
	}
	if got := reportText.WalkEnd("30 September 2026"); got != "Saldo 30 September 2026" {
		t.Errorf("WalkEnd = %q, want %q", got, "Saldo 30 September 2026")
	}
	for _, query := range []string{"", "&purpose=" + itoa(w.duka)} {
		for _, l := range totalsOf(t, w.get(t, w.base+query).Body.String()) {
			if strings.Contains(l.label, "Saldo per") {
				t.Errorf("walk line %q carries \"per\"", l.label)
			}
		}
	}
}

// The statement is the whole fund's month and takes no filter, so a purpose
// in its query changes nothing; but it draws the page's walkLines, so a walk
// that does carry Dipindah reads in the page's order there too.
func TestReportPDFDrawsTheWalkLinesInThePagesOrder(t *testing.T) {
	w := newWalkPage(t)
	plain := pdfText(t, w.get(t, "/report/"+w.fund.ReportSlug+"/pdf?month=2026-09").Body.Bytes())
	filtered := pdfText(t, w.get(t, "/report/"+w.fund.ReportSlug+"/pdf?month=2026-09&purpose="+itoa(w.duka)).Body.Bytes())
	if strings.Join(plain, "|") != strings.Join(filtered, "|") {
		t.Error("a purpose in the PDF's query changed the statement; the PDF is the whole fund's month")
	}

	walk := ledger.ReportWalk{
		Full: true, StartOn: "2026-08-31", EndOn: "2026-09-30",
		Start: 7_000, Openings: 100_000, In: 200_000, Out: 6_000, Adjustments: -4_000, Moved: -5_000, End: 292_000,
	}
	page := buildReportPage(ledger.Report{FundName: "Kas RT 05", Month: "2026-09", Walk: walk}, false)
	body, err := drawReportPDF(page)
	if err != nil {
		t.Fatalf("drawReportPDF() = %v", err)
	}
	runs := pdfText(t, body)

	want := []string{
		reportText.WalkStart("31 Agustus 2026"), money.FormatIDR(7_000),
		reportText.RowOpening, money.FormatIDR(100_000),
		reportText.TotalIn, money.FormatIDR(200_000),
		reportText.TotalOut, money.FormatIDR(6_000),
		reportText.RowAdjustment, money.FormatIDR(-4_000),
		reportText.WalkMoved, money.FormatIDR(-5_000),
		reportText.WalkEnd("30 September 2026"), money.FormatIDR(292_000),
	}
	at := slicesIndexFrom(runs, reportText.WalkStart("31 Agustus 2026"))
	if at < 0 || at+len(want) > len(runs) {
		t.Fatalf("PDF has no walk starting at %q; runs:\n%s", want[0], strings.Join(runs, "\n"))
	}
	for i, wantRun := range want {
		if runs[at+i] != wantRun {
			t.Errorf("walk run %d = %q, want %q", i, runs[at+i], wantRun)
		}
	}
}
