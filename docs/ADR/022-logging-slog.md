# ADR-022 — Logging: stdlib `log/slog`, adopted at M1.2

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** M1.1 logs one line with `log.Printf`, which is fine for one line and wrong as a habit: the operator's only window into a self-hosted instance is its container logs, and by M4 those need request context (method, path, status, duration) that a `Printf` string can't carry without inventing a format.

**Decision.** **`log/slog`** from the standard library, adopted in **M1.2** alongside the runtime config it needs. No third-party logger (zap, zerolog, logrus).

- One `*slog.Logger` built at startup and passed down — no package-level global.
- **Text handler to stderr by default**, JSON when `URUNI_LOG_FORMAT=json`; level from `URUNI_LOG_LEVEL` (default `info`). Both variables are additions to ADR-019's config table and land with it in M1.2.
- Logs are **operator-facing, so English** — the boundary is: operator surface (CLI, logs, self-hosting docs) English, treasurer surface (SPA, public report) Indonesian ([ADR-014](./014-localization-indonesian-first.md)).
- **Never log a value that could identify a member or an amount** — data minimization is a product rule (PRD §6), and container logs are the easiest place to leak it. Log IDs, not names or notes.

**Why stdlib.** slog is structured, in the standard library since Go 1.21, and fast enough by a wide margin for one small instance — the self-host-simplicity tiebreak in `CLAUDE.md` says stdlib wins ties, and this is not even close. A third-party logger would be the binary's first non-essential dependency and would buy nothing an RT treasurer's server can feel.

**Consequences.** Handlers take a logger (or pull one off the request context) rather than calling a global, which is slightly more plumbing and much easier to test — assertions run against a handler writing to a buffer. Request logging becomes a middleware at M4, on chi ([ADR-021](./021-http-routing-chi.md)).

**As built (M1.2, amended at M4).** The logger is constructed in `cmd/uruni` from the config and threaded into `internal/http`, where the router and the handlers hold it. Through M1.2–M3 it stayed in `cmd/uruni` alone, because the router had nothing to say until the request-logging middleware arrived; M4 is when it gained a reader.

`requestLogger` in `internal/http/middleware.go` is that middleware, and it delivers this ADR's promise: one line per request with method, path, status and duration, reading the status back through chi's `WrapResponseWriter`.

The data-minimization rule above is enforced by what the middleware reads rather than by care at each call site — `r.URL.Path` only, never `RawQuery` and never the body, because a query string or a posted body is exactly where a member name, a note or an amount would be.

## Amendments

**2026-08-27 ([#72](https://github.com/kerti/uruni/issues/72)).** The *As built* section above previously read: *"The logger is constructed in `cmd/uruni` from the config and stays there: it is not threaded into `internal/http` yet, because the router has nothing to say until the request-logging middleware arrives at M4. Passing a logger nobody calls would be plumbing with no reader on the other end."* True when written at M1.2; false from M4, when the middleware landed and the handlers took the logger. The decision is untouched — stdlib `slog`, no global, request logging as chi middleware — so this is a correction of fact, not a change of mind.
