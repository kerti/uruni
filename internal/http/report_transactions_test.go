package http

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// txnScenario is September 2026 with something for every filter to include
// and exclude:
//
//	in   200.000 Kas Utama  plain row (Sep 3)
//	in    25.000 Kas Utama  dues Ani 2026-09 (Sep 5)
//	in    15.000 Duka       named contribution, Budi (Sep 7)
//	out   60.000 Kas Utama  with a receipt (Sep 12)
//	move  40.000 Kas Utama -> Duka (Sep 15)
//	move  10.000 Duka -> Kas Utama (Sep 16)
//	xfer  30.000 Tunai -> Bank, hidden (Sep 20)
//
// plus an August row that must never appear in September.
type txnScenario struct {
	reportFixture
	ani, budi int64
	duka      int64
	base      string
}

const receiptPath = "private-receipt-path.jpg"

func newTxnScenario(t *testing.T) txnScenario {
	t.Helper()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	q := store.New(f.db)
	s := txnScenario{reportFixture: f}

	mk := func(name string) int64 {
		m, err := q.CreateMember(ctx, store.CreateMemberParams{JoinedOn: "2000-01-01", FundID: f.fund.ID, Name: name, CreatedAt: 1})
		if err != nil {
			t.Fatalf("CreateMember(%s) = %v", name, err)
		}
		return m.ID
	}
	s.ani, s.budi = mk("Ani"), mk("Budi")

	env, err := f.l.OpenIncidental(ctx, ledger.OpenIncidentalParams{FundID: f.fund.ID, Occasion: "Duka", OpenedOn: "2026-08-01"})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v", err)
	}
	s.duka = env.PurposeID

	f.post(t, "in", 7_000, "2026-08-20")
	f.post(t, "in", 200_000, "2026-09-03")
	if _, err := f.l.PostDuesPayments(ctx, ledger.PostDuesPaymentsParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID, MemberID: s.ani, OccurredOn: "2026-09-05",
		Periods: []ledger.PeriodAmount{{DuesPeriod: "2026-09", Amount: 25_000}},
	}); err != nil {
		t.Fatalf("PostDuesPayments() = %v", err)
	}
	if _, err := f.l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: s.duka, MemberID: &s.budi,
		Direction: "in", Amount: 15_000, OccurredOn: "2026-09-07",
	}); err != nil {
		t.Fatalf("PostTransaction(contribution) = %v", err)
	}
	spent, err := f.l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 60_000, OccurredOn: "2026-09-12",
	})
	if err != nil {
		t.Fatalf("PostTransaction(out) = %v", err)
	}
	if _, err := q.CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: f.fund.ID, TransactionID: &spent.ID, Path: receiptPath, UploadedAt: 1,
	}); err != nil {
		t.Fatalf("CreateReceipt() = %v", err)
	}
	for _, mv := range []struct {
		from, to int64
		amount   money.Amount
		on       string
	}{{f.mainID, s.duka, 40_000, "2026-09-15"}, {s.duka, f.mainID, 10_000, "2026-09-16"}} {
		if _, err := f.l.PostPurposeMove(ctx, ledger.PostPurposeMoveParams{
			FundID: f.fund.ID, FromPurposeID: mv.from, ToPurposeID: mv.to, AccountID: f.cashID, Amount: mv.amount, OccurredOn: mv.on,
		}); err != nil {
			t.Fatalf("PostPurposeMove() = %v", err)
		}
	}
	bank, err := f.l.CreateAccount(ctx, ledger.CreateAccountParams{FundID: f.fund.ID, Kind: "bank", Name: "Rekening Bank"})
	if err != nil {
		t.Fatalf("CreateAccount() = %v", err)
	}
	if _, err := f.l.PostTransferBetweenAccounts(ctx, ledger.PostTransferBetweenAccountsParams{
		FundID: f.fund.ID, PurposeID: f.mainID, FromAccountID: f.cashID, ToAccountID: bank.ID, Amount: 30_000, OccurredOn: "2026-09-20",
	}); err != nil {
		t.Fatalf("PostTransferBetweenAccounts() = %v", err)
	}
	s.base = "/report/" + f.fund.ReportSlug + "?month=2026-09"
	return s
}

