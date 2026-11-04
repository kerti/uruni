package report

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/ledger"
)

// Copy is every Indonesian string the public report prints: the Go half
// of ADR-014, as web/src/copy/id.ts is the SPA's. One place, so a locale is
// one more value of this type, not a hunt through a template. Existing app
// wording is reused verbatim (balanceHeading, lastChecked, matched,
// purposeBreakdownHeading), so a neighbour and the treasurer read the same
// words for the same thing.
//
// Source files are ASCII (CLAUDE.md rule 10): a character beyond it is a \u
// escape here, never a raw byte.
type Copy struct {
	// Title is the <title>: the fund's name, then what the page is.
	Title func(fundName string) string

	Eyebrow      string
	AsOf         func(date string) string
	AsOfRunning  func(date string) string
	BalanceLabel string
	LastChecked  func(date string) string
	// NewEntriesSince follows the date when something was recorded after the
	// count (#734), so "cocok" is never read as a claim about now.
	NewEntriesSince string
	Matched         string
	Discrepancy     func(amount string) string
	NeverChecked    string
	PurposesLabel   string
	OwedLabel       string

	MonthLabel  string
	MonthSubmit string
	MonthPrev   string
	MonthNext   string

	// The period form (ADR-049): Bulanan or Rentang tanggal, and the range's two
	// days. RangeText names a range wherever it is named: the header line, the
	// PDF's statement; its two days are long dates, each with its year.
	// DuesMonthCaption captions the dues section in a range, which reads the
	// month that holds the last day.
	PeriodLabel      string
	PeriodMonth      string
	PeriodRange      string
	RangeFrom        string
	RangeTo          string
	RangeText        func(from, to string) string
	DuesMonthCaption func(month string) string

	Empty string

	// The transactions section (#374).
	TransactionsLabel string
	FilterPurpose     string
	FilterMember      string
	FilterGroup       string
	GroupByKind       string
	GroupByMonth      string
	GroupOther        string
	NoRowsIn          string
	NoRowsOut         string
	FilterAll         string
	DirectionIn       string
	DirectionOut      string
	FilterSubmit      string
	TotalIn           string
	TotalOut          string
	WalkStart         func(date string) string
	WalkEnd           func(date string) string
	WalkMoved         string
	HasReceipt        string
	NoRows            string
	NoRowsRange       string

	// The dues section (#375). The four statuses are web/src/copy/id.ts's
	// dues.statuses verbatim; the filter offers three, "Lunas" covering paid in
	// advance. No arrears wording here (ADR-035).
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

	// The twelve-month dues grid (ADR-044). The status words are the dues
	// section's own above. GridCellLabel is the hidden per-cell label; GridAsOf
	// the caption on a past month, since the grid is read as of today.
	GridHeading   string
	GridRange     func(from, to string) string
	GridAsOf      func(date string) string
	GridNone      string
	GridCellLabel func(month, status string) string
	// MonthHeads are the grid's three-letter column heads, January first.
	MonthHeads [12]string

	// The envelopes section (#338). All but EnvelopeGiven is the envelope screen's
	// wording from web/src/copy/id.ts (incidentals.heading, detail.collectedLabel,
	// status, participation), verbatim; EnvelopeGiven is the report's: the screen
	// shows no count.
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

	// Row labels: the Go half of web/src/copy/id.ts's rowLabels (#257); see
	// report_labels.go. A location is never named, so Saldo awal and Penyesuaian
	// stand alone. They also name the walk's lines (ADR-038), so a line and the
	// rows it sums share a name.
	RowDues                 func(period, member string) string
	RowDuesReversal         func(period, member string) string
	RowContributionReversal func(member string) string
	// RowReversal labels a reversal that names no member (ADR-042), stating the
	// original's date (already long-formatted).
	RowReversal func(date string) string
	// RowReversed is the mark on a row something reverses; RowPartlyReversed on a
	// dues payment only some of whose months are reversed (data from before
	// ADR-048).
	RowReversed       string
	RowPartlyReversed string
	RowOpening        string
	RowAdjustment     string
	RowMoved          func(from, to string) string

	// A row's kind (#257's kind words, as Riwayat's screen reader hears them):
	// the PDF's Jenis column, and the page's sr-only word beside the kind icon.
	// An ordinary row is Masuk or Keluar; Saldo awal and Penyesuaian reuse the
	// row labels above.
	KindDues string
	// RowAnonymous labels a contribution nobody put a name to
	// (rowLabels.contribution.anonymous).
	RowAnonymous     string
	KindContribution string
	KindSettlement   string
	KindReversal     string
	KindMoved        string
	KindIn           string
	KindOut          string

	// The monthly PDF (#378): the page footer's link and the words the statement
	// adds. Section headings and labels already on the page are reused.
	PDFDownload  string
	PDFFilename  func(month string) string
	PDFStatement func(month string) string
	// PDFFilenameRange takes the range's two ISO days; PDFStatementRange the
	// range text.
	PDFFilenameRange  func(from, to string) string
	PDFStatementRange func(rangeText string) string
	PDFSummary        string
	PDFColDate        string
	PDFColKind        string
	PDFColNote        string
	PDFColAmount      string
	PDFColMember      string
	PDFColStatus      string
	PDFNoRows         string
	PDFNoRowsRange    string
	PDFPage           func(page, pages string) string
	PDFFooterSource   func(fundName string) string

	NotFoundTitle string
	NotFoundBody  string

	// The 429 page (#520): "Laporan sedang ramai dibuka. Coba lagi sebentar
	// lagi, ya." in two sentences, a heading and a line.
	RateLimitedTitle string
	RateLimitedBody  string

	// Months are January through December, for "2 Oktober 2026" and the
	// month selector's "Oktober 2026".
	Months [12]string
	// MonthsShort are the three-letter months of a multi-month payment's row
	// ("Mei-Jun, Okt 2026"), as the app's formatDuesPeriods prints them
	// (ADR-048).
	MonthsShort [12]string
}

