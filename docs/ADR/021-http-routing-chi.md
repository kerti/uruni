# ADR-021 — HTTP routing: chi, adopted at M4

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Context.** [`Tech-Design.md`](../Tech-Design.md) left this open as *"Router: stdlib `net/http` (Go 1.22+ routing) vs. `chi` — minor, decide at scaffold."* The M1.1 scaffold then picked stdlib `http.ServeMux` silently, which is how an open question turns into an accident. The real question isn't the two routes M1 has; it's what the API, session auth and the public report need — route groups, per-group middleware, and URL parameters, in M4 through M7.

**Decision.** **`github.com/go-chi/chi/v5`**, adopted at **M4 (core API)**, not before.

Until M4 the router stays stdlib `http.ServeMux`: M1 serves `/healthz` and the embedded SPA, and pulling in a dependency to route two paths would be noise. M4 is the first slice with route groups (`/api/*` authenticated, `/report/*` public) and middleware worth composing, so it is the honest moment to add chi — and it converts cleanly, because chi routers *are* `http.Handler`s and chi middleware *is* `func(http.Handler) http.Handler`.

**Why chi over staying on stdlib.** Go 1.22 routing handles method+pattern matching and wildcards, but sub-router mounting and per-group middleware stay hand-rolled, and middleware order is the kind of thing that is easy to get subtly wrong around an auth boundary. chi is small, stdlib-shaped, unopinionated, has no transitive dependencies, and is the most training-dense Go router — which matters in a repo built with AI assistance. It is the rare dependency that reduces the amount of code we own in the part of the server guarding the treasurer's writes.

**Consequences.** One direct dependency in `go.mod` from M4 (until then, zero). Handlers keep the `http.HandlerFunc` signature either way, so the M1–M3 handlers move over unchanged; what changes is `internal/http`'s wiring, in one file. Route parameters become `chi.URLParam`, and the SPA fallback becomes a `NotFound` handler. If chi is ever wrong, the exit is back to stdlib and is equally mechanical.
