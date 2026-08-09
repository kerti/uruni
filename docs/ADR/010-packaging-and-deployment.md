# ADR-010 — Packaging & deployment

**Status:** Accepted · [ADR index](./README.md)

**Decision.**
- Multi-stage **Dockerfile**: build the React bundle, then `go build` embedding it → a small **distroless/scratch** image with a single binary. Publish to **GHCR**.
- A single **`docker-compose.yml`**: `app` + `caddy` (+ optional `postgres`). SQLite file, uploaded receipt photos, and backups live on **named volumes**.
- Config via **environment variables** / `.env` (base URL, session secret, optional SMTP, optional Postgres/Neon URL).

**Consequences.** This compose file *is* the make-or-break deployment UX; treat it as a first-class deliverable with a short README.