// Text is the report's copy, the only value of Copy.
var Text = Copy{
	Title:           func(fundName string) string { return fundName + " - Laporan kas" },
	Eyebrow:         "Laporan kas",
	AsOf:            func(date string) string { return "per " + date },
	AsOfRunning:     func(date string) string { return "Bulan berjalan \u00b7 per " + date },
	BalanceLabel:    "Saldo kas",
	LastChecked:     func(date string) string { return "Terakhir dicek " + date + "." },
	NewEntriesSince: "Ada catatan baru sejak itu.",
	Matched:         "Kas dan catatan cocok saat dicek.",
	Discrepancy:     func(amount string) string { return "Ada selisih " + amount + "." },
	NeverChecked:    "Belum pernah dicek.",
	OwedLabel:       "Talangan belum dibayar",
	PurposesLabel:   "Saldo per pos",

	MonthLabel:  "Bulan",
	MonthSubmit: "Lihat",
	MonthPrev:   "\u2039 Bulan sebelumnya",
	MonthNext:   "Bulan berikutnya \u203a",

	PeriodLabel:      "Periode",
	PeriodMonth:      "Bulanan",
	PeriodRange:      "Rentang tanggal",
	RangeFrom:        "Dari",
	RangeTo:          "Sampai",
	RangeText:        func(from, to string) string { return from + " \u2013 " + to },
	DuesMonthCaption: func(month string) string { return "Iuran bulan " + month },

	Empty: "Belum ada transaksi yang dicatat di kas ini.",

	TransactionsLabel: "Transaksi",
	FilterPurpose:     "Pos",
	FilterMember:      "Anggota",
	FilterGroup:       "Kelompokkan",
	GroupByKind:       "Per jenis",
	GroupByMonth:      "Per bulan",
	GroupOther:        "Lainnya",
	NoRowsIn:          "Tidak ada uang masuk.",
	NoRowsOut:         "Tidak ada uang keluar.",
	FilterAll:         "Semua",
	DirectionIn:       "Uang masuk",
	DirectionOut:      "Uang keluar",
	FilterSubmit:      "Tampilkan",
	TotalIn:           "Total masuk",
	TotalOut:          "Total keluar",
	WalkStart:         func(date string) string { return "Saldo " + date },
	WalkEnd:           func(date string) string { return "Saldo " + date },
	WalkMoved:         "Dipindah",
	HasReceipt:        "Ada nota",
	NoRows:            "Tidak ada transaksi yang cocok bulan ini.",
	NoRowsRange:       "Tidak ada transaksi yang cocok pada rentang ini.",

	DuesLabel:         "Status iuran",
	DuesFilter:        "Status iuran",
	DuesTier:          "Kategori",
	DuesOwed:          "Iuran",
	DuesPaid:          "Dibayar",
	DuesNoRows:        "Tidak ada anggota yang cocok bulan ini.",
	DuesPaidThrough:   func(period string) string { return "Sudah dibayar sampai " + period },
	DuesUnpaid:        "Belum bayar",
	DuesPartial:       "Bayar sebagian",
	DuesPaidStatus:    "Lunas",
	DuesPaidInAdvance: "Lunas \u00b7 sudah bayar di muka",

	GridHeading:   "12 bulan",
	GridRange:     func(from, to string) string { return from + " \u2013 " + to },
	GridAsOf:      func(date string) string { return "Status per " + date },
	GridNone:      "Tidak ada iuran",
	GridCellLabel: func(month, status string) string { return month + ": " + status },
	MonthHeads:    [12]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"},

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
	RowReversal:             func(date string) string { return "Pembatalan \u00b7 transaksi " + date },
	RowReversed:             "Sudah dibatalkan",
	RowPartlyReversed:       "Sebagian dibatalkan",
	RowOpening:              "Saldo awal",
	RowAdjustment:           "Penyesuaian",
	RowMoved:                func(from, to string) string { return "Dipindah: " + from + " \u2192 " + to },

	KindDues:         "Iuran",
	RowAnonymous:     "Tanpa nama",
	KindContribution: "Sumbangan",
	KindSettlement:   "Talangan",
	KindReversal:     "Pembatalan",
	KindMoved:        "Dipindah",
	KindIn:           "Masuk",
	KindOut:          "Keluar",

	PDFDownload:       "Unduh PDF",
	PDFFilename:       func(month string) string { return "laporan-kas-" + month + ".pdf" },
	PDFStatement:      func(month string) string { return "Laporan kas bulan " + month },
	PDFFilenameRange:  func(from, to string) string { return "laporan-kas-" + from + "_" + to + ".pdf" },
	PDFStatementRange: func(rangeText string) string { return "Laporan kas " + rangeText },
	PDFColDate:        "Tanggal",
	PDFColKind:        "Jenis",
	PDFColNote:        "Keterangan",
	PDFColAmount:      "Jumlah",
	PDFColMember:      "Anggota",
	PDFColStatus:      "Status",
	PDFSummary:        "Ringkasan",
	PDFNoRows:         "Tidak ada transaksi bulan ini.",
	PDFNoRowsRange:    "Tidak ada transaksi pada rentang ini.",
	PDFPage:           func(page, pages string) string { return "Halaman " + page + " dari " + pages },
	PDFFooterSource:   func(fundName string) string { return fundName + " \u00b7 Laporan kas" },

	NotFoundTitle: "Laporan tidak ditemukan",
	NotFoundBody:  "Tautan laporan ini tidak berlaku. Minta tautan terbaru ke bendahara.",

	RateLimitedTitle: "Laporan sedang ramai dibuka",
	RateLimitedBody:  "Coba lagi sebentar lagi, ya.",

	Months: [12]string{
		"Januari", "Februari", "Maret", "April", "Mei", "Juni",
		"Juli", "Agustus", "September", "Oktober", "November", "Desember",
	},
	MonthsShort: [12]string{
		"Jan", "Feb", "Mar", "Apr", "Mei", "Jun",
		"Jul", "Agu", "Sep", "Okt", "Nov", "Des",
	},
}

