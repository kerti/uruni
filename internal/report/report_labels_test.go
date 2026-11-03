package report

import (
	"testing"

	"github.com/kerti/uruni/internal/ledger"
)

// TestReportRowLabelsMatchTheSPA pins the Go port of the row labels against
// what web/src/copy/id.ts's rowLabels (with formatPeriod, "2026-06" -> "Juni
// 2026") produces. Expected values are written out, not derived, so editing
// either side alone fails here.
//
// Where the report deliberately differs from Riwayat (no location suffix on
// "Saldo awal" and "Penyesuaian"; one "Dipindah: ..." wording for every purpose
// move; "Penyesuaian" on a standalone adjustment, which Riwayat leaves
// unlabelled) the expectation says so.
func TestReportRowLabelsMatchTheSPA(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	tests := []struct {
		name  string
		entry ledger.ReportEntry
		want  string
	}{
		// rowLabels.dues.text
		{"dues", ledger.ReportEntry{Kind: "dues", MemberName: str("Ani"), DuesPeriod: str("2026-06")}, "Juni 2026 \u00b7 Ani"},
		// rowLabels.duesReversal.text
		{"dues reversal", ledger.ReportEntry{Kind: "adjustment", IsReversal: true, MemberName: str("Ani"), DuesPeriod: str("2026-06")}, "Pembatalan \u00b7 Juni 2026 \u00b7 Ani"},
		// rowLabels.contributionReversal.text
		{"contribution reversal", ledger.ReportEntry{Kind: "adjustment", IsReversal: true, MemberName: str("Budi")}, "Pembatalan \u00b7 Budi"},
		// ADR-042: the reversal of an unnamed row names nobody.
		{"unnamed reversal", ledger.ReportEntry{Kind: "adjustment", IsReversal: true, ReversesOccurredOn: str("2026-08-12")}, "Pembatalan \u00b7 transaksi 12 Agustus 2026"},
		// rowLabels.contribution.text
		{"contribution", ledger.ReportEntry{Kind: "normal", MemberName: str("Budi")}, "Budi"},
		// rowLabels.settlement.text
		{"settlement", ledger.ReportEntry{Kind: "reimbursement", MemberName: str("Citra")}, "Citra"},
		// rowLabels.opening.text, minus the location (report hides it)
		{"opening", ledger.ReportEntry{Kind: "opening"}, "Saldo awal"},
		// rowLabels.reconciliationFix.text, minus the location
		{"adjustment", ledger.ReportEntry{Kind: "adjustment"}, "Penyesuaian"},
		// plain rows carry no label
		{"own normal row", ledger.ReportEntry{Kind: "normal"}, ""},
		// Deliberately not the SPA's blank (ADR-038): a standalone adjustment is
		// named for its line in the month's walk, as a reconciliation fix is.
		{"own adjustment", ledger.ReportEntry{Kind: "adjustment"}, "Penyesuaian"},
	}
	for _, tt := range tests {
		if got := Text.entryLabel(tt.entry); got != tt.want {
			t.Errorf("%s: label = %q, want %q", tt.name, got, tt.want)
		}
	}

	// rowLabels.transferPurpose*.text is `${from} \u2192 ${to}`; the report
	// prefixes "Dipindah: " to every purpose move (ADR-035, #374).
	got := Text.moveLabel(ledger.ReportMove{FromPurposeName: "Duka", ToPurposeName: "Kas Utama"})
	if want := "Dipindah: Duka \u2192 Kas Utama"; got != want {
		t.Errorf("move label = %q, want %q", got, want)
	}
}

// Every row names its kind - the PDF's Jenis column, the page's icon and
// sr-only word - so a dues payment, a contribution and her own row read apart.
// The words are Riwayat's kind words (rowLabels.*.kind); an ordinary row is
// Masuk or Keluar, and a label that already starts with its kind is not read
// twice.
func TestReportRowKindTellsDuesFromContributionFromAnOrdinaryRow(t *testing.T) {
	t.Parallel()
	str := func(s string) *string { return &s }
	tests := []struct {
		name         string
		entry        ledger.ReportEntry
		icon, word   string
		spokenBeside bool
	}{
		{"dues", ledger.ReportEntry{Kind: "dues", MemberName: str("Ani"), DuesPeriod: str("2026-06")}, "dues", "Iuran", true},
		{"contribution", ledger.ReportEntry{Kind: "normal", MemberName: str("Budi")}, "contribution", "Sumbangan", true},
		{"settlement", ledger.ReportEntry{Kind: "reimbursement", MemberName: str("Citra")}, "settlement", "Talangan", true},
		{"reversal", ledger.ReportEntry{Kind: "adjustment", IsReversal: true, MemberName: str("Budi")}, "reversal", "Pembatalan", false},
		{"opening", ledger.ReportEntry{Kind: "opening"}, "opening", "Saldo awal", false},
		{"adjustment", ledger.ReportEntry{Kind: "adjustment"}, "adjustment", "Penyesuaian", false},
		{"own row in", ledger.ReportEntry{Kind: "normal", Direction: ledger.ReportDirectionIn}, "", "Masuk", false},
		{"own row out", ledger.ReportEntry{Kind: "normal", Direction: ledger.ReportDirectionOut}, "", "Keluar", false},
	}
	for _, tt := range tests {
		icon, word, spoken := Text.entryKind(tt.entry)
		if icon != tt.icon || word != tt.word || spoken != tt.spokenBeside {
			t.Errorf("%s: kind = (%q, %q, %v), want (%q, %q, %v)", tt.name, icon, word, spoken, tt.icon, tt.word, tt.spokenBeside)
		}
	}
}

// An unnamed gift into an amplop is a contribution, as Riwayat reads it: Tanpa
// nama under the kind icon, Sumbangan in the PDF. The same money into Kas
// Utama is her own Masuk.
func TestReportUnnamedGiftIntoAnEnvelopeIsASumbangan(t *testing.T) {
	t.Parallel()
	gift := func(purposeID int64) *ledger.ReportEntry {
		return &ledger.ReportEntry{Kind: "normal", Direction: ledger.ReportDirectionIn, Amount: 20_000, PurposeID: purposeID, PurposeName: "Pos"}
	}
	r := ledger.Report{
		Month:     "2026-11",
		Envelopes: []ledger.ReportEnvelope{{PurposeID: 7}},
		Rows: []ledger.ReportRow{
			{Date: "2026-11-02", Entry: gift(7)},
			{Date: "2026-11-02", Entry: gift(1)},
		},
	}
	rows := NewPage(r, false).Rows
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if got := rows[0]; got.Kind != Text.KindContribution || got.KindIcon != "contribution" || !got.KindSpoken || got.Label != Text.RowAnonymous {
		t.Errorf("gift into the amplop = (%q, %q, %v, %q), want a Sumbangan labelled %q", got.Kind, got.KindIcon, got.KindSpoken, got.Label, Text.RowAnonymous)
	}
	if got := rows[1]; got.Kind != Text.KindIn || got.KindIcon != "" || got.Label != "" {
		t.Errorf("money into Kas Utama = (%q, %q, %q), want a plain Masuk", got.Kind, got.KindIcon, got.Label)
	}
}
