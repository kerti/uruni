# ADR-007 — Auth: local email/password now, OIDC later

**Status:** Accepted · implemented at M5 — change only by adding a superseding ADR · [ADR index](./README.md)

**Decision.** **Local auth** — email/password hashed with **argon2id** (`golang.org/x/crypto/argon2`), server-side sessions with httpOnly secure cookies. OIDC is an additive option later (`coreos/go-oidc`).

**The session library is [alexedwards/scs](https://github.com/alexedwards/scs), and the open choice this ADR used to carry — "or a minimal hand-rolled store given it's effectively one user" — is closed against it.** That parenthetical read as licence to skip a dependency, but the risk it was hedging is session fixation, non-constant-time token comparison and cookie-flag mistakes: precisely the class `CLAUDE.md` routes to `builder-deep` rather than to a quick afternoon. A small, single-purpose MIT library is not a framework. Trading a few hundred lines of untested cookie-security code for one well-exercised dependency is rule 7 honoured, not bent. **Storage is our own `Store` over sqlc** (`internal/http/session_store.go`), never scs's bundled `sqlite3store`: that package's hand-written SQL would sit outside [ADR-005](./005-data-access-sqlc.md)'s reviewed-SQL discipline, where every query is checked in beside the schema.

**The surface is four routes, and three of them stay unauthenticated.** `POST /api/register` (one-shot bootstrap, and it logs the treasurer straight in), `POST /api/login`, `GET /api/session` (`{authenticated, has_account}` and nothing else), `POST /api/logout` (204 whether or not a session was there). Everything else under `/api` is behind the session gate, **`POST /setup` included**. Authorization and bootstrap are [ADR-030](./030-multi-fund-scoping.md)'s ruling, not this ADR's: a session proves the treasurer and carries no fund. See it for why `/setup` is inside the gate and why `has_account` is an existence oracle chosen rather than overlooked.

**Why.** Zero external setup for the host; the public report page (7.9) is unauthenticated, so login only guards the treasurer's writes.

**Consequences.** Ship secure defaults:

- **Login rate limiting**, in-process and in-memory, fixed-window, keyed independently by client IP and by submitted identifier — both live at once, so a distributed guesser and a single-account brute force are each caught by the key the other evades. In-process is the honest shape for one binary and one account; nothing here wants a Redis.
- **Cookie flags:** `HttpOnly`, `SameSite=Lax`, and `Secure` derived from `URUNI_BASE_URL`'s scheme rather than from a config flag of its own — https means the operator has a real TLS origin in front (Caddy, [ADR-009](./009-reverse-proxy-caddy.md)); plain-HTTP loopback is `make web-dev`, where a Secure cookie would simply never be sent and login would look broken with no error to explain it.
- **No separate CSRF token, and that is a decision rather than an omission.** Single origin, a JSON API, and `SameSite=Lax` between them mean no cross-site context attaches the cookie to a state-changing POST. A token would add a round trip and a failure mode to defend a seam that is already closed. If a cross-origin client is ever real, that reopens here.
- HTTPS-only via Caddy.
