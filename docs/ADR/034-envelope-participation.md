# ADR-034 — Envelope participation: named contributions, derived from the ledger

**Status:** Accepted · implemented at M7 ([#338](https://github.com/kerti/uruni/issues/338)) · partly supersedes [ADR-029](./029-reversing-a-dues-payment.md) (the reversal's width) · change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** An envelope often asks every member for the same figure — a sunatan, a death in the neighbourhood — and the treasurer needs to see who has given and who has not ([#211](https://github.com/kerti/uruni/issues/211)). The schema made that unrepresentable on purpose: `"transaction"`'s CHECKs let only a dues payment (and its ADR-029 reversal) carry `member_id`, and `incidental.target_amount` is a total for the envelope, not an expectation of anyone. Grilled 2026-09-30 and pulled from M7 into M6 ahead of the backups epic, so the export's golden fixture ([#323](https://github.com/kerti/uruni/issues/323)) pins the schema after this change rather than bumping `format_version` straight after it.

The framing that decides most of it: **a participation table is two things, and only one of them comes from the ledger.** Who paid, and how much, is summed from posted rows — never stored, never a flag (`CLAUDE.md` rule 2). Who was *expected* is a decision, not a fact, and lives beside the ledger — for dues in the tier and rate tables, for an envelope on the envelope. Dues status already works this way; this ADR gives envelopes the same shape.

## Decision

**A contribution may name its member, on the ledger row.** A contribution stays what it is today — a `kind='normal'`, `direction='in'` row tagged to the envelope's purpose — and `member_id` becomes optional on it. No new `kind`: a named and an unnamed contribution are the same thing, and a guest or an anonymous giver must stay representable. Every reader that switches on `kind` (Riwayat, the report, the M8 workbook, ADR-033) is untouched.

A CHECK cannot see `purpose.kind` — it reads only its own row — so the rule is split:

- **CHECK** (own row): `member_id` without `dues_period` is allowed on `kind='normal' AND direction='in' AND reverses_transaction_id IS NULL`, and on the reversal shape below. Every other kind still carries neither.
- **`BEFORE INSERT` trigger** (cross-table): a `kind='normal'` row with `member_id` set is refused unless its purpose is `kind='incidental'`. The ledger checks first so the treasurer gets copy, not a constraint error; the trigger is what makes the row unrepresentable for anything that writes around the ledger.
- `DuesPaidByPeriod`, `LatestDuesPeriodPaidByMember` and every other dues query already filter `kind = 'dues'`, so a contribution can never be counted as dues. Balances sum every row regardless of `member_id` — **no change to any balance query**.

**The expectation lives on the envelope, and is editable.** `incidental` gains:

- `minimum_per_member INTEGER CHECK (minimum_per_member IS NULL OR minimum_per_member > 0)` — one figure for every expected member, same shape and nullability as `target_amount`. Not a dues rate: no tiers, no effective dates, no per-member amounts (that would be dues again, for one occasion).
- `incidental_recipient (fund_id, purpose_id, member_id)`, primary key on all three. `incidental` carries no `fund_id` (it is 1:1 with its purpose row), so fund scoping runs through `purpose`: a composite `(fund_id, purpose_id) REFERENCES purpose(fund_id, id)` and `(fund_id, member_id) REFERENCES member(fund_id, id)` ([ADR-024](./024-schema-conventions.md)), plus a plain `purpose_id REFERENCES incidental(purpose_id)` that proves the purpose is an envelope — the members the envelope is **for**. Zero or more, always members; an occasion for someone outside the roster names the member whose family it is, or no one. `ON DELETE CASCADE` from `member`: member delete exists only for setup duplicates, and a recipient is a choice, not a record.

Both are mutable like `occasion` — nothing on `incidental` is a posted fact. So is `target_amount`, through the same PATCH, open or closed ([#381](https://github.com/kerti/uruni/issues/381)): it was set once at open and never correctable, which this rule never intended.

**Participation is derived at read time.** For one envelope:

- **Expected** = members active on the day it opened (`joined_on` null or `<= opened_on`, `inactive_on` null or `> opened_on`), **tier ignored**, minus its recipients. Fixed by one date, so the list never shifts when someone later leaves.
- **Contributed** per member = the sum of that envelope's `kind='normal'`, `direction='in'` rows naming them, excluding any row a reversal points at.
- **State** per expected member: *Sudah menyumbang*, *Belum menyumbang*, or — only when a minimum is set and the sum is below it — *Kurang dari minimal*. Anyone who contributed without being expected (a later joiner, a recipient who gave anyway) is listed under *Sumbangan lain* with their amount, never as "not yet".
- A closed envelope's participation simply stops changing; nothing is frozen, because nothing was stored.

**Wrong member: reverse, then post again — ADR-029, widened.** ADR-029 kept its reversal "exactly as wide as dues". This ADR widens exactly that one thing: a reversal may also target a **named contribution**. The shape is ADR-029's — one `kind='adjustment'`, `direction='out'` row, `reverses_transaction_id` set, `account_id`, `purpose_id`, `amount`, `member_id` and `dues_period` copied from the original by the ledger, never re-typed — with `dues_period` now required only when the original is a dues payment:

- CHECK (reversal shape): `reverses_transaction_id IS NULL OR (kind = 'adjustment' AND direction = 'out' AND member_id IS NOT NULL)`. `dues_period` is no longer forced by the CHECK.
- The `BEFORE INSERT` trigger verifies the target: `kind='dues'`, or `kind='normal'` with a member on an incidental purpose; and that `member_id` and `dues_period` equal the original's. That is what keeps a dues reversal carrying its period and a contribution reversal carrying none — the CHECK cannot see the original row.
- The partial unique index (reversed at most once) and the composite FK stand as ADR-029 built them.
- A reversal posts to the envelope, so [ADR-031](./031-posting-to-a-closed-incidental.md) applies: a closed envelope is reopened first.

Everything else in ADR-029 — full-reversal-only, no one-step reassign, the reasons netting lost — carries over unchanged.

**A named contribution refuses purpose correction.** [ADR-033](./033-correcting-a-posted-purpose.md)'s transfer legs carry no member, so moving a named contribution to another peruntukan would leave participation counting the member against an envelope the money has left. It is refused the way a dues payment already is; the fix is reverse and post again. An unnamed contribution keeps ADR-033 exactly as it is.

**Where it shows.** The envelope's own screen (M6): the participation table, and one action — record a contribution with the member already filled in. The public report (M7): the same table on the envelope's section, consistent with the report showing dues status (PRD §10, resolved 2026-08-08: the report "accepts exposing names/payment status"). Catat asks **Dari siapa? (opsional)** only for money coming into an open envelope.

**No chasing.** PRD §7.3's line, copied into §7.5: a "belum menyumbang" view, **no reminders, no nagging automation** — nothing sends, schedules, drafts a message or links out to a chat app (§4).

## Consequences

**The trust core's constraints change, and the change is `builder-deep` work.** Two CHECKs, one new trigger, a new table and a column land in `00001_schema.sql`, edited in place ([ADR-025](./025-one-migration-file-until-1.0.md)); `make db-reset` after pulling. The existing dues-CHECK tests keep their meaning — every dues row still needs member, period and `in`, and a dues reversal still needs its period (now by trigger rather than CHECK) — and gain siblings: a member on a `normal` row outside an envelope refused, an outgoing named row refused, a contribution reversal carrying a period refused, a dues reversal missing one refused.

**Riwayat and search pick contributions up for free.** Both already `LEFT JOIN member` on `member_id`, so a named contribution shows its member wherever a dues payment does.

**The backup export sees one more table and one more column,** before its golden fixture exists — which is why this lands first.

**Copy** (approved 2026-09-30): *sumbangan* / *menyumbang*; *Sumbangan minimal per anggota*; *Untuk siapa amplop ini?* / *Untuk: …*; *Sudah menyumbang*, *Belum menyumbang*, *Kurang dari minimal* (neutral, never terracotta — being under a minimum is not a discrepancy); *Sumbangan lain*; *Dari siapa? (opsional)*. Deliberately not dues' *Belum bayar*: a sumbangan is not a debt, and `CONTEXT.md` keeps the two vocabularies apart.

**Not built:** per-member or per-tier expectations; exemptions other than recipients (hardship is a judgement, and the report would publish it); a treasurer-curated expected list per envelope. Each would be its own decision against PRD §4.
