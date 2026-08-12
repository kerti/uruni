# ADR-024 — Schema conventions: STRICT SQLite, integer ids, two time types

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Context.** M2 lays down every table Uruni will ever have a strong opinion about. Migrations become immutable at the first live treasurer's instance ([ADR-018](./018-release-and-versioning.md)), so a convention chosen loosely here is a convention forever. SQLite is the only engine through `0.x` ([ADR-004](./004-database-sqlite-only.md)), which removes the usual reason to write a cautious portable subset — the escape hatch is [ADR-012](./012-backup-and-export.md)'s canonical JSON, not dialect discipline.

## Decision

**`STRICT` on every table, `CHECK` on every enum and every amount.** Without `STRICT`, SQLite will store `"1000.50"` in an `INTEGER` column, which [ADR-006](./006-money-integer-minor-units.md)'s `int64` rupiah cannot tolerate.

**Ids are `INTEGER PRIMARY KEY`** (rowid aliases). No ULID or UUID: offline is out of scope (`CLAUDE.md` rule 4) so nothing generates ids client-side, and the one place unguessability is needed — the public report link — is a slug column on `fund`, not an id scheme. A ULID would also be the binary's first dependency bought for nothing.

**Money columns are `INTEGER`, and every one of them has `amount` in its name** — `amount`, `target_amount`, `recorded_amount`, `actual_amount`, `difference_amount`. Not the unit: `_rupiah` was the earlier rule and was dropped 2026-08-12, because no identifier in this codebase should know which country it is in. The neutral token keeps the one thing the unit suffix was actually good for — `grep amount` still enumerates every money column in the schema for an audit — while costing nothing if a second currency ever forces the question.

