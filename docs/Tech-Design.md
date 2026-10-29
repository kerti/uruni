# Uruni — Technical Design

**Status: living overview. The stack is built; each decision's stage is in the ADR index.**

Companion to [`PRD.md`](./PRD.md). The PRD owns *what* and *why*; this doc owns *how*.

The individual decisions live as one-file-per-ADR in [`ADR/`](./ADR/README.md) — each a single call with context, options, the decision, and consequences. This page keeps what frames them: the constraints, the stack at a glance, the production topology, and what is deliberately still open.

**ADR numbers are permanent** — never reused, never renumbered. An ADR's text is editable while it carries the **`draft`** tag; the tag comes off with the last slice of the milestone that implements it, after which the decision changes only by a superseding ADR ([`ADR/README.md`](./ADR/README.md) has the rule).

## Constraints that drive the tech (from the PRD)

- **Dead-simple self-host** — prebuilt Docker image + `docker compose`, minimal config, fewest possible moving parts. The single biggest force on the design.
- **One small instance per community** — one treasurer, a handful of members, ~Rp1–2M/month. Tiny data, negligible concurrency.
- **PWA, connection-required** — no offline data store, no sync. This *removes* a whole class of complexity.
- **Money integrity** — correctness beats cleverness; the reconciliation math must be trustworthy and tested.
- **Public report page** — server-rendered, unauthenticated, filterable.
- **Local auth now, OIDC later. AGPL. Bahasa Indonesia first. Solo maintainer, building with AI assistance.**

## Stack at a glance

**Go** backend (API + server-rendered public report + serves the React bundle, single origin) · **React** SPA (Vite, PWA) · **sqlc** over **SQLite** (the only engine through `0.x` — ADR-004) · integer-only money · Go sessions + argon2id auth · Caddy TLS · single Docker image · a small VPS or Fly.io as the maintainer's reference deployment.

Rationale for the two anchors: both React and Go are the most densely and accurately represented stacks in the model's training data, which matters because Uruni is built with AI assistance — fewer hallucinated APIs, more idiomatic output. They also mirror Balances, so proven patterns carry over. Go's single static binary is the best possible fit for "dead-simple self-host."

## The decisions

Every decision, with its current `draft` / implemented stage, is listed in the [ADR index](./ADR/README.md). That index is the one list; this page does not repeat it.

## Production topology

```
Internet ──▶ Caddy (TLS) ──▶ Go app  (single binary)
                               ├─ JSON API            /api/*
                               ├─ Public report (SSR) /report/*
                               ├─ Health check        /healthz (DB ping, last dump time)
                               ├─ React SPA (embed.FS) everything else
                               ├─ SQLite file   (volume)
                               ├─ uploads/      (volume)
                               └─ backups/      (volume)
```

Dev: `vite` (React, HMR) + `go run`, with `/api` and `/report` proxied to Go.

## Open technical questions

None open. The two once listed here - the JSON export's shape and version strategy, and whether receipt photos ride in the scheduled backup - were settled by [ADR-012](./ADR/012-backup-and-export.md) (`format_version`; a zip with a `receipts/` folder).

## Not deciding yet (deferred)

Error monitoring/observability, a multi-environment (preview/production) split, and any nightly migration-rehearsal CI — all deferred until there's real production data or a second deploy target to justify them. (CI/CD and release/versioning are now decided — [ADR-017](./ADR/017-cicd-github-actions.md)/[ADR-018](./ADR/018-release-and-versioning.md); the one public demo is [ADR-045](./ADR/045-a-public-demo-on-fly.md).)