// reportMonthLayout is "YYYY-MM", the ?month= value and the ledger's.
const reportMonthLayout = "2006-01"

// longDate renders "YYYY-MM-DD" as "2 Oktober 2026", as the app's date field
// reads. An unparseable value comes back as given: the ledger only hands this
// a valid date, and a raw ISO date beats a blank.
func (c Copy) longDate(iso string) string {
	t, err := time.Parse(time.DateOnly, iso)
	if err != nil {
		return iso
	}
	return strconv.Itoa(t.Day()) + " " + c.Months[t.Month()-1] + " " + strconv.Itoa(t.Year())
}

// rangeText is a range of two "YYYY-MM-DD" days as it is named on the page and
// in the PDF: "15 Oktober 2026 \u2013 3 November 2026".
func (c Copy) rangeText(from, to string) string {
	// One day is that day, once: "4 November 2026", not twice across a dash.
	if from == to {
		return c.longDate(from)
	}
	return c.RangeText(c.longDate(from), c.longDate(to))
}

// monthName renders "YYYY-MM" as "Oktober 2026".
func (c Copy) monthName(month string) string {
	t, err := time.Parse(reportMonthLayout, month)
	if err != nil {
		return month
	}
	return c.Months[t.Month()-1] + " " + strconv.Itoa(t.Year())
}

