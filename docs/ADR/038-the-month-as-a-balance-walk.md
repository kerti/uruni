# ADR-038 — The month as a balance walk

**Status:** Accepted · implemented at M7 ([#416](https://github.com/kerti/uruni/issues/416): #417, #418, #419) — supersedes [ADR-035](./035-public-report.md) on its in/out/net totals, [ADR-037](./037-report-shows-the-month-as-it-ended.md) on one point and [ADR-032](./032-two-level-navigation.md) on Beranda's order, all named below · change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** The report's month totals (ADR-035) were "money in, money out and net for whatever is filtered", summed by stored direction. That reads wrongly in three places. An **opening entry** - posted at setup, and again whenever a location is added with money already in it - counts as *Total masuk*, so the setup month shows the whole starting balance as income. A **reversal** (ADR-029) is stored `out`, so a cancelled dues payment shows as Rp X in *and* Rp X out, as if the fund had received and spent it. And a **penyesuaian** - money a *cek kas* found missing or extra, or a standalone adjustment - counts as income or spending, which it is not. Nothing on the page tied the month back to the balance either: a neighbour saw a total and three numbers and could not check one against the other. The maintainer asked for the month's in and out on Beranda, the page and the PDF, with the balance the month started and ended on (2026-10-05).

## Decision

**A month reads as a walk from the balance it started on to the balance it ended on**, and every posted row lands on exactly one line of it.

```
Saldo <last day of previous month>
+ Saldo awal      openings dated in the month; shown only when non-zero
+ Total masuk     income, net of reversals
- Total keluar    spending
+/- Penyesuaian   every adjustment that is not a reversal; shown only when non-zero
+/- Dipindah      purpose filter only: that pos's reclass legs, net
= Saldo <last day of the month>    (running month: today)
```

**Where each row lands.** One switch on the row, total by construction:

| Row | Line |
|---|---|
| `opening` | Saldo awal |
| `normal`, `dues` going in | Total masuk |
| `normal` going out, `reimbursement` payout | Total keluar |
| `adjustment` with `reverses_transaction_id` | Total masuk, **negative** |
| any other `adjustment` (a *cek kas* fix, a standalone one) | Penyesuaian |
| `transfer`, `between_accounts` | none - moves no fund or pos total |
| `transfer`, `reclass_purpose` | none on the whole fund; Dipindah under a purpose filter |

- **A reversal nets against the month it is dated in**, even when the payment it reverses was in an earlier month. *Total masuk* can then read below zero for a month; accepted, and revisited if treasurers find it confusing (maintainer, 2026-10-05). The ledger itself stays gross - two rows, as ADR-029 decided; netting happens only on this summary.
- **Openings get their own line, not the starting balance**, so a month's start is always the previous month's end. In the setup month the walk starts at Rp 0.
- **Dipindah sums the raw legs**, not #280's folded display rows: the line has to equal the change in the pos's balance, and folding is presentation only.
- **The last line is a ledger sum, never computed from the lines above.** The walk *footing* - start plus lines equals end, to the rupiah - is asserted by tests over every month of a scenario holding every row shape, for the whole fund and each purpose. It is not checked at runtime: a public page does not 500 on a neighbour.

**One class per row drives the list, the filter and the totals.** A reversal filters as *Uang masuk* and is counted there as a minus; it no longer shows under *Uang keluar*. Opening and Penyesuaian rows are listed, but hidden under a member or direction filter, as Dipindah rows already are.

**Which filters walk.**
- **No filter, or a purpose filter:** the full walk. Under a purpose filter the start and end are that pos's balance (ADR-037's bounded `PurposeBalance`); openings only ever land on Kas Utama.
- **A member or direction filter:** *Total masuk* and *Total keluar* only. A balance has no meaning for one member or one direction.

**The running month** reads its rows from the first of the month with **no upper bound**, matching its unbounded balance (ADR-037), so a row dated after today still foots.

**Beranda** shows the running month's In and Out in a section of its own, **Arus kas bulan ini** - one card with *Masuk* and *Keluar*, no walk - the first section below the divider (#319) that separates the fund as it stands from what happened, ahead of *Aktivitas terbaru*: what happened this month, then the rows it happened in (maintainer, 2026-10-05). Beranda's order becomes hero, per-location rows, reconciliation banner, purpose breakdown, divider, **Arus kas**, recent five. `GET /api/balances` carries them as `month: {in, out}`, read through the report's own `reportRows` over the unfiltered running month, in Asia/Jakarta; the client never works out a month. No chart, no comparison with other months: PRD section 4's "analytics dashboards" stays out.

**Copy:** the walk's ends are dated - "Saldo 31 Agustus 2026" - rather than *Saldo akhir*, which would be untrue mid-month and would sit beside the existing *Saldo awal* row label as a second meaning. The date carries no "per": the header's *Saldo per pos* already uses that word for "by purpose", and a second "per" on the same page would read as a second sense. *Saldo awal*, *Penyesuaian* and *Dipindah* are the report's existing row labels, so a line and the rows it sums share a name. *Bersih* goes: the two dated balances already say it.

## Consequences

- One reversal now reads three ways on one page, each correct for what it measures: the dues section drops the payment (status, a stock), an envelope's *terkumpul* drops the pair, and *Total masuk* nets it on the reversal's date (a flow).
- A standalone adjustment gains a row label, *Penyesuaian*; it rendered blank before.
- The report's assembly gains the walk; the page and the PDF render the same lines (ADR-035). The PDF takes no filters, so it always carries the whole-fund walk; the purpose walk is the page's.
- Riwayat labels a standalone adjustment *Penyesuaian* too, as the report does (amended 2026-10-06, below).

**Superseded in ADR-035:** "money-in, money-out and net totals for whatever is filtered", for the totals' contents and the net line. **Superseded in ADR-037:** "The month's transaction rows were always month-scoped", for the running month, whose rows are now unbounded above. **Superseded in ADR-032:** "Beranda's order becomes: hero, per-location rows, reconciliation banner, purpose breakdown, recent five", for the Arus kas section before the recent five.

## Amendments

An amendment corrects a statement of fact about the code that has since become false. It never changes a decision, a trade-off or an accepted cost - that is still a superseding ADR. See the [ADR index](./README.md) for the rule.

**2026-10-06** - the Beranda paragraph said the section is "**Arus kas** - one card, *Bulan ini*, with *Masuk* and *Keluar*".

The heading now names the month, *Arus kas bulan ini*, and the card drops its own *Bulan ini* label: the section only ever held the running month, so the label repeated what the heading could say once (maintainer, 2026-10-06). Same section, same place, same two figures; only the words moved.

**2026-10-06** - the Consequences said "Riwayat still leaves a standalone adjustment unlabelled; only the report names it."

Riwayat now labels it *Penyesuaian - <lokasi>* with the cek kas fix's icon, so the treasurer and the neighbour read the same word for the same row (maintainer, 2026-10-06). It is a narrow exception to #257's rule that labels go only on rows the app created: the label names the walk line the row sums into, not who made it, and her note still renders on its own line. The report's wording is unchanged. With both screens labelling every non-reversal adjustment alike, nothing reads `is_reconciliation_fix` any more, so it is gone from `GET /api/transactions` and `ledger.ReportEntry`.
