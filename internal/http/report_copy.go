package http

import (
	"strconv"
	"time"
)

// reportCopy is every Indonesian string the public report prints - the Go
// half of ADR-014, the way web/src/copy/id.ts is the SPA's. One place, so a
// future locale is one more value of this type rather than a hunt through a
// template. Wording that already exists in the app is reused verbatim
// (balanceHeading, lastChecked, matched, purposeBreakdownHeading), so a
// neighbour and the treasurer read the same words for the same thing.
//
// Source files are ASCII (CLAUDE.md rule 10): a character beyond it is a \u
// escape here, never a raw byte.
type reportCopy struct {
	// Title is the <title>: the fund's name, then what the page is.
	Title func(fundName string) string

	Eyebrow       string
	AsOf          func(date string) string
	BalanceLabel  string
	LastChecked   func(date string) string
	Matched       string
	Discrepancy   func(amount string) string
	NeverChecked  string
	PurposesLabel string

	MonthLabel  string
	MonthSubmit string
	MonthPrev   string
	MonthNext   string

	Empty string

	// The transactions section (#374).
	TransactionsLabel string
	FilterPurpose     string
	FilterMember      string
	FilterDirection   string
	FilterAll         string
	DirectionIn       string
	DirectionOut      string
	FilterSubmit      string
	TotalIn           string
	TotalOut          string
	TotalNet          string
	HasReceipt        string
	NoRows            string

	// Row labels: the Go half of web/src/copy/id.ts's rowLabels (#257), see
	// report_labels.go. A location is never named, so Saldo awal and
	// Penyesuaian stand alone.
	RowDues                 func(period, member string) string
	RowDuesReversal         func(period, member string) string
	RowContributionReversal func(member string) string
	RowOpening              string
	RowReconciliationFix    string
	RowMoved                func(from, to string) string

	NotFoundTitle string
	NotFoundBody  string

	// Months are January through December, for "2 Oktober 2026" and the
	// month selector's "Oktober 2026".
	Months [12]string
}

var reportText = reportCopy{
	Title:         func(fundName string) string { return fundName + " - Laporan kas" },
	Eyebrow:       "Laporan kas",
	AsOf:          func(date string) string { return "per " + date },
	BalanceLabel:  "Saldo kas",
	LastChecked:   func(date string) string { return "Terakhir dicek " + date },
	Matched:       "Kas dan catatan sudah cocok.",
	Discrepancy:   func(amount string) string { return "Ada selisih " + amount + "." },
	NeverChecked:  "Belum pernah dicek.",
	PurposesLabel: "Saldo per pos",

	MonthLabel:  "Bulan",
	MonthSubmit: "Lihat",
	MonthPrev:   "\u2039 Bulan sebelumnya",
	MonthNext:   "Bulan berikutnya \u203a",

	Empty: "Belum ada transaksi yang dicatat di kas ini.",

	TransactionsLabel: "Transaksi",
	FilterPurpose:     "Pos",
	FilterMember:      "Anggota",
	FilterDirection:   "Jenis",
	FilterAll:         "Semua",
	DirectionIn:       "Uang masuk",
	DirectionOut:      "Uang keluar",
	FilterSubmit:      "Tampilkan",
	TotalIn:           "Total masuk",
	TotalOut:          "Total keluar",
	TotalNet:          "Bersih",
	HasReceipt:        "Ada nota",
	NoRows:            "Tidak ada transaksi yang cocok bulan ini.",

	RowDues:                 func(period, member string) string { return period + " \u00b7 " + member },
	RowDuesReversal:         func(period, member string) string { return "Pembatalan \u00b7 " + period + " \u00b7 " + member },
	RowContributionReversal: func(member string) string { return "Pembatalan \u00b7 " + member },
	RowOpening:              "Saldo awal",
	RowReconciliationFix:    "Penyesuaian",
	RowMoved:                func(from, to string) string { return "Dipindah: " + from + " \u2192 " + to },

	NotFoundTitle: "Laporan tidak ditemukan",
	NotFoundBody:  "Tautan laporan ini tidak berlaku. Minta tautan terbaru ke bendahara.",

	Months: [12]string{
		"Januari", "Februari", "Maret", "April", "Mei", "Juni",
		"Juli", "Agustus", "September", "Oktober", "November", "Desember",
	},
}

// reportMonthLayout is "YYYY-MM", the ?month= value and the ledger's own.
const reportMonthLayout = "2006-01"

// longDate renders "YYYY-MM-DD" as "2 Oktober 2026", the way the app's date
// field reads. An unparseable value comes back as given:
// the ledger only ever hands this a valid date, and a raw ISO date on the
// page beats a blank.
func (c reportCopy) longDate(iso string) string {
	t, err := time.Parse(time.DateOnly, iso)
	if err != nil {
		return iso
	}
	return strconv.Itoa(t.Day()) + " " + c.Months[t.Month()-1] + " " + strconv.Itoa(t.Year())
}

// monthName renders "YYYY-MM" as "Oktober 2026".
func (c reportCopy) monthName(month string) string {
	t, err := time.Parse(reportMonthLayout, month)
	if err != nil {
		return month
	}
	return c.Months[t.Month()-1] + " " + strconv.Itoa(t.Year())
}
