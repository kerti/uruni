package report

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
)

// The three cek kas states, read as the home banner reads them: never counted
// is neutral (never green), matched is green, a discrepancy is terracotta
// with its amount.
func TestReportPageCheckStates(t *testing.T) {
	t.Parallel()
	base := ledger.Report{FundName: "Kas", AsOf: "2026-10-02", Month: "2026-10", Months: []string{"2026-10"}}

	page := NewPage(base, false)
	if page.Check.Class != "never" || page.Check.Message != Text.NeverChecked {
		t.Errorf("no count = %+v, want the never state", page.Check)
	}

	base.Reconciliation = &ledger.ReportReconciliation{Date: "2026-09-30", Matched: true}
	page = NewPage(base, false)
	if page.Check.Class != "matched" || page.Check.When != "Terakhir dicek 30 September 2026" {
		t.Errorf("matched = %+v", page.Check)
	}

	base.Reconciliation = &ledger.ReportReconciliation{Date: "2026-09-30", Difference: 15_000}
	page = NewPage(base, false)
	if page.Check.Class != "discrepancy" || page.Check.Message != Text.Discrepancy(money.FormatIDR(15_000)) {
		t.Errorf("discrepancy = %+v", page.Check)
	}
}

func TestReportPagePurposeBalances(t *testing.T) {
	t.Parallel()
	page := NewPage(ledger.Report{
		AsOf: "2026-10-02", Month: "2026-10", Months: []string{"2026-10"},
		PurposeBalances: []ledger.ReportPurposeBalance{
			{Name: "Kas Utama", Kind: "main", Balance: -20_000},
			{Name: "Duka", Kind: "incidental", Balance: 50_000},
		},
	}, false)
	if len(page.Purposes) != 2 || page.Purposes[0].Name != "Kas Utama" {
		t.Fatalf("purposes = %+v, want Kas Utama first, in the ledger's order", page.Purposes)
	}
	if !page.Purposes[0].Negative || page.Purposes[1].Negative {
		t.Errorf("negative flags = %v, %v; want only Kas Utama's", page.Purposes[0].Negative, page.Purposes[1].Negative)
	}
}

// TestReportPaletteMatchesIndexCSS is ADR-035's guard on the hand-kept palette:
// every token the report uses holds the value index.css's :root gives it.
func TestReportPaletteMatchesIndexCSS(t *testing.T) {
	t.Parallel()
	css, err := os.ReadFile("../../web/src/index.css")
	if err != nil {
		t.Fatalf("reading index.css: %v", err)
	}
	root := regexp.MustCompile(`(?s):root\s*\{(.*?)\}`).FindSubmatch(css)
	if root == nil {
		t.Fatal("no :root block in index.css")
	}
	values := map[string]string{}
	for _, m := range regexp.MustCompile(`--([a-z-]+):\s*([^;]+);`).FindAllSubmatch(root[1], -1) {
		values[string(m[1])] = strings.ToLower(strings.TrimSpace(string(m[2])))
	}
	for name, want := range reportPalette {
		got, ok := values[name]
		if !ok {
			t.Errorf("--%s is in the report palette but not in index.css's :root", name)
			continue
		}
		if got != want {
			t.Errorf("--%s = %s in index.css, %s in the report palette", name, got, want)
		}
	}
	if len(reportPaletteOrder) != len(reportPalette) {
		t.Errorf("reportPaletteOrder has %d names, reportPalette %d", len(reportPaletteOrder), len(reportPalette))
	}
}

func TestReportCopyDates(t *testing.T) {
	t.Parallel()
	if got := Text.longDate("2026-01-09"); got != "9 Januari 2026" {
		t.Errorf("longDate = %q", got)
	}
	if got := Text.monthName("2026-12"); got != "Desember 2026" {
		t.Errorf("monthName = %q", got)
	}
}

// TestHeroSizeClassStepsDownAsTheFigureGrows pins #540's steps: everyday
// figures keep the full hero size, and the widest legal figure
// (Rp999.999.999.999, with or without its minus) gets a smaller one.
func TestHeroSizeClassStepsDownAsTheFigureGrows(t *testing.T) {
	for _, c := range []struct {
		figure string
		want   string
	}{
		{money.FormatIDR(1_450_000), ""},
		{money.FormatIDR(money.MaxAmount), "s28"},
		{money.FormatIDR(99_999_999_999), "s32"},
		{money.FormatIDR(-money.MaxAmount), "s28"},
		{money.FormatIDR(2_999_999_999_997), "s28"},
	} {
		if got := heroSizeClass(c.figure); got != c.want {
			t.Errorf("heroSizeClass(%q) = %q, want %q", c.figure, got, c.want)
		}
	}
}

func TestReportFontLicenceShipsWithTheFonts(t *testing.T) {
	t.Parallel()
	b, err := fontFS.ReadFile("fonts/OFL.txt")
	if err != nil {
		t.Fatalf("OFL.txt is not embedded beside the fonts: %v", err)
	}
	if !bytes.Contains(b, []byte("SIL OPEN FONT LICENSE Version 1.1")) {
		t.Error("OFL.txt does not look like the SIL Open Font License 1.1")
	}
}

func TestPDFSafe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"Kas RT 05", "Kas RT 05"},
		{"Kas RT 05 \U0001F3E0", "Kas RT 05"},
		{"\U0001F3E0 Kas RT 05", "Kas RT 05"},
		{"Kas \U0001F3E0 RT 05", "Kas RT 05"},
		{"Kas \U0001F3E0\U0001F333 RT 05", "Kas RT 05"},
		{"Halal bihalal \U0001F319\uFE0F", "Halal bihalal"},
		{"Keluarga \U0001F468\u200D\U0001F469\u200D\U0001F467 Budi", "Keluarga Budi"},
		{"\U0001F3E0", ""},
		// Nothing dropped: the text comes back exactly, spaces and all.
		{"Rp50.000  tunai ", "Rp50.000  tunai "},
		{"Caf\u00e9 \u2615", "Caf\u00e9 \u2615"},
		// The font has no arrow: the PDF writes it in ASCII.
		{"Dipindah: Kas Utama \u2192 Duka", "Dipindah: Kas Utama -> Duka"},
		{"Dipindah: Kas \U0001F3E0 \u2192 Duka", "Dipindah: Kas -> Duka"},
	} {
		if got := pdfSafe(tc.in); got != tc.want {
			t.Errorf("pdfSafe(%q) = %q, want %q", tc.in, got, tc.want)
		}
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

// The fonts are read from the embedded files once per process, however many
// PDFs are drawn, concurrently or not (#520). The race detector is the other
// half of the test: the shared bytes are only read.
func TestPDFFontsAreReadOnceAcrossConcurrentRenders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("db.Open() = %v, want no error", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := db.Up(ctx, sqlDB, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("db.Up() = %v, want no error", err)
	}
	l := ledger.New(sqlDB)
	res, err := l.SetUpFund(ctx, ledger.SetUpFundParams{
		FundName: "Kas RT 05",
		Accounts: []ledger.AccountInput{{Kind: "cash", Name: "Tunai"}},
	})
	if err != nil {
		t.Fatalf("SetUpFund() = %v, want no error", err)
	}

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, _, err := PDF(ctx, l, res.Fund.ID, ""); err != nil {
				t.Errorf("PDF() = %v, want no error", err)
			}
		})
	}
	wg.Wait()
	if got := pdfFontReads.Load(); got != 1 {
		t.Errorf("font files read %d times, want 1", got)
	}
}
