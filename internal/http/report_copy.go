package http

import (
	"strconv"
	"time"

	"github.com/kerti/uruni/internal/ledger"
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

	// The dues section (#375). The four statuses are web/src/copy/id.ts's
	// dues.statuses verbatim, so the report and the app read the same words;
	// the filter offers three of them, "Lunas" covering paid in advance too.
	// There is deliberately no arrears wording here (ADR-035).
	DuesLabel         string
	DuesFilter        string
	DuesTier          string
	DuesOwed          string
	DuesPaid          string
	DuesNoRows        string
	DuesPaidThrough   func(period string) string
	DuesUnpaid        string
	DuesPartial       string
	DuesPaidStatus    string
	DuesPaidInAdvance string

	// The envelopes section (#338). Everything but EnvelopeGiven is the
	// envelope screen's own wording from web/src/copy/id.ts (incidentals.
	// heading, detail.collectedLabel, status, participation), verbatim.
	// EnvelopeGiven is the report's own: the screen shows no count.
	EnvelopesLabel     string
	EnvelopeCollected  string
	EnvelopeOpen       string
	EnvelopeClosed     string
	EnvelopeGiven      func(given, expected int) string
	EnvelopeRecipients func(names string) string
	EnvelopeNoneExpect string
	EnvelopeOthers     string
	ParticipationGiven string
	ParticipationNot   string
	ParticipationUnder string

	// Row labels: the Go half of web/src/copy/id.ts's rowLabels (#257), see
	// report_labels.go. A location is never named, so Saldo awal and
	// Penyesuaian stand alone.
	RowDues                 func(period, member string) string
	RowDuesReversal         func(period, member string) string
	RowContributionReversal func(member string) string
	RowOpening              string
	RowReconciliationFix    string
	RowMoved                func(from, to string) string

	// The monthly PDF (#378): the link in the page's footer and the words the
	// statement adds. Every section heading and label the page already has is
	// reused, so the file and the page read the same.
	PDFDownload     string
	PDFFilename     func(month string) string
	PDFStatement    func(month string) string
	PDFSummary      string
	PDFColDate      string
	PDFColNote      string
	PDFColAmount    string
	PDFColMember    string
	PDFColStatus    string
	PDFNoRows       string
	PDFPage         func(page, pages string) string
	PDFFooterSource func(fundName string) string

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

	DuesLabel:         "Status iuran",
	DuesFilter:        "Status iuran",
	DuesTier:          "Golongan",
	DuesOwed:          "Iuran",
	DuesPaid:          "Dibayar",
	DuesNoRows:        "Tidak ada anggota yang cocok bulan ini.",
	DuesPaidThrough:   func(period string) string { return "Sudah dibayar sampai " + period },
	DuesUnpaid:        "Belum bayar",
	DuesPartial:       "Bayar sebagian",
	DuesPaidStatus:    "Lunas",
	DuesPaidInAdvance: "Lunas \u2014 sudah bayar di muka",

	EnvelopesLabel:    "Amplop",
	EnvelopeCollected: "Terkumpul",
	EnvelopeOpen:      "Berjalan",
	EnvelopeClosed:    "Ditutup",
	EnvelopeGiven: func(given, expected int) string {
		return strconv.Itoa(given) + " dari " + strconv.Itoa(expected) + " sudah menyumbang"
	},
	EnvelopeRecipients: func(names string) string { return "Untuk: " + names },
	EnvelopeNoneExpect: "Tidak ada anggota yang diharapkan menyumbang untuk amplop ini.",
	EnvelopeOthers:     "Sumbangan lain",
	ParticipationGiven: "Sudah menyumbang",
	ParticipationNot:   "Belum menyumbang",
	ParticipationUnder: "Kurang dari minimal",

	RowDues:                 func(period, member string) string { return period + " \u00b7 " + member },
	RowDuesReversal:         func(period, member string) string { return "Pembatalan \u00b7 " + period + " \u00b7 " + member },
	RowContributionReversal: func(member string) string { return "Pembatalan \u00b7 " + member },
	RowOpening:              "Saldo awal",
	RowReconciliationFix:    "Penyesuaian",
	RowMoved:                func(from, to string) string { return "Dipindah: " + from + " \u2192 " + to },

	PDFDownload:     "Unduh PDF",
	PDFFilename:     func(month string) string { return "laporan-kas-" + month + ".pdf" },
	PDFStatement:    func(month string) string { return "Laporan kas bulan " + month },
	PDFColDate:      "Tanggal",
	PDFColNote:      "Keterangan",
	PDFColAmount:    "Jumlah",
	PDFColMember:    "Anggota",
	PDFColStatus:    "Status",
	PDFSummary:      "Ringkasan",
	PDFNoRows:       "Tidak ada transaksi bulan ini.",
	PDFPage:         func(page, pages string) string { return "Halaman " + page + " dari " + pages },
	PDFFooterSource: func(fundName string) string { return fundName + " \u00b7 Laporan kas" },

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

// duesStatus is the app's own word for a dues status.
func (c reportCopy) duesStatus(s ledger.DuesStatus) string {
	switch s {
	case ledger.DuesStatusPartial:
		return c.DuesPartial
	case ledger.DuesStatusPaid:
		return c.DuesPaidStatus
	case ledger.DuesStatusPaidInAdvance:
		return c.DuesPaidInAdvance
	default:
		return c.DuesUnpaid
	}
}

// participationStatus is the app's own word for an expected member's standing.
func (c reportCopy) participationStatus(s ledger.ParticipationState) string {
	switch s {
	case ledger.ParticipationSudah:
		return c.ParticipationGiven
	case ledger.ParticipationKurang:
		return c.ParticipationUnder
	default:
		return c.ParticipationNot
	}
}
