# Uruni — Architecture Decision Records

One file per decision. Each ADR is a single call with its context, the decision, and its consequences. The overview that frames them — the constraints, the stack at a glance, the production topology, and what is deliberately *not* decided yet — lives in [`../Tech-Design.md`](../Tech-Design.md).

## How an ADR changes

**Numbers are permanent** — never reused, never renumbered. Prose across the repo refers to decisions as "ADR-0NN", so a number has to mean one thing forever.

**Text depends on the tag:**

- **`draft`** — the decision is still editable in place. **Edit it in place.** Grilling a draft, changing your mind, tightening the wording: all fine, no ceremony.
- **no tag (implemented)** — a slice has shipped code behind this decision. **Change it only by adding a superseding ADR** and marking this one superseded.

**An implemented ADR may be *amended*, and only for this:** to correct a statement of fact about the code that has since become false. The correction is made in place, so a first-time reader never reads a lie, and the old wording is recorded in an `## Amendments` section at the foot of the ADR with its date and the issue that caused it — history the doc itself carries, not only git. **An amendment never changes a decision, a trade-off, or an accepted cost; that is still a superseding ADR.** The test is one question: *would this ADR have been written differently if we had known?* No — amendment. Yes — supersede. Like an edit to a `draft`, an amendment ships in the same PR as the code that makes it true. (First used by [ADR-027](./027-ledger-domain-boundary.md), [#103](https://github.com/kerti/uruni/issues/103).)

**The tag comes off when the decision is fully implemented — which is the last slice of the milestone that implements it, not the first.** For an ADR one PR implements end to end, those are the same PR and nothing changes. For one that several slices build out, holding the tag to the milestone's close is deliberate: an ADR frozen after slice one turns everything slices two through nine *learn* into a superseding ADR against a decision no shipped code had tested yet, which is the ceremony `draft` exists to avoid.

**One rule comes with holding it.** Once any code exists behind a `draft` ADR, the tag no longer means "free to edit" — it means "editable, but something depends on this now", and that difference is invisible in the tag itself. So from the first slice on, **an edit to that ADR ships in the same PR as the code that makes it true.** Prose drifting ahead of the implementation is the failure mode, not the editing.

The milestone's final PR then does a real reconciliation pass — read each ADR against what actually shipped, fix what drifted, drop the tags — rather than a checkbox on every slice. Dropping a tag stays the orchestrator's and the maintainer's call, never an agent's (see [`../../CLAUDE.md`](../../CLAUDE.md)). Migrations run the other way round: **looser** than an ADR through `0.x` — one file, edited in place ([ADR-025](./025-one-migration-file-until-1.0.md)) — then stricter than one from the first production deploy, when that file freezes for good.

Standing decisions that no ADR owns go in [`../Decisions.md`](../Decisions.md).

## The decisions

| # | Decision | Stage |
|---|---|---|
| [001](./001-one-go-binary-single-origin.md) | Overall shape: one Go binary, single origin | implemented |
| [002](./002-languages-go-and-react.md) | Languages: Go (server) + TypeScript/React (client) | implemented |
| [003](./003-frontend-react-spa-backend-go.md) | Frontend: React SPA (Vite) + backend: Go | implemented |
| [004](./004-database-sqlite-only.md) | Database: SQLite only through 0.x | implemented |
| [005](./005-data-access-sqlc.md) | Data access: sqlc | implemented |
| [006](./006-money-integer-minor-units.md) | Money is integer minor units (never floats) | implemented |
| [007](./007-auth-local-email-password.md) | Auth: local email/password now, OIDC later | implemented |
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
| [021](./021-http-routing-chi.md) | HTTP routing: chi, adopted at M4 | implemented |
| [022](./022-logging-slog.md) | Logging: stdlib `log/slog`, adopted at M1.2 | implemented |
| [023](./023-agent-operating-model.md) | Agent operating model: one orchestrator, pinned role agents | implemented |
| [024](./024-schema-conventions.md) | Schema conventions: STRICT SQLite, integer ids, two time types | implemented |
| [025](./025-one-migration-file-until-1.0.md) | One migration file, edited in place, until v1.0.0 | implemented |
| [026](./026-money-package.md) | Money package: overflow-checked `Amount`, arithmetic only | implemented |
| [027](./027-ledger-domain-boundary.md) | Ledger domain boundary: one package, transactional writes | implemented |
| [028](./028-testing-the-trust-core.md) | Testing the trust core: `internal/money` and `internal/ledger` | implemented |
| [029](./029-reversing-a-dues-payment.md) | Reversing a dues payment: linked adjustment row, not netting | implemented |
| [030](./030-multi-fund-scoping.md) | Multi-fund scoping: implicit fund resolution, single-account auth | implemented |

The **Stage** column is the record, and it is all this index says about implementation: `draft` means editable in place, `implemented` means superseding-ADR-only. *Which* slice put code behind a given ADR belongs in that ADR and in the PR that dropped its tag — restated here, this index becomes a changelog.

**A number is claimed when its ADR is written**, first come. No issue or plan reserves one in advance — a reservation would either block the next decision that needs a number or dictate the order decisions get made in.
