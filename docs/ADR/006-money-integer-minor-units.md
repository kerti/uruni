# ADR-006 — Money is integer minor units (never floats)

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** Store and compute all amounts as **`int64`** integer rupiah. IDR is effectively whole-rupiah, so the minor unit and the major unit are the same thing and there is no scale factor to remember. No float arithmetic beyond display.

**Consequences.** A small money package in Go ([ADR-026](./026-money-package.md)) and formatting on the client via `Intl.NumberFormat('id-ID')`. Balances are derived by summing the integer ledger; reconciliation compares integers exactly.

## The exit plan, for when sub-rupiah starts to matter

Redenomination (three zeros dropped, sen made significant) is the scenario that breaks the assumption above, and the earlier wording of this ADR — *"migrate the whole system to integer sen"* — is the version of that migration that must **not** be taken. Recorded here so the cheap moment to prepare is not missed, not because anything is being built for it now.

**Do not divide. Choose a minor unit that makes the conversion `x1`.** Old rupiah to new sen is a divide by 10, so every amount not ending in a zero rounds. Rounding each row independently then breaks the property this whole project rests on: the sum of rounded rows is not the rounded sum, so a historical `recorded_amount` stops reproducing from the ledger beneath it, while `CHECK (difference_amount = actual_amount - recorded_amount)` keeps passing because it is internally self-consistent whatever it was rescaled to. A snapshot that no longer derives from its own transactions is exactly what [ADR-024](./024-schema-conventions.md) says a snapshot is not.

Storing **thousandths of a new rupiah** — which is exactly one old rupiah — makes every historical row convert by multiplying by one. No data transformation, no rounding, no precision lost anywhere in the archive; new amounts entered in sen are `x10`. Redenomination becomes a display change and one scale constant rather than a migration of the ledger. `int64` has room to spare: a fund moving Rp 1-2M/month accumulates on the order of 10^9 over a century, against a ceiling of 9.2 x 10^18.

**Why the archive cannot simply be rewritten in place.** `"transaction"`, `transfer`, `reconciliation` and `reconciliation_line` all carry `BEFORE UPDATE` triggers that `RAISE(ABORT, ...)`, so a rescaling `UPDATE` is refused by the database. The supported path is [ADR-012](./012-backup-and-export.md)'s canonical JSON: export, transform outside the database, import into a fresh one. `INSERT` staying open on those tables is what makes that possible, and this is the second reason for it.

**What protects the option, and what would spend it.** Nothing in the domain divides — [ADR-026](./026-money-package.md)'s `Mul(int64)` is a dues rate times a count of periods, and there is no split, no percentage, no interest anywhere. No division means there is no rounding policy to get wrong today and none to re-verify later. The first feature that divides money (splitting an expense across members is the plausible one) spends that, and should re-read this section before it ships.

**Two hooks, both free at a moment already coming:**

1. When `Format` is written for M7's server-rendered report, it takes the currency from `fund` and keeps the **scale in one named constant** — not implied by `minimumFractionDigits: 0` scattered across call sites.
2. When [ADR-012](./012-backup-and-export.md)'s canonical export format is designed for M8, its version header carries **currency and scale**. An export whose integers do not say what unit they are in is the artifact that makes this migration unrecoverable, and the header costs nothing to add before any exported file exists in the wild.

**A second currency is a different, smaller question.** Currency is a fund-level fact and [ADR-024](./024-schema-conventions.md) scopes every query by `fund_id`, so two funds in two currencies never meet inside one aggregate — the `CHECK (currency = 'IDR')` and a hardcoded format string are close to the whole surface. The one latent hole is that `money.Amount` carries a unit but not a currency, so adding IDR to USD compiles; harmless while no code path can hold two funds' amounts at once, and the reason a cross-fund total is not a small feature.
