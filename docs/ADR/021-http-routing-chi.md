# ADR-021 — HTTP routing: chi, adopted at M4

**Status:** Accepted · `draft` — implemented at M4; the tag drop is proposed in the M4.10 PR and is the maintainer's call · [ADR index](./README.md)

**Context.** [`Tech-Design.md`](../Tech-Design.md) left this open as *"Router: stdlib `net/http` (Go 1.22+ routing) vs. `chi` — minor, decide at scaffold."* The M1.1 scaffold then picked stdlib `http.ServeMux` silently, which is how an open question turns into an accident. The real question isn't the two routes M1 has; it's what the API, session auth and the public report need — route groups, per-group middleware, and URL parameters, in M4 through M7.

**Decision.** **`github.com/go-chi/chi/v5`**, adopted at **M4 (core API)**, not before.

Until M4 the router stays stdlib `http.ServeMux`: M1 serves `/healthz` and the embedded SPA, and pulling in a dependency to route two paths would be noise. M4 is the first slice with route groups (`/api/*` authenticated, `/report/*` public) and middleware worth composing, so it is the honest moment to add chi — and it converts cleanly, because chi routers *are* `http.Handler`s and chi middleware *is* `func(http.Handler) http.Handler`.

**Why chi over staying on stdlib.** Go 1.22 routing handles method+pattern matching and wildcards, but sub-router mounting and per-group middleware stay hand-rolled, and middleware order is the kind of thing that is easy to get subtly wrong around an auth boundary. chi is small, stdlib-shaped, unopinionated, has no transitive dependencies, and is the most training-dense Go router — which matters in a repo built with AI assistance. It is the rare dependency that reduces the amount of code we own in the part of the server guarding the treasurer's writes.

**Consequences.** One direct dependency in `go.mod` from M4 (until then, zero). Handlers keep the `http.HandlerFunc` signature either way, so the M1–M3 handlers move over unchanged; what changes is `internal/http`'s wiring, in one file. Route parameters become `chi.URLParam`, and the SPA fallback becomes a `NotFound` handler. If chi is ever wrong, the exit is back to stdlib and is equally mechanical.

**As built (M4).** chi carries the whole API, one direct dependency (`go-chi/chi/v5`) with no transitive ones, as promised. Three things read differently now that the milestone is done:

- **The route groups are not yet what the Decision describes.** It motivates chi with `/api/*` authenticated and `/report/*` public. Through M4 nothing is authenticated — session auth is M5 ([ADR-007](./007-auth-local-email-password.md)) and the public report is M7. What M4 actually needed chi for was URL parameters and one shared middleware stack. The auth boundary remains the strongest reason chi is the right call; it is simply ADR-007's to deliver, not this ADR's.
- **The wiring did not stay in one file.** It is three: `router.go` (the outer mount and the SPA fallback), `api.go` (the `/api` routes and their shared helpers), and `middleware.go` (request logging). That split is the honest shape once middleware exists, not drift worth correcting.
- **Route ordering is a real constraint, not a detail.** chi resolves a literal segment before a wildcard only if it is registered first, so `/reconciliations/latest` sits above `/reconciliations/{id}` deliberately and has a test pinning it. Anyone adding a literal segment under an existing `{id}` route needs to know that.

Delivered exactly as written: `chi.URLParam` for route parameters, and the SPA fallback as chi's `NotFound` handler — registered routes still win, so `/api` and `/report` 404 as themselves rather than serving `index.html`.
