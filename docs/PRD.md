# Uruni — Product Requirements Document

**Version 0.1 · 2026-08-08 · Status: draft (pre-code)**

Companion docs: [`Positioning.md`](./Positioning.md) · [`Decisions.md`](./Decisions.md). Grounded in treasurer interview #1 (kept in the private working vault, not this public repo). This PRD turns those into concrete requirements for a first version.

---

## 1. Summary

Uruni is a mobile app, backed by a community's **own self-hosted always-on server**, that lets a reluctant, non-accountant treasurer record every transaction from their phone and always know the recorded balance matches the real money. The first version serves one validated persona and does one job extremely well: **keep a small group's shared fund honest, with the least possible thinking.**

## 2. The problem (validated)

From interview #1 (office-unit treasurer, 8 people, ~Rp 1–2M/month):

- Her hardest, most-hated task is **reconciliation** — making the recorded balance match the real money. Money sits in two places (cash wallet + a *personal* bank account mixed with her salary), the routine and incidental funds get lumped together, and people take cash to shop and forget to record it. So the numbers drift and she has to hunt down the gap.
- **Chasing late payers is not a pain** (small group, everyone pays after payday, in-person nudge works).
- **Transparency is not a felt need** — members never read the ledger — though she wants it to *exist* out of moral duty.
- She's on a laptop today (Google Sheets) but **wants to do it all from her phone**.
- The bar to switch: it must be **simpler than a spreadsheet and reliable enough to replace it** — "kalau malah bikin tambah rumit" is the dealbreaker; "tidak usah berpikir" is the dream.

## 3. Target user

