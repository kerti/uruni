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
| [001](./001-one-go-binary-single-origin.md) | Overall shape: one Go binary, single origin | implemented |
| [002](./002-languages-go-and-react.md) | Languages: Go (server) + TypeScript/React (client) | implemented |
| [003](./003-frontend-react-spa-backend-go.md) | Frontend: React SPA (Vite) + backend: Go | implemented |
| [004](./004-database-sqlite-only.md) | Database: SQLite only through 0.x | `draft` |
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
| [019](./019-cli-surface-and-runtime-config.md) | CLI surface & runtime config (the scaffold's contract) | implemented |
| [020](./020-dev-environment.md) | Dev environment: one entry point, guards committed | implemented |
| [021](./021-http-routing-chi.md) | HTTP routing: chi, adopted at M4 | `draft` |
| [022](./022-logging-slog.md) | Logging: stdlib `log/slog`, adopted at M1.2 | implemented |

Six were implemented by the tooling that shipped ahead of the code: the `Caddyfile` and compose stack (009, 010), the provider-agnostic packaging and the deliberate absence of a `deploy.yml` (016), the four workflows and release-notes config (017, 018), and the `Makefile` + committed guards (020). The M1.1 scaffold added three more — the one-binary shape and the two language/framework calls (001, 002, 003) now have running code behind them. M1.2 closed the loop on the two the tooling had been addressing on credit: the CLI surface and its env-var config (019) and the logger they configure (022).

**A number is claimed when its ADR is written**, first come. No issue or plan reserves one in advance — a reservation would either block the next decision that needs a number or dictate the order decisions get made in.

**019 was the repo's special case, and no longer is.** It sat `draft` while the `Makefile`, `Dockerfile` and `ci.yml` already invoked the surface it described — tooling addressing a binary that only partly existed. M1.2 built the surface, so the tag is off: it now changes only by a superseding ADR, and a rename costs the three-file change the ADR itself calls for (binary, `Makefile`, ADR) in one PR.
