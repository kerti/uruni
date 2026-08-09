# Uruni — Technical Design

**Version 0.4 · 2026-08-09 · Status: draft (stack confirmed: Go + React)**

Companion to [`PRD.md`](./PRD.md). The PRD owns *what* and *why*; this doc owns *how*.

The individual decisions live as one-file-per-ADR in [`ADR/`](./ADR/README.md) — each a single call with context, options, the decision, and consequences. This page keeps what frames them: the constraints, the stack at a glance, the production topology, and what is deliberately still open. **ADR numbers are permanent**; to change a decision, add a superseding ADR rather than rewriting one in place.

> Version caveat: recommendations reflect the ecosystem as of ~mid-2025. Before building, verify current versions and that named libraries are still actively maintained.

## Constraints that drive the tech (from the PRD)

- **Dead-simple self-host** — prebuilt Docker image + `docker compose`, minimal config, fewest possible moving parts. The single biggest force on the design.
- **One small instance per community** — one treasurer, a handful of members, ~Rp 1–2M/month. Tiny data, negligible concurrency.
- **PWA, connection-required** — no offline data store, no sync. This *removes* a whole class of complexity.
- **Money integrity** — correctness beats cleverness; the reconciliation math must be trustworthy and tested.
- **Public report page** — server-rendered, unauthenticated, filterable.
- **Local auth now, OIDC later. AGPL. Bahasa Indonesia first. Solo maintainer, building with AI assistance.**

## Stack at a glance

**Go** backend (API + server-rendered public report + serves the React bundle, single origin) · **React** SPA (Vite, PWA) · **sqlc** over **SQLite** (Postgres optional) · integer-only money · Go sessions + argon2id auth · Caddy TLS · single Docker image · Fly.io + Neon as the maintainer's reference deployment.

Rationale for the two anchors: both React and Go are the most densely and accurately represented stacks in the model's training data, which matters because Uruni is built with AI assistance — fewer hallucinated APIs, more idiomatic output. They also mirror Balances, so proven patterns carry over. Go's single static binary is the best possible fit for "dead-simple self-host."

## The decisions

Full text in [`ADR/`](./ADR/README.md).

| # | Decision |
|---|---|
| [ADR-001](./ADR/001-one-go-binary-single-origin.md) | Overall shape: one Go binary, single origin |
| [ADR-002](./ADR/002-languages-go-and-react.md) | Languages: Go (server) + TypeScript/React (client) |
| [ADR-003](./ADR/003-frontend-react-spa-backend-go.md) | Frontend: React SPA (Vite) + backend: Go |
| [ADR-004](./ADR/004-database-sqlite-default-postgres-option.md) | Database: SQLite default, Postgres option |
| [ADR-005](./ADR/005-data-access-sqlc.md) | Data access: sqlc |
| [ADR-006](./ADR/006-money-integer-minor-units.md) | Money is integer minor units (never floats) |
| [ADR-007](./ADR/007-auth-local-email-password.md) | Auth: local email/password now, OIDC later |
| [ADR-008](./ADR/008-pwa-no-offline-data.md) | PWA: installable shell, no offline data |
| [ADR-009](./ADR/009-reverse-proxy-caddy.md) | Reverse proxy & TLS: Caddy |
| [ADR-010](./ADR/010-packaging-and-deployment.md) | Packaging & deployment |
| [ADR-011](./ADR/011-receipt-photos-local-volume.md) | Receipt photos: local volume |
| [ADR-012](./ADR/012-backup-and-export.md) | Backup & export implementation |
| [ADR-013](./ADR/013-scheduling-in-process.md) | Scheduling: in-process |
| [ADR-014](./ADR/014-localization-indonesian-first.md) | Localization: Indonesian-first, strings centralized |
| [ADR-015](./ADR/015-testing-money-math.md) | Testing: prioritize the money math |
| [ADR-016](./ADR/016-deployment-targets-reference-infra.md) | Deployment targets & reference infra |
| [ADR-017](./ADR/017-cicd-github-actions.md) | CI/CD: GitHub Actions |
| [ADR-018](./ADR/018-release-and-versioning.md) | Release & versioning: tag-driven SemVer, operator upgrade contract |
| [ADR-019](./ADR/019-cli-surface-and-runtime-config.md) | CLI surface & runtime config (the scaffold's contract) |
| [ADR-020](./ADR/020-dev-environment.md) | Dev environment: one entry point, guards committed |

---

## Proposed topology (v1 production)

```
Internet ──▶ Caddy (TLS) ──▶ Go app  (single binary)
                               ├─ JSON API            /api/*
                               ├─ Public report (SSR) /report/*
                               ├─ React SPA (embed.FS) everything else
                               ├─ SQLite file   (volume)   ── or Neon/Postgres
                               ├─ uploads/      (volume)
                               └─ backups/      (volume)
```

Dev: `vite` (React, HMR) + `go run`, with `/api` and `/report` proxied to Go.

## Open technical questions

- JSON export schema: concrete shape + version strategy (own sub-doc when building).
- Balance derivation: compute-on-read by summing the ledger (recommended at this scale) vs. maintained running totals — confirm.
- Receipt uploads: include in the default scheduled backup, or document as a separate host responsibility?
- Router: stdlib `net/http` (Go 1.22+ routing) vs. `chi` — minor, decide at scaffold.

## Not deciding yet (deferred)

Error monitoring/observability, rate-limit specifics, a multi-environment (preview/demo/production) split, and any nightly migration-rehearsal CI — all deferred until there's real production data or a second deploy target to justify them. (CI/CD and release/versioning are now decided — [ADR-017](./ADR/017-cicd-github-actions.md)/[ADR-018](./ADR/018-release-and-versioning.md).)
