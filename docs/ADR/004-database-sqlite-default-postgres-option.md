# ADR-004 — Database: SQLite default, Postgres option

**Status:** Accepted · [ADR index](./README.md)

**Context.** One tiny instance per community, single writer, integrity-critical; self-host simplicity is priority. Balances uses Postgres on Neon.

**Decision.** **SQLite** as the default (no separate DB container; the whole deploy can be one binary + a file). Keep SQL portable so **PostgreSQL** is a config swap — the maintainer's own hosted instance may use **Neon Postgres** (see [ADR-016](./016-deployment-targets-reference-infra.md)).

**Consequences.** SQLite file on a mounted volume; use WAL mode. Staying within portable SQL keeps the Postgres path open. **Driver: pure-Go SQLite (`modernc.org/sqlite`)** so the image builds with `CGO_ENABLED=0` for a static/distroless binary (a cgo driver like `mattn/go-sqlite3` would force cgo and a heavier base image). Worth a one-line ADR at scaffold.
