# Uruni — Architecture Decision Records

One file per decision. Each ADR is a single call with its context, the decision, and its consequences. The overview that frames them — the constraints, the stack at a glance, the production topology, and what is deliberately *not* decided yet — lives in [`../Tech-Design.md`](../Tech-Design.md).

## How an ADR changes

**Numbers are permanent** — never reused, never renumbered. Prose across the repo refers to decisions as "ADR-0NN", so a number has to mean one thing forever.

**Text depends on the tag:**

- **`draft`** — no code implements this decision yet. **Edit it in place.** Grilling a draft, changing your mind, tightening the wording: all fine, no ceremony.
- **no tag (implemented)** — a slice has shipped code behind this decision. **Change it only by adding a superseding ADR** and marking this one superseded.

**The tag comes off in the PR that implements the ADR** — that's part of the definition of done for a slice (see [`../../CLAUDE.md`](../../CLAUDE.md)). Migrations are stricter than either state: immutable from the first production deploy ([ADR-018](./018-release-and-versioning.md)).

Standing decisions that no ADR owns go in [`../Decisions.md`](../Decisions.md).

## The decisions

| # | Decision | Stage |
|---|---|---|
| [001](./001-one-go-binary-single-origin.md) | Overall shape: one Go binary, single origin | `draft` |
| [002](./002-languages-go-and-react.md) | Languages: Go (server) + TypeScript/React (client) | `draft` |
| [003](./003-frontend-react-spa-backend-go.md) | Frontend: React SPA (Vite) + backend: Go | `draft` |
| [004](./004-database-sqlite-default-postgres-option.md) | Database: SQLite default, Postgres option | `draft` |
| [005](./005-data-access-sqlc.md) | Data access: sqlc | `draft` |
| [006](./006-money-integer-minor-units.md) | Money is integer minor units (never floats) | `draft` |
| [007](./007-auth-local-email-password.md) | Auth: local email/password now, OIDC later | `draft` |
| [008](./008-pwa-no-offline-data.md) | PWA: installable shell, no offline data | `draft` |
| [009](./009-reverse-proxy-caddy.md) | Reverse proxy & TLS: Caddy | implemented |
| [010](./010-packaging-and-deployment.md) | Packaging & deployment | implemented |
| [011](./011-receipt-photos-local-volume.md) | Receipt photos: local volume | `draft` |
| [012](./012-backup-and-export.md) | Backup & export implementation | `draft` |
| [013](./013-scheduling-in-process.md) | Scheduling: in-process | `draft` |
| [014](./014-localization-indonesian-first.md) | Localization: Indonesian-first, strings centralized | `draft` |
| [015](./015-testing-money-math.md) | Testing: prioritize the money math | `draft` |
| [016](./016-deployment-targets-reference-infra.md) | Deployment targets & reference infra | implemented |
| [017](./017-cicd-github-actions.md) | CI/CD: GitHub Actions | implemented |
| [018](./018-release-and-versioning.md) | Release & versioning: tag-driven SemVer, operator upgrade contract | implemented |
| [019](./019-cli-surface-and-runtime-config.md) | CLI surface & runtime config (the scaffold's contract) | `draft` |
| [020](./020-dev-environment.md) | Dev environment: one entry point, guards committed | implemented |

The six implemented ones are the tooling that shipped ahead of the code: the `Caddyfile` and compose stack (009, 010), the provider-agnostic packaging and the deliberate absence of a `deploy.yml` (016), the four workflows and release-notes config (017, 018), and the `Makefile` + committed guards (020).

**019 is a special case.** It is `draft` because no binary implements it — but the `Makefile`, `Dockerfile` and `ci.yml` already invoke the surface it describes. Editing it in place is allowed and still costs the three-file change the ADR itself calls for: binary, `Makefile`, ADR, one PR.
