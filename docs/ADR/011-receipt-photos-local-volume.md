# ADR-011 — Receipt photos: local volume

**Status:** Accepted · [ADR index](./README.md)

**Decision.** Store optional uploaded images on a mounted **local volume**, path referenced in the DB; enforce a size cap and downscale on upload. Avoids an object-storage dependency.

**Consequences.** Backups must cover (or the docs must call out) the uploads volume; the JSON export references photo files rather than embedding them.
