# ADR-012 — Backup & export implementation

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.**
- **JSON export**: Go `encoding/json` serializes all tables into one **versioned** document; a matching **import** restores it.
- **Excel**: generated server-side with **excelize** — human-readable, not the restore path.
- **Scheduled dumps**: in-process scheduler writes periodic JSON to the backup volume ([ADR-013](./013-scheduling-in-process.md)).
- **Email delivery**: optional, via `net/smtp` (or **gomail**) using host-configured SMTP.

**Consequences.** The JSON schema needs a `version` field and a documented shape (own sub-doc when building).

**Two obligations the M2 schema puts on the import** (added 2026-08-12, from [ADR-024](./024-schema-conventions.md)):

1. **Insert in dependency order, or set `PRAGMA defer_foreign_keys=ON` inside the import transaction.** Fund scoping is enforced by composite foreign keys, so a child inserted before its parent is rejected rather than tolerated.
2. **Preserve original integer ids verbatim** — never let SQLite reassign them. Ids are semantically load-bearing, not surrogate: `reconciliation.through_transaction_id` is a ledger cutoff whose meaning is id ordering, and every cross-table reference in a restored database has to keep pointing at the same row.

Both are cheap to honour when the importer is written and expensive to discover afterwards, which is why they are recorded here rather than at M8.

**The import half is load-bearing.** Since [ADR-004](./004-database-sqlite-only.md) makes SQLite the only engine through `0.x`, this document is also the **sanctioned engine-migration path**, not just disaster recovery. A round-trip test — export → fresh empty database → import → identical balances — is therefore required, not a nice-to-have.
