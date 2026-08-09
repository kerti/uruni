# CONTEXT — Uruni domain language

The shared vocabulary. Use these exact terms in code, API, and user-facing copy (copy in Bahasa Indonesia; identifiers in English). One word per concept — don't introduce synonyms.

## Core

- **Fund** (kas) — a community's shared pool of money. One instance may hold one or more; one is enough for the validated user.
- **Account / Location** — *where money physically sits*: `Cash` (dompet) or `Bank`. Balances are tracked per location because that split is where discrepancies come from.
- **Purpose tag** — what a transaction is *for*, over one pooled balance: `Kas Utama` (routine), a named **Incidental**, or `Pass-through`.
- **Member** (anggota) — a person in the group. Name + role/tier only; no email/phone (data minimization).
- **Dues rate** (iuran) — the recurring amount owed, which **varies by member tier** (e.g. pelaksana, fungsional pertama/muda/madya).

## Movements

- **Transaction** — an immutable posted entry: income or expense; amount (`int64` rupiah); date; location; purpose tag; optional member link, note, receipt photo. **Never edited or deleted** — corrections are new **adjusting entries**.
- **Reimbursement** — a member fronted money; it becomes owed to them and is settled when repaid. Receipt optional, never required.
- **Incidental collection** — a one-off pool for an occasion (sickness, death, sunatan, pension): contributions in, a disbursement out, and a **leftover** that rolls into Kas Utama.
- **Pass-through** — money collected on behalf of a parent body (e.g. **Kas Bidang**) and forwarded; never inflates the fund's own balance.

## Trust core

- **Balance** — derived by **summing the integer ledger**, never stored as a float.
- **Reconciliation** — comparing the recorded balance to the *actual* cash + bank, per location. States: **cocok** (matches — reconciled) and **selisih** (a difference — shown calmly in terracotta, never alarm-red).
- **Reconciliation snapshot** — a saved point-in-time record of expected vs. actual and how any difference was resolved.

## Sharing

- **Public report** — an unauthenticated, filterable, read-only page at a stable unguessable link the treasurer shares once. Shows everything, with filters. Not a member portal (no accounts).
- **Backup / export** — full-data **JSON** (canonical, restorable) + optional **Excel**; optional scheduled dumps and emailed backups.