**Primary:** the accidental treasurer of a small group (roughly 5–20 people) who manages routine dues plus occasional collections, wants to work from a phone, and has access to a technical helper to run the server (for interview #1's user, that helper is the maintainer).

**Explicitly not (v1):** large groups (RT with dozens of households), treasurers with no technical helper, and anyone needing accounting-grade bookkeeping. Per project principle, we do not generalize hard.

## 4. Goals & non-goals

**Goals**

1. Record any transaction from the phone in a few taps, anywhere.
2. Always show an honest running balance, and make reconciliation (records vs. real cash + bank) trivial.
3. Model the group's real money shape: two locations, tagged purposes, tiered dues, reimbursements.
4. Be reliable enough to be the *sole* record — safe if the phone is lost.
5. Be simpler than the spreadsheet it replaces.

**Non-goals (v1)**

- Payment reminders / automated chasing.
- Member logins or a member portal (there is a public read-only report link instead — no accounts).
- QRIS/payment-gateway integration, bank sync, receipts-required workflows.
- Accounting journals, analytics dashboards, multi-currency.
- Anything on the "RT operating system" list (residents, voting, complaints, inventory).

## 5. Principles (binding)

- Small groups first
- Simpler than a spreadsheet, reliable enough to replace it
- Mobile-first
- The numbers always add up, and Uruni proves it
- Records that can't be lost
- Your data stays yours (self-hosted; the project holds nothing)
- Free forever
- No feature creep

## 6. Core concept & information model

One community server holds one or more **Funds** (for the validated user, a single shared fund is enough; the model allows more without UI complexity).

Key entities:

- **Fund** — the shared kas. Has a name and currency (IDR).
- **Account/Location** — where money physically sits: `Cash` (wallet) and `Bank`. The recorded balance is tracked *per location* because that split is the source of her discrepancies. (v1 assumes the bank is possibly a personal account; Uruni tracks only the kas portion the treasurer reports.)
- **Purpose tag** — every transaction is tagged: `Kas Utama` (routine), a named **Incidental** (e.g. "Duka Pak Budi"), or `Pass-through` (e.g. Kas Bidang). One pooled real balance, separated *in meaning*, not in separate pots.
- **Member** — name + role/tier. No email/phone required (minimize data held).
- **Dues rate** — amount per tier (e.g. pelaksana 50k, fungsional pertama 70k, muda 80k, madya TBD); editable; effective over time.
- **Transaction** — income or expense; amount; date; location; purpose tag; optional member link (for dues/reimbursement); optional note; optional receipt photo. Immutable once posted (corrections are new adjusting entries) so the ledger stays trustworthy.
- **Incidental collection** — a lightweight envelope: target/occasion, contributions in, disbursement out, and a **leftover** that can be rolled into Kas Utama in one tap.
- **Reconciliation snapshot** — a point-in-time record of expected vs. actual balance per location, any difference, and how it was resolved.

## 7. Functional requirements

### 7.1 Setup & access
- The treasurer signs in to *their community's* server (single treasurer role in v1; optional read-only viewer later). No central Uruni accounts.
- First-run setup: name the fund, add members with tiers, set dues rates, set opening balances for Cash and Bank.

### 7.2 Record a transaction (the everyday action)
- A prominent "add" action reachable in one tap from the home screen.
- Minimum fields with smart defaults: amount, in/out, location (remembers last), purpose (defaults to Kas Utama), date (defaults to today). Note and photo optional.
- Recording should feel responsive; the server is the single source of truth and confirms the write quickly.
- Connectivity: **the app requires a live connection.** When offline it is deliberately unavailable — a clear "butuh koneksi" state, with no local data and no queue. This is a user-driven choice: she prefers no "which copy is valid?" ambiguity over any offline capability.

### 7.3 Dues
- View members and, for the current period, who has paid / partially paid / paid in advance (some pay several months at once).
- Mark a dues payment (amount auto-filled from the member's tier, editable; location; cash/transfer).
- Undo a dues payment recorded in error (wrong member, entered twice) — the payment is reversed by a new entry, never edited away, and the member reads as unpaid for that period again.
- A simple "not yet paid" view. **No reminders, no nagging automation.**

### 7.4 Reimbursements
- Record that a member fronted money → it becomes owed to them.
- Settle the reimbursement when repaid. Receipt optional and never required (parking Rp 2.000 needs no nota).

### 7.5 Incidental collections
- Create an incidental for an occasion (sickness, death, sunatan, pension).
- Collect one-off contributions and record the disbursement.
- On close, show the leftover and offer a one-tap **roll into Kas Utama**.

### 7.6 Pass-through (Kas Bidang)
- Record money collected on behalf of the parent org (e.g. Kas Bidang) and its forwarding, each as an ordinary transaction tagged `Pass-through`, so the report shows plainly what came in for the parent body and what went out to it.
- **The balance does not exclude it.** While the money sits in the wallet it really is in the wallet, so it counts — §6's "one pooled real balance, separated in meaning, not in separate pots" applies here too. Uruni does not track a levy as owed-but-unpaid and has no second "available" figure; a levy is an ordinary expense on the day it is paid. (Revised 2026-08-12: the original wording promised the balance would never be inflated by pass-through money, which required a second balance the treasurer would have to reconcile in her head — and would have been inconsistent anyway, since incidental collections are earmarked just as firmly and were never excluded. See [ADR-024](./ADR/024-schema-conventions.md).)

### 7.7 Balance & home screen
- Home shows: current total balance, balance per location (Cash / Bank), and a **reconciliation status**: "cocok" or "selisih Rp X — cek?".
- Optional breakdown by purpose tag.

### 7.8 Reconciliation (the heart of the product)
- A "reconcile" flow the treasurer can run anytime: she enters the *actual* cash on hand and the *actual* kas balance in the bank; Uruni compares each to the recorded figure.
- If they match: a small, satisfying confirmation.
- If they differ: show the gap per location, list recent transactions to help her spot a missing/duplicated entry, and let her either add the missing transaction or post a noted **adjustment** to square it. Every reconciliation is saved as a snapshot.
- (Future consideration, not v1: encouraging a dedicated non-personal kas account to remove the mixing at the source.)

### 7.9 Public shareable report
- The server exposes a **public, read-only report page** at a stable, unguessable link. The treasurer shares the link once; it stays valid for the life of the app (no rotation required).
- Anyone with the link opens it without logging in.
- The page shows **everything by default** and provides **filters** (month, purpose/tag, member, income/expense, dues status) so a public viewer can sift the data easily.
- Safeguards: long random slug + `noindex` so it isn't discoverable via search; an optional **"regenerate link"** escape hatch if it ever leaks (not required in normal use). Trade-off accepted: because everything is shown, the public URL exposes member names and payment status — fine given the treasurer's transparency intent and the low sensitivity of the data.
- This is a shared page, not a member portal — no accounts, no member logins.

### 7.10 Backup / export
- A full-data **export to JSON** (canonical, restorable via import) that the treasurer can download anytime. An **Excel workbook** is offered as an optional, human-readable secondary format (not necessarily re-importable).
- **Scheduled server-side dumps** the host can enable (periodic automatic backups written on the server).
- **Optional email delivery** of periodic backups (requires the host to configure SMTP).

## 8. Non-functional requirements

- **Reliability & backup:** the server is the single source of truth; losing the phone loses nothing. Backup is a full-data **JSON export** (canonical, restorable via import), with an optional **Excel workbook** secondary format, plus optional **scheduled server-side dumps** and **email delivery** the host can enable (see 7.10).
- **Self-hostable:** **prebuilt Docker images + a `docker compose` template** with minimal config; hosts pull images rather than compile. Bundle TLS/reverse-proxy (e.g. Caddy) since the public report link needs HTTPS. This deployment UX is make-or-break for anyone without a technical helper.
- **Authentication:** **local auth** for the single treasurer in v1 — self-contained, no external identity provider to configure per instance. Optional OIDC/OAuth may come later. The public report page (7.9) is unauthenticated by design.
- **Data minimization:** collect only what the job needs (names, amounts, dates, notes). No member contact info required.
- **Client platform: PWA** — installable to the home screen, no app store (which also dissolves the earlier app-store licensing nuance). Requires a live connection to function (see 7.2).
- **License:** AGPL-3.0 (network clause keeps any hosted instance open).
- **Localization:** Bahasa Indonesia first; IDR/Rupiah formatting; warm, human copy per the brand voice.

## 9. Success criteria

- The treasurer adopts Uruni as her **sole** record and stops opening the laptop/spreadsheet.
- Her monthly reconciliation completes quickly and **unexplained discrepancies drop toward zero**.
- Recording a transaction takes only a few seconds, on the phone, in the moment.
- She reports the feeling she asked for: less to think about.

## 10. Open questions

**Resolved 2026-08-08:** deployment = prebuilt Docker images + `docker compose` (with TLS) · backup = manual JSON export (+ optional Excel), plus optional scheduled server-side dumps and optional email delivery · auth = local auth (OIDC later) · platform = PWA · offline = app unavailable when disconnected (no queue) · public report = shows everything with filters (accepts exposing names/payment status) · dedicated kas account = deferred · persona coverage = n=1 accepted.

No product-level open questions remain. Implementation-level decisions (stack, database, framework) are tracked separately — see `Tech-Design.md` (to be created).

## 11. Out of scope / possible future

Member-facing app, multiple treasurers/roles, richer reporting, the emotional-context "what the money became" view (nice-to-have, deferred), dedicated-account guidance, additional personas (RT/large groups), reassigning a posted dues payment to a different member in one step (today: reverse it, then post it again against the right member).
