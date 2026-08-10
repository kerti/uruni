# ADR-003 — Frontend: React SPA (Vite) + backend: Go

**Status:** Accepted (supersedes the earlier SvelteKit proposal) · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** Built with AI assistance, so stack fluency is a first-class criterion; Balances is React + Go.

**Decision.** **React** (via Vite) for the treasurer app as a PWA; **Go** for the server. Confirmed (supersedes the earlier SvelteKit proposal).

**Why.** Highest AI-codegen accuracy, proven Balances patterns, Go's single-binary self-host. The public report is plain server-rendered Go templates (robust, no React needed for that page).

**Consequences.** Anchors the repo layout (`/web` React app, Go module at root or `/server`). Component/library ecosystem is React's (shadcn/ui etc. available if wanted).
