# ADR-007 — Auth: local email/password now, OIDC later

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** **Local auth** — email/password hashed with **argon2id** (`golang.org/x/crypto/argon2`), server-side sessions with httpOnly secure cookies (e.g. **alexedwards/scs**, or a minimal hand-rolled store given it's effectively one user). OIDC is an additive option later (`coreos/go-oidc`).

**Why.** Zero external setup for the host; the public report page (7.9) is unauthenticated, so login only guards the treasurer's writes.

**Consequences.** Ship secure defaults: login rate-limiting, strong cookie flags, HTTPS-only via Caddy.
