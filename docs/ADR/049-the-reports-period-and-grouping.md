# ADR-049 - The report's period and grouping

**Status:** Accepted · implemented at M11 in two slices ([#799](https://github.com/kerti/uruni/pull/799), the grouping; then the date range) · decided with the maintainer 2026-11-04 · supersedes in part [ADR-035](./035-public-report.md) (the page's direction filter; the month as the only unit), [ADR-037](./037-report-shows-the-month-as-it-ended.md) (the selected month as the only period) and [ADR-038](./038-the-month-as-a-balance-walk.md) (the report's direction filter) · change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** A treasurer's group - an office fund, close to the persona `Positioning.md` was validated with - asked two things of the report. First, to see money in apart from money out, so they can read what the fund was spent on this month or a past one (now that #798 makes any month's PDF one tap away). Second, a period that is not a calendar month: "15 Oktober sampai 3 November", a pay cycle or the time since the last meeting.

The report was one month, one list, in and out and moves mixed. The page had a *Jenis* filter that narrowed the list to one side, but a reader had to know it was there, and the PDF - the file that is forwarded to the group chat - ignores filters, so it could never show the spending alone.

**The test every option here had to pass** (maintainer, 2026-11-04): *what actual user need does it serve?* - real (someone has it), distinct (the default and the existing features cannot already answer it) and in scope (PRD section 4). "More options cover more use cases" was weighed and rejected as a reason: it never says when to stop, and Uruni is meant to be small, opinionated and finished. Each option below is here for the question it answers.

**Decision.**

**The list has two groupings (`?group=`), on the page and in the PDF alike.**

- **Per jenis** - the default; its value is empty, so it stays out of a shared URL, as *Semua* does. Three blocks: **Uang masuk**, **Uang keluar** and **Lainnya**. A row's block is its walk line (ADR-038, ADR-042): a reversal sits with the side it cancels, counting there as a minus; an opening, a *Penyesuaian* and a *Dipindah* row are neither in nor out, so they are *Lainnya*. Uang masuk and Uang keluar are always shown - "nothing went out" is an answer, said as *Tidak ada uang keluar.* - and each heading carries its subtotal, which is the walk's *Total masuk* / *Total keluar*: the ledger's own figure for exactly those rows, so the blocks and the walk cannot disagree. Lainnya appears only when it holds a row, and has no subtotal (its rows mix signs). *Answers: what came in, what went out, and what the money was spent on.*
- **Per bulan** - the ledger's order, with a heading at each month the rows fall in and no subtotal: per-month in and out figures would be a second walk, and every figure on the report has to reconcile. On a one-month report it is the single mixed list the page used to show. *Answers: what happened, in order, across a period that crosses months* - a question the date range below creates.

The page's PDF link carries the grouping, so the file is laid out as the page is; filters stay the page's alone (ADR-035). The app's **Unduh PDF** dialog offers the same choice (*Kelompokkan*: *Per jenis* / *Per bulan*) beside the month.

**The page's direction filter is retired.** Per jenis shows both of its answers at once; keeping it would be two ways to do one thing. An old link's `?dir=` is ignored and shows the whole month, by kind - never an error. Riwayat keeps its own direction filter: it is the treasurer's working list, and ADR-038's rule that a direction filter walks only *Total masuk* and *Total keluar* still holds there.

**Rejected:** spending categories, or a per-pos breakdown of spending - the analytics PRD section 4 rules out; what the money went to is each row's pos and note. A view switch with no question of its own ("someone might prefer it").

**The period: *Periode* - *Bulanan* (the default, as before) or *Rentang tanggal*.** A range is `?from=YYYY-MM-DD&to=YYYY-MM-DD`: whole days, inclusive, in the fund's zone (ADR-041). *Answers: how did the fund do over a period that is not a calendar month* - a pay cycle, or since the last meeting.

- **Bounds:** *Dari* no earlier than the first transaction's day, *Sampai* no later than today; each is clamped into that span, then swapped if backwards. Only both days valid make a range; anything else - one day, a malformed one, `period=range` alone, or *Bulanan* chosen beside them - reads as the month, quietly, as an invalid `?month=` does: the report is read-only and nothing is recorded. The ledger itself refuses a one-sided, backwards or future-starting range, and pulls a *Sampai* past today back to today, so it never relies on the edge.
- **One period drives both:** a month is the period from its first to its last day, so the month and the range share every step of the assembly, and Riwayat's month summary (`MonthWalk`) and Beranda's month in/out run through the same code unchanged.
- **The walk** runs from the balance at the end of the day before *Dari* to the balance at the end of *Sampai*. A range that reaches today is *running*, read unbounded like the running month, so its figures equal Beranda's. Otherwise every header figure - Saldo kas, the cek kas banner (counts before the first instant of the day after *Sampai*), Saldo per pos, what is owed to members - and the envelopes read as of *Sampai*: ADR-037's "as it ended", for the period's end. An envelope is listed when it was open on any day of the period.
- **The dues list and the twelve-month grid are monthly by nature:** in a range they read the month that holds *Sampai*, and the list is captioned "Iuran bulan November 2026".
- **Wording:** the header line in a range is the range itself, "15 Oktober 2026 – 3 November 2026" (an en dash, both years), a one-day range its day once; the PDF is "Laporan kas 15 Oktober 2026 – 3 November 2026", saved as `laporan-kas-2026-10-15_2026-11-03.pdf`, with no second date line beneath a title that already says it. An empty range says "Tidak ada transaksi yang cocok pada rentang ini."
- **The page:** Periode switches between the month select and two native date fields (*Dari*, *Sampai*) with a few lines in the page's one script, and never submits by itself in that direction: the days submit with *Lihat*, opening on the 1st of this month through today. Back to *Bulanan* is a whole choice and shows that month at once. Only the fields in use reach the URL - a month, or from and to, never `period`. The month steps (*Bulan sebelumnya / berikutnya*) show only in month mode. Without JavaScript every field shows; the date fields carry no value in month mode, so submitting a month never turns into a range, and from a range, *Bulanan* with *Lihat* goes back to the month (a `period` that is present and not `range` overrides the dates; the script never sends it).
- **The app's Unduh PDF dialog** asks in the page's order - Periode, then Bulan or Dari and Sampai, then Kelompokkan - each a labelled field like every dialog's; each date field stops at the other, so the range cannot be backwards. `GET /api/report/months` adds `first_day` and `today` for them.

**Consequences.**

- Every later change to the report is checked in both groupings, on the page and in the PDF.
- `group=month` is a parameter in shared links from now on; removing it later would change what old links show.
- The PRD's report section names the grouping, the date range, and no income/expense filter.
- A range is a parameter pair in shared links too, with the same caution as `group=month`.
- Riwayat and the public report now differ: Riwayat filters by direction, the report groups by it. Each fits its reader - the treasurer looking for one row, the group reading the period.
