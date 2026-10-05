# ADR-037 — The report shows each month as it ended

**Status:** Accepted · implemented at M7 ([#408](https://github.com/kerti/uruni/issues/408)) — supersedes [ADR-035](./035-public-report.md) on one point, named below · change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** ADR-035 made the report's header current whatever month was selected: a September page showed today's balance above September's rows. On the page that reads as "the fund now, then what happened in September". The PDF made the split visible: it is titled "Laporan kas bulan September 2026" and is printed and passed round the group chat as a monthly statement, so a copy made in December showed December's balance under a September title, and nothing on it said why. A monthly report, to the people who read one, states the fund as the month left it - which is also how Balances v2 treats its dashboard and its downloadable monthly report. Raised by the maintainer while reviewing #406, ruled 2026-10-05.

## Decision

**The report, page and PDF alike, states every figure for its selected month** - the month as it ended, or the running month as it stands.

- **A past month** is as of its **last day**: Saldo kas, Saldo per pos, the latest *cek kas* and whether it matched, Talangan belum dibayar, and each envelope card (saldo, terkumpul, who gave). The date line reads "per 30 September 2026".
- **The running month** - the one a bare link opens - is as of today, and its date line says so: "Bulan berjalan · per 5 Oktober 2026". A neighbour who opens the link to see the fund now still does.
- **The month picker moves to the top**, above the summary, so everything beneath it visibly belongs to the month it names.

**How "as of" is read.** Every figure comes from the ledger as it stands, bounded:
- Balances sum rows whose `occurred_on` is on or before the month's last day. A row recorded later but dated inside the month counts - the report states what the ledger now says the month ended with, not what an earlier print said. That is the same rule every balance in Uruni already follows.
- A *cek kas* is bounded by `performed_at` before the first instant of the next month in **Asia/Jakarta**. A count is never edited (ADR-024), so the latest one before the bound, and the lines still open at it, are exactly what was true then.
- An envelope belongs to Saldo per pos if it was open at the end of the last day: opened on or before it, and not closed on or before it (closing rolls the leftover out that day). A pass-through belongs once it was created (`created_at`, its only date). **Any purpose holding money at the bound is shown regardless**, so the lines always sum to Saldo kas: back-filling - setting up mid-life, or a Titipan created in October for money that arrived in September - puts rows before a purpose's own dates. An envelope card shows no closing date it did not yet have.
- A talangan claim is owed if it was incurred by then, not paid out by then, and not waived by then.
- The running month passes no bound at all, so its figures are, by construction, the ones Beranda shows.

**What stays as it was.** Iuran keeps ADR-035's reading: the status of that month's dues as of today. A member who paid September late shows *Lunas* on September, so the public page never keeps calling someone unpaid after they have paid. The month's transaction rows were always month-scoped.

## Consequences

- "Snapshot" stays the word for a *cek kas* (CONTEXT.md); a month on the report is "as it ended", never a snapshot.
- One assembly still feeds both renderers (ADR-035); each bounded read is a sibling query taking a nullable bound, or an existing query that gained one, with NULL meaning unbounded so no other caller changes.
- **Accepted inexactness in Talangan belum dibayar.** `waived_on` holds only the latest waive, so a claim waived and later un-waived reads as owed in the months it was waived; and a claim corrected in place counts at its current amount. Both are rare, and both err towards showing money owed.
- A past month's figures can still move if the treasurer later records something dated inside it - by design, as above.

**Superseded in ADR-035:** the header item's "as of today like the balance, whatever month is shown", and the page order that put the month picker below the summary.
