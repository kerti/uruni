# ADR-012 — Backup & export implementation

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.**
- **JSON export**: Go `encoding/json` serializes all tables into one **versioned** document; a matching **import** restores it.
- **Excel**: generated server-side with **excelize** — human-readable, not the restore path.
- **Scheduled dumps**: in-process scheduler writes periodic JSON to the backup volume ([ADR-013](./013-scheduling-in-process.md)).
- **Email delivery**: optional, via `net/smtp` (or **gomail**) using host-configured SMTP.

**Consequences.** The JSON schema needs a `version` field and a documented shape (own sub-doc when building).

**The import half is load-bearing.** Since [ADR-004](./004-database-sqlite-only.md) makes SQLite the only engine through `0.x`, this document is also the **sanctioned engine-migration path**, not just disaster recovery. A round-trip test — export → fresh empty database → import → identical balances — is therefore required, not a nice-to-have.