What stops a float is not the name: `STRICT` plus `INTEGER` rejects `1000.50` outright, [ADR-006](./006-money-integer-minor-units.md) fixes the scale (IDR's minor unit is the rupiah, so there is no factor to remember), and from M3 the `money` package carries the unit in the Go type where it belongs.

A currency **code** stored as a value is a different thing and stays: `CHECK (currency = 'IDR')` is ISO 4217 data, in the same category as a dues tier the treasurer names "pelaksana". The rule binds identifiers, not the rows users and standards put in them.

**Two time types, deliberately.**

| Kind | Type | Example | Why |
|---|---|---|---|
| Business date | `TEXT` `'YYYY-MM-DD'` | `occurred_on` | A transaction happens on a *calendar day* in the treasurer's week, not at an instant. Storing an instant invites offset bugs and answers a question nobody asked. |
| Period | `TEXT` `'YYYY-MM'` | `dues_period` | Dues are monthly. A period is not a date. |
| Audit instant | `INTEGER` unix seconds UTC | `created_at` | Unambiguous, sortable, `int64` in Go. |

**Dates are validated by `date()`, never by `LIKE`.** `CHECK (d LIKE '____-__-__')` is decorative — verified to accept `'2026-13-45'` and `'aaaa-bb-cc'`, because `_` matches any character. Use:

```sql
CHECK (date(d) IS NOT NULL AND d = date(d))                                     -- YYYY-MM-DD
CHECK (p GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]' AND date(p||'-01') IS NOT NULL) -- YYYY-MM
```

The `date(d) IS NOT NULL` half is load-bearing: `date('not-a-date')` returns `NULL`, and a `CHECK` that evaluates to `NULL` is *satisfied*, so `CHECK (d = date(d))` alone lets garbage through. `GLOB`'s character classes restrict to digits where `LIKE`'s `_` does not. Both forms verified against SQLite, including the calendar-invalid `'2026-02-30'`.

**Every aggregate is wrapped in `CAST(… AS INTEGER)`.** sqlc's SQLite engine cannot infer the type of a summed expression and emits `interface{}`:

```go
BalanceRaw(ctx, fundID)  (interface{}, error)  // COALESCE(SUM(CASE …), 0)
BalanceCast(ctx, fundID) (int64, error)        // CAST(COALESCE(SUM(CASE …), 0) AS INTEGER)
```

Every derived balance in Uruni is that query, so without the cast the trust core loses its compiler exactly where `CLAUDE.md` rule 1 needs it most — and nothing turns red: `sqlc generate` succeeds, and the `interface{}` only surfaces at M3. [ADR-005](./005-data-access-sqlc.md) predicted this class of gap in the abstract; this is the concrete rule.

**Query files are pure ASCII** — no em dashes, no `§`, no curly quotes, in `internal/store/queries/*.sql`. sqlc v1.31.1's SQLite engine measures a statement's end in runes but slices the source in bytes, so **every non-ASCII character in a query file chops one more byte off the tail of the generated SQL constant**, silently, with a green build. Three em dashes in `ping.sql`'s comment turned `SELECT 1 AS ok` into `SELECT 1` — which still runs, and is why this went unnoticed through M1. One em dash above `GetEffectiveDuesRate` turned `LIMIT 1` into `LIMIT`, which does not run at all.

The earlier reading of the same symptom — that a bare `SELECT 1` truncates to `SELE` and an alias is the fix — was the byte/rune bug misdiagnosed; the alias was never what saved it. `TestQueryFilesAreASCII` enforces the real rule, because prose is exactly what a careful author adds above a subtle query. Migrations are unaffected: goose reads them verbatim, and sqlc's schema parse showed no truncation.

**Fund scoping is enforced by composite foreign keys.** Every fund-scoped table carries `UNIQUE (fund_id, id)`; children reference `(fund_id, parent_id)` rather than `parent_id` alone. SQLite then rejects a transaction whose account belongs to another fund, which is otherwise an invariant only the application remembers. PRD §6 requires the model to allow several funds without UI complexity; this is what makes that claim true rather than aspirational.

**The rule that matters is coverage:** *every* nullable pointer to a fund-scoped table gets the composite form. Single-column `REFERENCES` on a fund-scoped parent is a bug, not a shortcut — it checks existence and says nothing about ownership.

**Foreign keys point one way — toward the ledger — and no two tables reference each other.** The create order is `fund → account/purpose/dues_tier/member/reimbursement/transfer → transaction → reconciliation → reconciliation_line → incidental → receipt`, with no forward references anywhere. SQLite would have tolerated a cycle (it resolves FKs at DML time, not DDL time), which is exactly why the rule has to be stated rather than discovered.

The cycle this rule removed: an early draft had `reconciliation.through_transaction_id` pointing at the ledger *and* `"transaction".reconciliation_id` pointing back, so an adjusting entry could name the reconciliation that prompted it. The link belongs on `reconciliation_line.adjustment_transaction_id` instead, because an adjustment squares **one account's line**, not a whole snapshot — and `CHECK (resolution <> 'adjusted' OR adjustment_transaction_id IS NOT NULL)` is a stronger invariant than the one it replaced, since "this line was adjusted" is the claim capable of being a lie.

It also fixed a case the cycle had quietly forbidden. `CLAUDE.md` rule 3 makes every correction a new adjusting entry, not only the ones raised during a reconciliation; the old `CHECK (kind <> 'adjustment' OR reconciliation_id IS NOT NULL)` forced a Tuesday-afternoon correction to be `kind='normal'`, discarding the fact that it was a correction. A standalone `adjustment` is now legal.

**Lookup tables only for rows a user creates.** Fixed sets (`direction`, `account.kind`, `purpose.kind`, `transaction.kind`, `resolution`) are `TEXT` + `CHECK`. Dues tiers are a table because the treasurer names them.

**Identifiers, enum values and comments are English, exclusively.** Bahasa Indonesia lives in UI labels and translation files and nowhere else — see [ADR-014](./014-localization-indonesian-first.md). The routine purpose is **Kas Utama** to the treasurer and `main` in the schema; `CONTEXT.md` records that mapping. Data a user types (a tier named "pelaksana") is data, not an identifier, and is exempt.

**Discriminator columns are named `kind`, never `type`.** `type` is a Go keyword, so the column and every local that reads it would disagree forever — `typ`, `t`, or `kind` in the code against `type` in the schema. `kind` is what Go (`reflect.Kind`) and TypeScript (the discriminant property of a discriminated union) both already use for a tag column, and unlike `TYPE` it needs no quoting in any dialect. The word repeats across `account`, `purpose`, `transaction` and `transfer` for four unrelated enumerations, which is deliberate: sqlc namespaces per struct so nothing collides, and one word doing one job consistently beats four invented synonyms.

**Immutability is enforced by triggers, not by convention.** `"transaction"`, `reconciliation` and `reconciliation_line` each get a `BEFORE UPDATE` and a `BEFORE DELETE` trigger that `RAISE(ABORT, …)`. `transfer` gets the pair too, so the two rows it binds cannot be re-labelled after the fact. `INSERT` stays open, which is what lets ADR-012's import restore a database.

## The model's load-bearing choices

**Opening balances are ledger rows** (`kind='opening'`), not a column on `account`. `CLAUDE.md` rule 2 makes the ledger the only source of a balance; a column would be a second one.

**Value-neutral movements are pairs, held by a `transfer` row.** Depositing cash at the bank, or rolling an incidental's leftover into Kas Utama, is two transactions of equal amount and opposite direction. The fund's total is unchanged because no money moved — only where it sits, or what it is for. One row would silently change the total.

**Reimbursements sit off the ledger until settled.** When a member fronts Rp 2.000 of their own money, the kas cash did not move; a ledger row at that moment would make the recorded balance differ from the real wallet, which is the single failure Uruni exists to prevent. A `reimbursement` row means "owed to this member"; settling posts one real `out`. A `UNIQUE` partial index enforces "one", because "settle once" was otherwise only prose. `waived_on` closes a claim that will never be repaid, and the settle path filters on it — a cross-table `CHECK` SQLite cannot express.

Accepted cost: the expense's *ledger* date is the settle date. `incurred_on` keeps the truth about when it actually happened.

**Pass-through is descriptive, and drives no arithmetic.** `purpose.kind = 'pass_through'` marks money handled for a parent body (Kas Bidang) so the report can group it. It does **not** come out of any balance. PRD §6's own words are "one pooled real balance, separated in meaning, not in separate pots" — and an earlier draft of this ADR broke that by excluding pass-through from a second "available" figure, while leaving incidental collections (earmarked exactly as hard) inside it. Either both leave the headline or neither does; two balances that disagree is the wrong thing to put on the calmest screen in the app.

Accepted cost, decided 2026-08-12: there is no way to plan for a levy or to record that one is owed. A levy is an ordinary `out` on the day it is paid. PRD §7.6 was amended to match. The enum value survives, so the exclusion can return later without a migration.

**A reconciliation snapshot stores numbers, and that is not a cache.** `recorded_amount` is a frozen fact about a past moment, reproducible because `"transaction"` rows can never be deleted, so `through_transaction_id` is a stable cutoff. `resolution` is set at insert and never revised: revisiting a `left_open` difference next week is a *new* snapshot, and a backdated correcting entry gets a higher id, so it lands outside the old snapshot's math and inside the live balance the next one compares against. This is the one place a derived number is stored, and it is bounded on purpose.

**Accepted limitation: `member.tier_id` is not effective-dated** while `dues_rate` is. A member promoted mid-year has their *current* tier applied to past periods, so dues owed for those months can be misstated. For 5–20 people with roughly annual tier changes, a temporal member-tier table is more machinery than the error it prevents. Revisit only if a real instance disagrees.

## Consequences

Migrations are hand-written SQLite with `STRICT`, partial indexes, composite FKs and triggers — verified to survive sqlc's SQLite code generation, including the quoted keyword table `"transaction"`.

Two rules bind every query written from M3 on: **cast the aggregates**, and **scope by `fund_id`**. Both are cheap and neither is discoverable from a green build.

ADR-012's import inherits two obligations from the composite FKs: insert in dependency order or set `PRAGMA defer_foreign_keys=ON` inside the import transaction, and preserve original ids verbatim — ids are semantically load-bearing here, not surrogate.
