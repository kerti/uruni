# Uruni — Architecture Decision Records

One file per decision. Each ADR is a single call with its context, the decision, and its consequences. The overview that frames them — the constraints, the stack at a glance, the production topology, and what is deliberately *not* decided yet — lives in [`../Tech-Design.md`](../Tech-Design.md).

## How an ADR changes

**Numbers are permanent** — never reused, never renumbered. Prose across the repo refers to decisions as "ADR-0NN", so a number has to mean one thing forever.

**Text depends on the tag:**

- **`draft`** — no code implements this decision yet. **Edit it in place.** Grilling a draft, changing your mind, tightening the wording: all fine, no ceremony.
- **no tag (implemented)** — a slice has shipped code behind this decision. **Change it only by adding a superseding ADR** and marking this one superseded.

**The tag comes off in the PR that implements the ADR** — that's part of the definition of done for a slice (see [`../../CLAUDE.md`](../../CLAUDE.md)). Migrations run the other way round: **looser** than an ADR through `0.x` — one file, edited in place ([ADR-025](./025-one-migration-file-until-1.0.md)) — then stricter than one from the first production deploy, when that file freezes for good.

Standing decisions that no ADR owns go in [`../Decisions.md`](../Decisions.md).

## The decisions

| # | Decision | Stage |
|---|---|---|
| [001](./001-one-go-binary-single-origin.md) | Overall shape: one Go binary, single origin | implemented |
| [002](./002-languages-go-and-react.md) | Languages: Go (server) + TypeScript/React (client) | implemented |
| [003](./003-frontend-react-spa-backend-go.md) | Frontend: React SPA (Vite) + backend: Go | implemented |
| [004](./004-database-sqlite-only.md) | Database: SQLite only through 0.x | implemented |
| [005](./005-data-access-sqlc.md) | Data access: sqlc | implemented |
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
| [019](./019-cli-surface-and-runtime-config.md) | CLI surface & runtime config (the scaffold's contract) | implemented |
| [020](./020-dev-environment.md) | Dev environment: one entry point, guards committed | implemented |
| [021](./021-http-routing-chi.md) | HTTP routing: chi, adopted at M4 | `draft` |
| [022](./022-logging-slog.md) | Logging: stdlib `log/slog`, adopted at M1.2 | implemented |
| [023](./023-agent-operating-model.md) | Agent operating model: one orchestrator, pinned role agents | implemented |
| [024](./024-schema-conventions.md) | Schema conventions: STRICT SQLite, integer ids, two time types | implemented |
| [025](./025-one-migration-file-until-1.0.md) | One migration file, edited in place, until v1.0.0 | implemented |

The **Stage** column is the record, and it is all this index says about implementation: `draft` means editable in place, `implemented` means superseding-ADR-only. *Which* slice put code behind a given ADR belongs in that ADR and in the PR that dropped its tag — restated here, this index becomes a changelog.

**A number is claimed when its ADR is written**, first come. No issue or plan reserves one in advance — a reservation would either block the next decision that needs a number or dictate the order decisions get made in.