// PeriodHyphen joins the two ends of a range of months. It is U+2011, a
// non-breaking hyphen, so a narrow screen never wraps "Jan-" from "Jul 2027";
// the PDF, whose font is not trusted with it and whose column does not wrap a
// label mid-range, writes a plain hyphen (pdfText).
const PeriodHyphen = "\u2011"

// periodsText renders the months of a dues payment ("YYYY-MM", any order,
// repeats ignored) for its row (ADR-048). One month reads as monthName does
// ("Oktober 2026"). Several read in three-letter months, consecutive months
// joined as a range by PeriodHyphen (shown as "-" below), runs separated by
// ", ", and the year after a run when the next run starts in another year or
// none follows. A range may cross the new year, and then names both years:
//
//	Mei-Jun, Okt 2026
//	Nov 2025-Jan 2026
//	Agu, Okt 2026-Jul 2027
//	Jan, Mar 2026
//
// A value that is not a month comes back as given, as monthName does.
func (c Copy) periodsText(periods []string) string {
	var months []int // year*12 + month index
	seen := map[int]bool{}
	for _, period := range periods {
		t, err := time.Parse(reportMonthLayout, period)
		if err != nil {
			return strings.Join(periods, ", ")
		}
		m := t.Year()*12 + int(t.Month()) - 1
		if !seen[m] {
			seen[m] = true
			months = append(months, m)
		}
	}
	sort.Ints(months)
	switch len(months) {
	case 0:
		return ""
	case 1:
		return c.Months[months[0]%12] + " " + strconv.Itoa(months[0]/12)
	}

	type run struct{ from, to int }
	var runs []run
	for i := 0; i < len(months); {
		j := i
		for j+1 < len(months) && months[j+1] == months[j]+1 {
			j++
		}
		runs = append(runs, run{months[i], months[j]})
		i = j + 1
	}

	var b strings.Builder
	for i, r := range runs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(c.MonthsShort[r.from%12])
		if r.from/12 != r.to/12 {
			b.WriteString(" " + strconv.Itoa(r.from/12))
		}
		if r.to != r.from {
			b.WriteString(PeriodHyphen + c.MonthsShort[r.to%12])
		}
		if i == len(runs)-1 || runs[i+1].from/12 != r.to/12 {
			b.WriteString(" " + strconv.Itoa(r.to/12))
		}
	}
	return b.String()
}

// duesStatus is the app's own word for a dues status.
func (c Copy) duesStatus(s ledger.DuesStatus) string {
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
func (c Copy) participationStatus(s ledger.ParticipationState) string {
	switch s {
	case ledger.ParticipationSudah:
		return c.ParticipationGiven
	case ledger.ParticipationKurang:
		return c.ParticipationUnder
	default:
		return c.ParticipationNot
	}
}

// gridWord is the word for one grid cell: the dues section's own status words,
// plus the grid's for no obligation.
func (c Copy) gridWord(cell ledger.DuesGridCell) string {
	switch cell {
	case ledger.DuesCellPaid:
		return c.DuesPaidStatus
	case ledger.DuesCellPartial:
		return c.DuesPartial
	case ledger.DuesCellUnpaid:
		return c.DuesUnpaid
	default:
		return c.GridNone
	}
}
