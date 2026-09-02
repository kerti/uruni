<div align="center">

# Uruni

**Kelola dana kebersamaan.**
The calm way to keep your community's shared money — and know it always adds up.

*Open source · Self-hosted · Free forever*

</div>

---

## What Uruni is

Uruni is a small, calm app for the **accidental treasurer** — the person who got told *"Bu, jadi bendahara ya"*, didn't ask for it, and now looks after a community's shared money: monthly dues, birthdays, graduations, bereavement donations, team lunches.

It does one job extremely well: **keep a small group's shared fund honest, with the least possible thinking.**

- **One-tap capture, anywhere** — record a transaction from a phone at a market stall. The thing a spreadsheet can't do.
- **A running balance that's always honest**, with a simple **reconciliation** check: does what's recorded match the cash in the wallet and the money in the account? Uruni flags the gap instead of making you hunt for it.
- **Two money locations** (cash and bank), because that split is where the discrepancies come from.
- **One pooled balance with purpose tags** — kas utama, incidental collections, pass-through to kas bidang — so funds stay separated *in meaning* without juggling separate pots.
- **Per-member dues that vary by tier**, and a simple "who's paid" view.
- **Reimbursements in seconds, receipts optional** — nobody wants a receipt for Rp 2.000 of parking.
- **A shareable public report** so anyone can see where the money went.

That's the whole product. On purpose.

## Why it exists

The treasurer's hardest task isn't chasing people — in a small group, nagging is fine. It's **making the recorded balance match the real money.** Cash lives in two places, routine and incidental funds get lumped together, and someone always takes cash to shop and forgets to write it down. So the numbers drift.

Everything Uruni does serves one feeling:

> **Tenang, karena catatan selalu cocok dengan uang yang ada.**
> *Calm, because the records always match the money.*

## What makes it different

Every comparable app is a *company's server* holding your community's financial records. With Uruni, the server is **yours**.

- **The project holds nothing.** Uruni runs no production service and stores none of your data. Ever.
- **Self-hosted.** A community runs its own always-on instance — one source of truth, so a lost or broken phone loses nothing.
- **AGPL-3.0.** Anyone who hosts Uruni for their community must keep the code open. The trust story is auditable and true by construction.
- **Data minimization.** Names, amounts, dates, notes. No member emails or phone numbers are collected.

Honest caveat: this is **not** a tap-to-install consumer app. Standing up an instance takes a technical helper, and we don't pretend otherwise. See [`SELF-HOSTING.md`](SELF-HOSTING.md).

## Principles

1. **Small groups first.** Roughly 5–20 people: office units, RT, churches, choirs, alumni, family clans, hobby groups.
2. **Simpler than a spreadsheet**, and reliable enough to replace it.
3. **Mobile-first** — capture anywhere.
4. **The numbers always add up, and Uruni proves it.**
5. **Records that can't be lost.**
6. **Your data stays yours** — on your community's own server.
7. **Free, forever.** A gift to communities, not a business. There is no paid tier and never will be.
8. **No feature creep — ever.** See below.

## What Uruni will never be

Uruni is *not* an operating system for your RT or organization. It will not grow resident databases, digital voting, complaint handling, visitor logs, inventory, QRIS reconciliation, bank sync, accounting journals, or analytics dashboards.

It is also, deliberately, **not a payment-chasing or reminder machine**. For the treasurer we validated, chasing wasn't the pain — reconciliation was. There's a simple "not yet paid" view and nothing more.

> There are excellent products for those. Uruni exists to help small communities keep their shared fund honest, from a phone, without thinking hard.

If a proposed feature doesn't serve *the accidental treasurer looking after a shared fund*, the answer is a friendly no. This is the most important rule in the project, and pull requests that expand scope will be declined with thanks. The full non-goals list is [`docs/PRD.md` §4](docs/PRD.md).

## How it's built

A single **Go** binary serves the JSON API, server-renders the public report, and serves an embedded **React** PWA — one origin, one process, SQLite by default. Behind Caddy for automatic HTTPS.

The app is **connection-required by design**: no local data store, no write queue, no offline sync. When disconnected it shows a clear "butuh koneksi" state. That's a deliberate choice made with the validated user, who preferred no "which copy is valid?" ambiguity over any offline capability.

Details in [`docs/Tech-Design.md`](docs/Tech-Design.md); the decisions themselves are one file per ADR in [`docs/ADR/`](docs/ADR/README.md).

## Voice

Uruni speaks like a helpful friend, not software.

- *"Pengeluaran berhasil dicatat."*
- *"Kas dan catatan sudah cocok. 🎉"*
- *"Ada selisih Rp 15.000 — mau dicek bersama?"*

Contributions to copy and UI should keep this tone: warm, human, never corporate. All user-facing copy is Bahasa Indonesia, sentence case.

## Status

**Pre-release, built in supervised vertical slices.** The ledger works and is covered by tests, the JSON API is complete and sits behind a session cookie, and one binary serves it alongside the embedded React app. **M5 — auth** shipped as `v0.5.0`; **M6 — everyday UI** is in progress, and it is the first build a treasurer can actually try: record → home → reconcile, on a phone.

There is no released version for self-hosters yet. What's in flight is the [M6 milestone](https://github.com/kerti/uruni/milestone/6) and its open issues — that board is the live cursor, not this file.

## Documentation

| Doc | What's in it |
|---|---|
| [`docs/Positioning.md`](docs/Positioning.md) | The product thesis and emotional core |
| [`docs/PRD.md`](docs/PRD.md) · [`docs/PRD-ID.md`](docs/PRD-ID.md) | What to build and why (English / Indonesian) |
| [`docs/Tech-Design.md`](docs/Tech-Design.md) | Stack and architecture — the overview |
| [`docs/ADR/`](docs/ADR/README.md) | The architecture decisions, one file each |
| [`docs/Design-System.md`](docs/Design-System.md) | Colors, type, components, voice |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Milestones and release cadence |
| [`docs/Decisions.md`](docs/Decisions.md) | The running decision log |
| [`CONTEXT.md`](CONTEXT.md) | The domain vocabulary — use these exact words |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Setup, workflow, and the scope bar |
| [`SELF-HOSTING.md`](SELF-HOSTING.md) | Run your own instance |
| [`SECURITY.md`](SECURITY.md) | Reporting a vulnerability |
| [`CLAUDE.md`](CLAUDE.md) | The rules a change must respect (for AI assistants and humans alike) |

## Contributing

Contributions are welcome, with one caveat that matters more than usual: **Uruni stays small.** The most valuable contributions are polish, clarity, accessibility, translations, and bug fixes — not new capabilities.

Before opening a feature PR, open an issue first and check it against the principles above. *"Would the accidental treasurer notice and love this, without having to learn anything?"* is the bar. If it adds a screen, a setting, or a concept, it probably doesn't clear it.

Start with [`CONTRIBUTING.md`](CONTRIBUTING.md).

## License

[AGPL-3.0](LICENSE) — copyright and the applying notice are in [`NOTICE`](NOTICE). Uruni will always be free to use. The network clause means anyone who hosts Uruni for a community must keep the source open, which is what makes the trust story true by construction rather than by promise.

---

<div align="center">

*The calmest way to look after your community's shared money — and prove you did it right.*

</div>
