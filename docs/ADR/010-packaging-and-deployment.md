# ADR-010 — Packaging & deployment

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Decision.**
- Multi-stage **Dockerfile**: build the React bundle, then `go build` embedding it → a small **distroless/scratch** image with a single binary. Publish to **GHCR**.
- A single **`docker-compose.yml`**: `app` + `caddy`. SQLite file, uploaded receipt photos, and backups live on **named volumes**.
- Config via **environment variables** / `.env` (base URL, session secret, optional SMTP).

**Consequences.** This compose file *is* the make-or-break deployment UX; treat it as a first-class deliverable with a short README.

**Erratum 2026-08-09.** The two bullets above originally offered an optional `postgres` service and an "optional Postgres/Neon URL". [ADR-004](./004-database-sqlite-only.md) narrowed the engine to **SQLite only through `0.x`**, so both are struck as stale fact rather than by a superseding ADR — the packaging decision (multi-stage image, distroless, GHCR, compose as the deliverable, env-var config) is unchanged, and the shipped `docker-compose.yml` never had a `postgres` service.

**Erratum 2026-10-01.** The config bullet's *session secret* and *optional SMTP* are both gone: `URUNI_SESSION_SECRET` was required but nothing read it ([#119](https://github.com/kerti/uruni/issues/119)), and SMTP was cut with the backups rework ([#327](https://github.com/kerti/uruni/issues/327)). Struck as stale fact, like the erratum above; the packaging decision is unchanged.