// txnSection is the transactions list's markup alone, so an assertion about
// a row is never satisfied by the header or the filter form.
func txnSection(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<ul class="rows txns">`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</ul>")
	return html.UnescapeString(body[i : i+j])
}

func rowCount(t *testing.T, body string) int {
	t.Helper()
	return strings.Count(txnSection(t, body), "<li>")
}

// totalLine is one line of the walk as the page prints it.
type totalLine struct{ label, value string }

// totalsOf reads the walk's lines in page order, unescaped.
func totalsOf(t *testing.T, body string) []totalLine {
	t.Helper()
	dl := regexp.MustCompile(`(?s)<dl class="totals">(.*?)</dl>`).FindStringSubmatch(body)
	if dl == nil {
		t.Fatal("no totals list on the page")
	}
	var out []totalLine
	for _, m := range regexp.MustCompile(`<dt>([^<]*)</dt><dd class="tabular">([^<]*)</dd>`).FindAllStringSubmatch(dl[1], -1) {
		out = append(out, totalLine{html.UnescapeString(m[1]), html.UnescapeString(m[2])})
	}
	return out
}

// inOut is the two lines every filter keeps.
func inOut(in, out int64) []totalLine {
	return []totalLine{
		{reportText.TotalIn, money.FormatIDR(money.Amount(in))},
		{reportText.TotalOut, money.FormatIDR(money.Amount(out))},
	}
}

func equalLines(a, b []totalLine) bool {
	return slices.Equal(a, b)
}

func TestReportListsTheMonthWithLabelsAndTotals(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	body := s.get(t, s.base).Body.String()

	if got := rowCount(t, body); got != 6 {
		t.Fatalf("rows = %d, want 6 (4 entries, 2 moves; the August row and the location transfer are out)", got)
	}
	for _, want := range []string{
		"3 September 2026", "September 2026 \u00b7 Ani", "Budi", "Dipindah: Kas Utama \u2192 Duka", "Dipindah: Duka \u2192 Kas Utama",
		"+" + money.FormatIDR(200_000), "-" + money.FormatIDR(60_000),
	} {
		if !strings.Contains(txnSection(t, body), want) {
			t.Errorf("list does not contain %q:\n%s", want, txnSection(t, body))
		}
	}
	if strings.Contains(body, "20 September") || strings.Contains(body, "Rekening Bank") {
		t.Error("a location transfer appears on the report")
	}
	if strings.Contains(body, "20 Agustus") {
		t.Error("an August row appears in September")
	}

	// ADR-038: the month is a walk. The August row (7.000) is what September
	// started on; in is 200.000 + 25.000 + 15.000 and out 60.000, moves in
	// neither; and September ended on 187.000. No Saldo awal, no Penyesuaian:
	// nothing of either was recorded, so neither line shows. There is no Bersih.
	want := []totalLine{
		{reportText.WalkStart("31 Agustus 2026"), money.FormatIDR(7_000)},
		{reportText.TotalIn, money.FormatIDR(240_000)},
		{reportText.TotalOut, money.FormatIDR(60_000)},
		{reportText.WalkEnd("30 September 2026"), money.FormatIDR(187_000)},
	}
	if got := totalsOf(t, body); !equalLines(got, want) {
		t.Errorf("totals = %v, want %v", got, want)
	}
}

func TestReportFilters(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	id := func(n int64) string { return itoa(n) }

	tests := []struct {
		name  string
		query string
		rows  int
		in    int64
		out   int64

		// walks marks the queries that still get a walk (ADR-038): no usable
		// filter at all, or a purpose alone. Only their two shared lines are
		// checked here; the walks themselves are in report_walk_test.go.
		walks bool
	}{
		{"purpose Kas Utama: its entries and both moves", "&purpose=" + id(s.mainID), 5, 225_000, 60_000, true},
		{"purpose Duka: its entry and both moves", "&purpose=" + id(s.duka), 3, 15_000, 0, true},
		{"member Ani", "&member=" + id(s.ani), 1, 25_000, 0, false},
		{"member Budi", "&member=" + id(s.budi), 1, 15_000, 0, false},
		{"direction in hides moves", "&dir=in", 3, 240_000, 0, false},
		{"direction out", "&dir=out", 1, 0, 60_000, false},
		{"purpose and direction together", "&purpose=" + id(s.duka) + "&dir=in", 1, 15_000, 0, false},
		{"a purpose that is not the fund's is no filter", "&purpose=99999", 6, 240_000, 60_000, true},
		{"a bad direction is no filter", "&dir=sideways", 6, 240_000, 60_000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := s.get(t, s.base+tt.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if got := rowCount(t, body); got != tt.rows {
				t.Errorf("rows = %d, want %d:\n%s", got, tt.rows, txnSection(t, body))
			}
			// A member or direction filter keeps only Total masuk and Total
			// keluar. A purpose alone, or no usable filter (a stale purpose, a
			// bad direction), walks, so those check the two lines they share.
			got := totalsOf(t, body)
			if tt.walks {
				got = got[1:3]
			}
			if want := inOut(tt.in, tt.out); !equalLines(got, want) {
				t.Errorf("totals = %v, want %v", got, want)
			}
		})
	}
}

// A purpose move is matched by a filter on either side, and shows once.
func TestReportPurposeMoveMatchesEitherSideOfThePurposeFilter(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	for name, purpose := range map[string]int64{"from side": s.mainID, "to side": s.duka} {
		t.Run(name, func(t *testing.T) {
			body := txnSection(t, s.get(t, s.base+"&purpose="+itoa(purpose)).Body.String())
			for _, move := range []string{"Dipindah: Kas Utama \u2192 Duka", "Dipindah: Duka \u2192 Kas Utama"} {
				if n := strings.Count(body, move); n != 1 {
					t.Errorf("%q appears %d times, want exactly one row", move, n)
				}
			}
		})
	}
}

func TestReportFilterFormIsAPlainGetCarryingTheMonth(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	body := s.get(t, s.base+"&purpose="+itoa(s.duka)+"&member="+itoa(s.budi)+"&dir=in").Body.String()

	for _, want := range []string{
		`<form class="filters" method="get" action="#transactions">`,
		`<input type="hidden" name="month" value="2026-09">`,
		`<option value="` + itoa(s.duka) + `" selected>Duka</option>`,
		`<option value="` + itoa(s.budi) + `" selected>Budi</option>`,
		`<option value="in" selected>`,
		`<option value="` + itoa(s.ani) + `">Ani</option>`,
		`<option value="` + itoa(s.mainID) + `">Kas Utama</option>`,
		`<button type="submit">Tampilkan</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	// The month form carries the filters, so changing month keeps them.
	for _, want := range []string{
		`<input type="hidden" name="purpose" value="` + itoa(s.duka) + `">`,
		`<input type="hidden" name="member" value="` + itoa(s.budi) + `">`,
		`<input type="hidden" name="dir" value="in">`,
	} {
		if strings.Count(body, want) != 1 {
			t.Errorf("body should carry %q once, in the month form", want)
		}
	}
}

