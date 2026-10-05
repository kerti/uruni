package http

import (
	"strconv"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
)

// TestReportRowLabelsMatchTheSPA pins the Go port of the row labels against
// the strings web/src/copy/id.ts's rowLabels (with formatPeriod, "2026-06" ->
// "Juni 2026") produces. The expected values are written out, not derived, so
// editing either side alone fails here.
//
// Where the report deliberately differs from Riwayat (no location suffix on
// "Saldo awal" and "Penyesuaian"; one "Dipindah: ..." wording for every
// purpose move; "Penyesuaian" on a standalone adjustment, which Riwayat leaves
// unlabelled) the expectation says so.
func TestReportRowLabelsMatchTheSPA(t *testing.T) {
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
		// rowLabels.contribution.text
		{"contribution", ledger.ReportEntry{Kind: "normal", MemberName: str("Budi")}, "Budi"},
		// rowLabels.settlement.text
		{"settlement", ledger.ReportEntry{Kind: "reimbursement", MemberName: str("Citra")}, "Citra"},
		// rowLabels.opening.text, minus the location (report hides it)
		{"opening", ledger.ReportEntry{Kind: "opening"}, "Saldo awal"},
		// rowLabels.reconciliationFix.text, minus the location
		{"reconciliation fix", ledger.ReportEntry{Kind: "adjustment", IsReconciliationFix: true}, "Penyesuaian"},
		// plain rows carry no label
		{"own normal row", ledger.ReportEntry{Kind: "normal"}, ""},
		// Deliberately not the SPA's blank (ADR-038): a standalone adjustment is
		// named for its line in the month's walk, as a reconciliation fix is.
		{"own adjustment", ledger.ReportEntry{Kind: "adjustment"}, "Penyesuaian"},
	}
	for _, tt := range tests {
		if got := reportText.entryLabel(tt.entry); got != tt.want {
			t.Errorf("%s: label = %q, want %q", tt.name, got, tt.want)
		}
	}

	// rowLabels.transferPurpose*.text is `${from} \u2192 ${to}`; the report
	// prefixes "Dipindah: " to every purpose move (ADR-035, #374).
	got := reportText.moveLabel(ledger.ReportMove{FromPurposeName: "Duka", ToPurposeName: "Kas Utama"})
	if want := "Dipindah: Duka \u2192 Kas Utama"; got != want {
		t.Errorf("move label = %q, want %q", got, want)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
