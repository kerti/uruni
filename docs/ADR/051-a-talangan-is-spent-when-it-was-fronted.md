# ADR-051 - A talangan is spent when it was fronted

**Status:** Accepted · implemented at M11 ([#844](https://github.com/kerti/uruni/issues/844), [#845](https://github.com/kerti/uruni/issues/845), [#846](https://github.com/kerti/uruni/issues/846), [#847](https://github.com/kerti/uruni/issues/847)) · grilled with the maintainer 2026-11-19 · change only by adding a superseding ADR · supersedes in part [ADR-024](./024-schema-conventions.md) (the accepted cost "the expense's *ledger* date is the settle date") and [ADR-038](./038-the-month-as-a-balance-walk.md) (a row lands in the month of its own date) · [ADR index](./README.md)

**Context.** A member fronts an expense when the kas can't: Rp450.000 for a sickness amplop on 27 Okt, repaid from Kas tunai on 12 Nov. The claim sits off the ledger until it is settled ([ADR-024](./024-schema-conventions.md)), and settling posts one real `out` dated the settle date. ADR-024 accepted that the *expense* then reads as November's, with `incurred_on` keeping the truth on the side. A live treasurer found the cost was not acceptable: Okt's spending for that pos looked Rp450.000 too low and Nov's too high, on the app and on the public report the neighbours read. The cash date is right for the cash, and wrong for the spending.

Three shapes were grilled:

- **C - a hidden debt account.** Fronting posts a real expense on its real date, paid from a non-location account "owed to members" that goes negative; settling is a transfer into it. Rejected: a claim becomes a ledger row the moment it is recorded, so a wrong amount or member on an *unsettled* claim needs a talangan-specific reversal - correction machinery a reluctant treasurer would have to learn, for the commonest mistake.
- **C' - the same account, posted only at settle.** The settle posts the backdated expense from the debt account and a transfer into it. Rejected: the account's live balance is always zero and it holds nothing the claim does not already say. It is only a device to give the backdated row somewhere to sit, and every location query would have to learn to exclude it.
- **C'' - two dates, read side only.** Chosen.

**Decision.**

**Balances follow the cash; flows follow the spending.** A settled talangan's payout is still one `out` row from a real location, dated the day it was settled - nothing about the write path, the schema or reconciliation changes. What changes is which date the reads use for that one kind of row (`kind='reimbursement'`):

- **Balances** - Saldo kas, Saldo per lokasi, the balance per pos, the walk's start and end, every cek kas - use the row's own date. The cash left Kas tunai on 12 Nov and a balance never says otherwise.
- **Flows** - the walk's *Total keluar*, the report's transaction rows (by kind or by month, [ADR-049](./049-the-reports-period-and-grouping.md)), Riwayat > Transaksi, and Beranda's *Arus kas bulan ini* - date the payout by its claim's `incurred_on`. Okt's Kas Utama spending holds the Rp450.000.
- **A location filter is a cash view.** Riwayat or the report narrowed to a location ([#784](https://github.com/kerti/uruni/issues/784)) places the payout on its own date, since that view walks the location's balance.
- **The row names its other date.** Placed by `incurred_on`, it carries a second line *Dibayar 12 Nov · Kas tunai* in the app, and *Dibayar 12 Nov* on the public report and its PDF, which never name a location ([ADR-035](./035-public-report.md)); placed by its own date, *Ditalangi 27 Okt*. Wherever the row sits, the treasurer can see where else it reads. The settle confirmation says where the row went.

**The walk gets two talangan lines.** With the payout counted in Okt but the cash leaving in Nov, Okt's walk no longer reaches its own end. Two lines close it, each with its own words and one fixed sign, each shown only when non-zero, like Dipindah:

- **Talangan dibayar kemudian** (always +): payouts counted in this period whose cash left after it.
- **Pelunasan talangan sebelumnya** (always -): payouts whose cash left in this period for spending an earlier period counts.

```
                                 Okt          Nov
Start                            300.000      300.000
Total masuk                            0     +600.000   (Nov's dues)
Total keluar                    -450.000            0
Talangan dibayar kemudian       +450.000
Pelunasan talangan sebelumnya                -450.000
End                              300.000      450.000
```

A payout fronted and paid inside one period is in neither. A month or a range can hold both, e.g. one that repays an old claim while a new one is still out. The words are their own because the hero's *Talangan* already means what is owed, subtracted, and one page cannot use one word for two things; the lines are two because a single signed line (tried first) read as *sometimes plus, sometimes minus* and could not be tallied by eye (the maintainer, on the first screenshots). For the same reason **every line between the two balances shows the sign it is added with** - *+Rp409.000*, Total keluar as *-Rp605.500* - on the page, the PDF and Riwayat; the balances stay unsigned, and so do the two totals of a member-filtered summary, which is no walk.

**A talangan cannot be settled before the day it was fronted.** Money is not repaid before it was lent, so such a date is a typo, and settling refuses it by name (*Tanggal bayar tidak bisa sebelum tanggal talangan.*). It also keeps the two lines one sign each: a payout counted in a period is paid in it or after it. A payout stored before this rule, if any exists, still foots; its lines then read only "outside the period".

Both lines apply to the whole fund and to a pos filter, on the page, the PDF and Riwayat alike (one derivation, as `MonthWalk` already guarantees). A location walk has neither: it is a cash walk end to end. Summed over the locations, the location walks still equal the fund walk *with* its two lines.

**An unsettled claim stays off the ledger and editable.** It is in no walk, no flow and no balance; what is owed shows where it shows today (the hero's *Talangan*, ADR-037's owed figure), and a wrong amount, member, pos or date is still an edit, not a reversal (PRD 7.4). The talangan enters Okt's spending on the day it is settled - **a past period's flows change when a claim settles**, and that is the accepted cost: an Okt report read on 31 Okt and one read on 13 Nov differ by the Rp450.000, because on 31 Okt nobody knew yet that the fund would pay it.

**An amplop cannot close while it has a talangan not yet paid or waived.** The leftover rolled into Kas Utama ([ADR-031](./031-posting-to-a-closed-incidental.md)) is only knowable once every claim on the envelope is done; today a close ignores open claims, overstates the leftover, and then refuses the settle. Closing refuses by name, and the treasurer settles or waives first. This guards new closes only; a closed amplop already holding an open claim must be reopened by hand.

**The report lists who is owed what.** Below the hero, a **Talangan belum dibayar** section on the page and the PDF: one row per claim owed as of the period's last day (member, the date it was fronted, its pos, the amount), by the same rule as the owed total, so the rows add up to the hero's *Talangan*. Hidden when nothing is owed; narrowed by the pos and member filters; hidden under a location filter, where no claim sits. Member names and amounts are already public in the dues list, so this adds no new kind of data (PRD section 6); the claim's note stays private, as a waiver's does ([ADR-050](./050-waiving-a-members-dues.md)).

**What does not change.**

- **Putihkan posts nothing**, as today, and a waived talangan never counts as spending. No cash passed through the kas: the member paid and then gave it, a gift in kind. Counting it would need a matching zero-cash *in* on no real location - the account C' needed. It stays visible as *Diputihkan* in the Talangan tab.
- **A settlement is still not reversible** (`ErrReversalReimbursement`): a claim is corrected before it is settled. A payout from the wrong location is put right through cek kas, as now.
- **Past settled claims need nothing.** Every one already carries `incurred_on`, so the new reads date them correctly the day this lands; no backfill, no rebuild.

**Not built: taking over a claim.** One member repaying another out of pocket and holding the claim from then on came up in the grill, but has not happened. If it does, its shape is an effective-dated holder beside the claim, as [ADR-040](./040-effective-dated-tiers-and-rate-corrections.md) does for tiers: the claim keeps who fronted it and its `incurred_on`, the holder changes on a date, the kas sees nothing, and the settle pays whoever holds it. It is the only part that would need a schema change.

**Consequences.**

- No schema change and no ledger write change: both VPS instances take this release by image upgrade, not by rebuild.
- Every flow read learns one rule for one row kind, and one test must hold it everywhere: a fixture with a payout fronted in one month and paid in the next, asserting each view's placement and that every walk still foots.
- Riwayat > Transaksi pages by the placed date, which joins `reimbursement` into the paging key; the query plan must stay on the indexes [#839](https://github.com/kerti/uruni/pull/839) added (`internal/store/plan_test.go`).
- The walk has two more optional lines, every walk line between the balances is signed, and the report gains a new section.
- Settling gains one refusal (`settled_before_fronted`).
- ADR-024 and ADR-038 are marked superseded in part in the PR whose code makes it true.
