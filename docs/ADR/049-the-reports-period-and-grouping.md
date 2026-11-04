# ADR-049 - The report's period and grouping

**Status:** `draft` · M11 · decided with the maintainer 2026-11-04 · supersedes in part [ADR-035](./035-public-report.md) (the page's direction filter; and, with the second slice, the month as the only unit) and [ADR-038](./038-the-month-as-a-balance-walk.md) (the report's direction filter) · [ADR index](./README.md)

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

**The period - second slice, not yet implemented.** *Bulanan* (the default, as today) or *Rentang tanggal*: *Dari* and *Sampai*, whole days, inclusive, in the fund's zone (ADR-041).

- Bounds: *Dari* no earlier than the first transaction, *Sampai* no later than today. Dates given the wrong way round are swapped; anything invalid reads as the default, quietly, as an invalid `?month=` does - the report is read-only and nothing is recorded.
- The walk runs from the balance at the end of the day before *Dari* to the balance at the end of *Sampai*. The header's balance, the cek kas banner, *Saldo per pos*, what is owed to members and the envelopes read as of *Sampai* - ADR-037's "as it ended", for the period's end.
- The dues status list and the twelve-month grid are monthly by nature: in a range they read the month that holds *Sampai*, captioned so.
- The page switches between the month select and the two date fields with a few lines in its one script; without JavaScript both are shown. The PDF and the app's dialog offer the same choice.

**Consequences.**

- Every later change to the report is checked in both groupings, on the page and in the PDF.
- `group=month` is a parameter in shared links from now on; removing it later would change what old links show.
- The PRD's report section names the grouping and drops the income/expense filter (this slice), and names the date range (second slice).
- Riwayat and the public report now differ: Riwayat filters by direction, the report groups by it. Each fits its reader - the treasurer looking for one row, the group reading the period.