func TestReportReceiptIsAMarkerNeverALink(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	body := s.get(t, s.base).Body.String()

	if n := strings.Count(txnSection(t, body), reportText.HasReceipt); n != 1 {
		t.Fatalf("%q appears %d times, want once (one row has a receipt)", reportText.HasReceipt, n)
	}
	for _, leak := range []string{receiptPath, "receipt", "nota/", ".jpg", "<img"} {
		if strings.Contains(body, leak) {
			t.Errorf("body contains %q; no receipt URL, id or path may reach the report", leak)
		}
	}
	// The only links on the page are the month steps and the PDF download
	// (#378), which is the month's file and nothing about a receipt.
	for _, m := range regexp.MustCompile(`<a [^>]*href="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		if !strings.HasPrefix(m[1], "?month=") && !regexp.MustCompile(`^/report/[^/]+/pdf\?month=\d{4}-\d{2}$`).MatchString(m[1]) {
			t.Errorf("unexpected link %q", m[1])
		}
	}
}

func TestReportEmptyFilteredMonthSaysSoCalmly(t *testing.T) {
	t.Parallel()
	s := newTxnScenario(t)
	// Budi gave to Duka only; filtered to money out there is nothing.
	body := s.get(t, s.base+"&member="+itoa(s.budi)+"&dir=out").Body.String()

	if !strings.Contains(body, reportText.NoRows) {
		t.Errorf("body does not carry %q", reportText.NoRows)
	}
	if strings.Contains(body, `<ul class="rows txns">`) {
		t.Error("an empty filtered month renders an empty list")
	}
	if got := totalsOf(t, body); !equalLines(got, inOut(0, 0)) {
		t.Errorf("totals = %v, want Total masuk and Total keluar at zero, no walk under a filter", got)
	}
	// A quiet month is not "nothing recorded at all".
	if strings.Contains(body, reportText.Empty) {
		t.Error("a filtered month reads as a fund with nothing recorded")
	}
}

func TestReportMonthStepKeepsTheFilters(t *testing.T) {
	t.Parallel()
	active := url.Values{}
	if got, want := monthHref("2026-08", active), "?month=2026-08#months"; got != want {
		t.Errorf("unfiltered: got %q, want %q", got, want)
	}
	active.Set("purpose", "3")
	active.Set("dir", "out")
	if got, want := monthHref("2026-08", active), "?month=2026-08&dir=out&purpose=3#months"; got != want {
		t.Errorf("filtered: got %q, want %q", got, want)
	}
}

// Every row shows the note the treasurer typed under its label - an entry
// and a "Dipindah" move alike - trimmed and escaped like any other text, on
// the page and in the PDF; a row without one shows no note line.
func TestReportRowShowsItsNote(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newReportFixture(t, "Kas RT 05")
	note := "  Beli gula <b>dan</b> teh  "
	if _, err := f.l.PostTransaction(ctx, ledger.PostTransactionParams{
		FundID: f.fund.ID, AccountID: f.cashID, PurposeID: f.mainID, Direction: "out", Amount: 40_000, OccurredOn: "2026-09-12", Note: &note,
	}); err != nil {
		t.Fatalf("PostTransaction() = %v", err)
	}
	f.post(t, "in", 50_000, "2026-09-13")
	env, err := f.l.OpenIncidental(ctx, ledger.OpenIncidentalParams{FundID: f.fund.ID, Occasion: "Duka", OpenedOn: "2026-09-01"})
	if err != nil {
		t.Fatalf("OpenIncidental() = %v", err)
	}
	moveNote := "Sisa kas untuk santunan"
	if _, err := f.l.PostPurposeMove(ctx, ledger.PostPurposeMoveParams{
		FundID: f.fund.ID, FromPurposeID: f.mainID, ToPurposeID: env.PurposeID, AccountID: f.cashID, Amount: 5_000, OccurredOn: "2026-09-14", Note: &moveNote,
	}); err != nil {
		t.Fatalf("PostPurposeMove() = %v", err)
	}

	body := f.get(t, "/report/"+f.fund.ReportSlug+"?month=2026-09").Body.String()
	// The raw body, not txnSection, which unescapes: the note's markup must
	// arrive as text.
	if want := `<span class="note">Beli gula &lt;b&gt;dan&lt;/b&gt; teh</span>`; !strings.Contains(body, want) {
		t.Errorf("page does not contain %q:\n%s", want, body)
	}
	section := txnSection(t, body)
	if want := `<span class="note">` + moveNote + `</span>`; !strings.Contains(section, want) {
		t.Errorf("the move's row does not carry its note %q:\n%s", want, section)
	}
	if got := strings.Count(section, `class="note"`); got != 2 {
		t.Errorf("note lines = %d, want 2 (the row without a note shows none)", got)
	}

	runs := pdfText(t, f.get(t, "/report/"+f.fund.ReportSlug+"/pdf?month=2026-09").Body.Bytes())
	for _, want := range []string{"Beli gula <b>dan</b> teh", moveNote} {
		if !pdfHas(runs, want) {
			t.Errorf("PDF text has no run %q; runs:\n%s", want, strings.Join(runs, "\n"))
		}
	}
}
