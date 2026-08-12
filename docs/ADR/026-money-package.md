# ADR-026 — The `money` package: overflow-checked `Amount`, arithmetic only

**Status:** Accepted · `draft` — editable in place until M3 closes; an edit made once code exists ships with that code · [ADR index](./README.md)

**Context.** [ADR-006](./006-money-integer-minor-units.md) already decided *what* Uruni's money is: `int64` integer rupiah, no floats. [ADR-024](./024-schema-conventions.md) already promised *when* the promise gets a name: "from M3 the `money` package carries the unit in the Go type." M3 is where that promise becomes a concrete package, and the concrete shape — what methods it has, what a caller gets back on overflow, how it crosses the sqlc boundary, how it serializes to JSON, whether it parses treasurer input — has not been decided anywhere yet.

## Decision

**`type Amount int64`, in `internal/money`.** The zero value is Rp 0 — an ordinary, meaningful balance (an empty fund, an account that nets to zero), not a sentinel for "unset." Nothing in the schema or the PRD ever needs to say "no amount" as distinct from "zero amount," so no `*Amount` or wrapper type is introduced for that purpose.

**The arithmetic surface exists only to add overflow checking — nothing else.** Because `Amount` is a defined integer type, Go's native `+`, `-`, unary `-`, and all six comparison operators (`==`, `<`, `<=`, `>`, `>=`, `!=`) already work correctly between two `Amount` values, or between an `Amount` and an untyped constant, with zero methods. The only thing native operators get wrong is silent wraparound on overflow, which is the one thing a trust core cannot tolerate. So the package adds exactly three methods, each doing one checked operation and nothing else:

```go
func (a Amount) Add(b Amount) (Amount, error)
func (a Amount) Sub(b Amount) (Amount, error)
func (a Amount) Mul(n int64) (Amount, error)
```

All three return `(Amount, error)` — never panic, never saturate. `Mul` takes a plain `int64` count (a number of months, not an `Amount`) because multiplying money by money is never a meaningful operation here; the only multiplication in the domain is a dues rate times a count of periods.

No `Neg()` — `-a` is exact for every representable `Amount` except negating `math.MinInt64`, and that value is already unreachable by construction: see the SQLite `SUM()` overflow finding in the M3 plan, which shows the ledger itself refuses to produce a sum anywhere near that magnitude before Go ever sees it. No comparison methods — `a < b` already compiles and is correct.

**Why checked-and-erroring, not panic, not saturation.** Three options were live:

- *Panic on overflow.* Rejected: it changes the caller's error-handling discipline depending on which arithmetic call happens to be in the stack, and a panic three domain calls deep in `internal/ledger` is a worse failure mode than an error the immediate caller can log and turn into a 500 — Go doesn't ask every intermediate frame to `recover`, so a panic here either crashes the process or needs a `recover` boundary this codebase doesn't otherwise have (see [ADR-022](./022-logging-slog.md) — one `*slog.Logger`, no framework-level recover middleware yet).
- *Saturate at `math.MaxInt64`/`MinInt64`.* Rejected outright: a saturated balance is not a warning, it is a **confidently wrong number** silently substituted for the real one, on the single screen (`CLAUDE.md`'s "always shows an honest running balance") this project exists to make trustworthy. Silently wrong is strictly worse than any error.
- *Checked, returns `error`.* Chosen. It is the ordinary Go idiom for an operation with an expected failure mode, it composes with everything else in this codebase that already returns `error`, and it lets `internal/ledger` decide what an overflow means at the call site (it currently means "something is very wrong," since realistic scale is nowhere near this boundary).

**The realistic-vs-ceiling gap is real, and is not the argument for this design.** A validated fund moves roughly Rp 1–2,000,000/month; `int64`'s ceiling is about 9.2 × 10¹⁸. Overflow is not a scenario this design defends against so much as a discipline a *trust core* keeps regardless of whether the threat is real — the same reasoning that puts `CHECK` constraints on every enum even though the Go code that writes them is the only writer.

**sqlc boundary: hand-written `FromDB`/`Int64`, not a `sql.Scanner`/`driver.Valuer` pair, not sqlc column overrides.**

```go
func FromDB(v int64) Amount { return Amount(v) }
func (a Amount) Int64() int64 { return int64(a) }
```

`internal/store` stays exactly what sqlc generates today: plain `int64` fields, zero changes to `sqlc.yaml`. `internal/ledger` calls `money.FromDB(row.Amount)` / `amt.Int64()` at each of the roughly fifteen places a `store.*` value crosses into an `Amount` or back.

Why not overrides, and why not Scanner/Valuer: sqlc's column overrides match `table.column` pairs declared in the schema. Most of M3's new aggregate queries return **computed, aliased columns** (`balance_amount`, `paid_amount`, `collected_amount` — see the M3 plan's query inventory) that are not schema columns at all, so an override list could not reach the columns that matter most even if written. The columns an override *could* reach (`"transaction".amount`, `reimbursement.amount`, …) would then need `Amount` to implement `sql.Scanner`/`driver.Valuer` anyway, for `database/sql` to read and write it — meaning two mechanisms (overrides *and* Scanner/Valuer) to cover one boundary that a single explicit conversion pair covers uniformly, for both schema and computed columns, at the cost of one line per call site, reviewable in the same diff as the query it converts. Given `sqlc v1.31.1` is already a version [ADR-024](./024-schema-conventions.md) documents as having a real parsing bug (the ASCII-truncation issue), leaning further on its override-matching behavior for something a five-line function already solves is not a trade worth making.

**JSON marshalling for M4's API: a bare JSON number — no custom `MarshalJSON`/`UnmarshalJSON` at all.** `type Amount int64` already marshals as a JSON number via Go's default `encoding/json` behavior, and already **rejects** a fractional literal (`"amount": 50000.50` fails with `json: cannot unmarshal number 50000.50 into Go value of type money.Amount`) for free. This is decided, not defaulted: a string encoding is a defensible alternative for unbounded precision, but no amount at Uruni's validated scale — or even a fund a thousand times larger — approaches JavaScript's 2⁵³ safe-integer ceiling, so string buys safety this codebase's own numbers cannot spend, at the real cost of a parse step the SPA does not currently have (it already sends and expects JSON numbers per [ADR-006](./006-money-integer-minor-units.md)'s `Intl.NumberFormat` design). Number, not string.

**No `Parse`, no `Format`, in M3.** M3 ships neither a function that turns a treasurer-typed string ("50.000") into an `Amount`, nor one that turns an `Amount` into an Indonesian-formatted display string. Reasons, not an oversight: M3 has no HTTP surface at all (the premise M3 is built on), so nothing in this milestone ever calls `Parse` — the SPA formats and parses client-side today, and hands M4 a JSON number already. `Format` is real, needed work, but its first caller is M7's server-rendered public report (`html/template` has no `Intl.NumberFormat` to lean on) and M8's Excel export — both after M3. Building either now is code with no caller for two milestones, which is what the prime directive exists to prevent.

**Consequences.** `internal/money` stays small: a defined type, three checked-arithmetic methods, a conversion pair, and their tests — nothing else. [ADR-022](./022-logging-slog.md)'s "never log a value that could identify a member or an amount" now has a type to hang a review check on: a `money.Amount` reaching an `slog` field is a `grep`-able mistake (`money\.Amount` near `slog\.`), not one the compiler catches. M7 and M8 add `Format` (and, if a manual/CSV import path ever needs one, `Parse`) to this same package later — a forward note, not a design made here.
