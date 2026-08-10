# ADR-004 — Database: SQLite only through 0.x

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** One tiny instance per community, single writer, integrity-critical; self-host simplicity is the tie-breaker. This ADR originally kept SQL portable "so PostgreSQL is a config swap." Grilled 2026-08-09 and rejected on three grounds: **sqlc generates per engine** — placeholders differ (`?` vs `$1`) and so do the generated types, so query files cannot be shared; dual support means two schema dirs, two query dirs, two generated packages and a hand-written interface over them, which is a second data layer for the trust core. **goose migrations don't port either** (identity columns, `timestamptz`, real booleans). And **nothing in `make check` would run Postgres**, so the promise was never verified — an assertion rather than a feature, which [ADR-016](./016-deployment-targets-reference-infra.md) had already softened to "may".

**Decision.** **SQLite only** through `0.x`. There is no `DATABASE_URL`. Portability moves out of the SQL dialect and into the data format: [ADR-012](./012-backup-and-export.md)'s versioned canonical JSON export + import is the sanctioned way off SQLite, and it has to be tested at M8 regardless. Postgres returns only via a superseding ADR with real demand behind it.

**Consequences.**

- The schema uses SQLite properly instead of a portable subset: **`STRICT` tables** — without them SQLite will cheerfully store `"1000.50"` in an INTEGER column, which is not acceptable under [ADR-006](./006-money-integer-minor-units.md)'s int64 rupiah — plus `CHECK` constraints on the enum-ish columns (location, purpose tag) and partial indexes.
- **Driver: pure-Go `modernc.org/sqlite`**, so the image builds with `CGO_ENABLED=0`. That is what makes the `linux/arm64` cross-compile free (no QEMU); a cgo driver like `mattn/go-sqlite3` would force cgo and a heavier base image.
- Pragmas on every connection: `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL` (safe under WAL).
- **A single `*sql.DB` with `SetMaxOpenConns(1)`.** Everything serializes, so `SQLITE_BUSY` is structurally impossible — no retry logic, no intermittent failure in the ledger. The cost is that an unauthenticated public-report request can queue behind a write; on a fund of a few dozen members those queries are sub-millisecond. If report latency ever demonstrates otherwise, the upgrade is a split writer(1)/reader(N) pool under WAL — one file.
- **No exit ramp exists until M8** ships the JSON import. Through M2–M7 the honest answer to "can I move engines?" is *not yet*.
- Narrows [ADR-016](./016-deployment-targets-reference-infra.md)'s "SQLite or any Postgres" — see the erratum there.
- Unblocks the demo environment (issue #8): Fly.io with SQLite on ephemeral disk, no Neon.
