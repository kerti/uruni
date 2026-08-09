# ADR-005 — Data access: sqlc

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Context.** Want type-safe, transparent SQL in Go without a heavy ORM. This originally also read "must target both SQLite and Postgres" — that premise died in [ADR-004](./004-database-sqlite-only.md)'s grill, so sqlc has to earn its place on money-code safety alone rather than on portability.

**Decision.** **sqlc** — write SQL, generate type-safe Go. `engine: sqlite`, one generated package. Migrations via **goose**.

**Alternatives.** GORM (more magic, heavier). `database/sql` with hand-written scans — no codegen step and no engine limits, but hand-maintained scanning in the money path is exactly where a silent column-order mistake is most expensive. sqlx — reflection-based convenience instead of compile-time checking, which is a weak trade under "self-host simplicity wins ties."

**Consequences.** SQL lives in versioned files and stays reviewable, which is the whole point in ledger code where you want to see exactly what runs. The generated `Querier` interface makes the ledger fakeable in tests ([ADR-015](./015-testing-money-math.md)). sqlc's SQLite engine is younger than its Postgres one: type inference is weaker (some columns land as `any` and need casts) and complex CTEs can fail to parse. Where the generator can't handle a query, drop to a hand-written `database/sql` query for that one query — never bend the schema to satisfy the generator.
