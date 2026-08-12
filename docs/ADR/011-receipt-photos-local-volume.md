# ADR-011 — Receipt photos: local volume

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** Store optional uploaded images on a mounted **local volume**, path referenced in the DB; enforce a size cap and downscale on upload. Avoids an object-storage dependency.

**A receipt is an attachment, not a ledger fact** (added 2026-08-12, with M2's schema). The path lives in its own `receipt` table referencing the transaction, **not** in a column on the transaction row — because posted transactions are immutable at the database level ([ADR-024](./024-schema-conventions.md)), and a photo must be attachable after the fact. A treasurer records Rp 20.000 for parking from the car and finds the nota that evening; a column would have forced her to choose between the receipt and the immutability rule.

Two things follow, both free: **several receipts per transaction** (the nota plus the transfer screenshot), and a wrong photo is **replaceable** — `receipt` rows are insertable and deletable, because deleting an image changes no number. A receipt can also hang off a `reimbursement`, which is not a ledger row at all.

**Consequences.** Backups must cover (or the docs must call out) the uploads volume; the JSON export references photo files rather than embedding them. A deleted `receipt` row leaves an orphaned file on the volume until something sweeps it — acceptable, and cheaper than making image deletion transactional.
