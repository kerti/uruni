# ADR-005 — Data access: sqlc

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Context.** Want type-safe, transparent SQL in Go without a heavy ORM; must target both SQLite and Postgres.

**Decision.** **sqlc** — write SQL, generate type-safe Go. Migrations via **goose** (or golang-migrate).

**Alternative.** GORM (more magic, heavier). sqlc keeps the SQL explicit, which suits money code where you want to see exactly what runs.

**Consequences.** SQL lives in versioned files; keep dialect differences minimal to preserve SQLite↔Postgres portability.
