# ADR-001 — Overall shape: one Go binary, single origin

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** Self-hosters should run as little as possible; a split SPA + separate API adds CORS, a static host, and more to deploy.

**Decision.** In production the **Go server is the single origin**: it exposes the JSON API, **server-renders the public report** (Go `html/template`), and **serves the React SPA** — with the built React assets **embedded into the binary via `embed.FS`**. One self-contained binary, one container.

**Dev vs prod.** In development, frontend and backend run **separately** for DX: Vite dev server (React hot-reload) with `/api` and `/report` proxied to the Go server. In production they collapse into the one binary.

**Consequences.** No CORS in prod; SPA client-side routes fall back to `index.html` except `/api/*` and `/report/*`. The build pipeline must compile the React bundle before the Go build so `embed.FS` can pick it up.
