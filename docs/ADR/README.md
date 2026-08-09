# Uruni — Architecture Decision Records

One file per decision. Each ADR is a single call with its context, the decision, and its consequences. The overview that frames them — the constraints, the stack at a glance, the production topology, and what is deliberately *not* decided yet — lives in [`../Tech-Design.md`](../Tech-Design.md).

ADR numbers are permanent. To change a decision, add a new ADR that supersedes the old one and mark the old one superseded; don't rewrite history in place. Standing decisions that no ADR owns go in [`../Decisions.md`](../Decisions.md).

| # | Decision | Status |
|---|---|---|
| [001](./001-one-go-binary-single-origin.md) | Overall shape: one Go binary, single origin | Accepted |
| [002](./002-languages-go-and-react.md) | Languages: Go (server) + TypeScript/React (client) | Accepted |
| [003](./003-frontend-react-spa-backend-go.md) | Frontend: React SPA (Vite) + backend: Go | Accepted |
| [004](./004-database-sqlite-default-postgres-option.md) | Database: SQLite default, Postgres option | Accepted |
| [005](./005-data-access-sqlc.md) | Data access: sqlc | Accepted |
| [006](./006-money-integer-minor-units.md) | Money is integer minor units (never floats) | Accepted |
| [007](./007-auth-local-email-password.md) | Auth: local email/password now, OIDC later | Accepted |
| [008](./008-pwa-no-offline-data.md) | PWA: installable shell, no offline data | Accepted |
| [009](./009-reverse-proxy-caddy.md) | Reverse proxy & TLS: Caddy | Accepted |
| [010](./010-packaging-and-deployment.md) | Packaging & deployment | Accepted |
| [011](./011-receipt-photos-local-volume.md) | Receipt photos: local volume | Accepted |
| [012](./012-backup-and-export.md) | Backup & export implementation | Accepted |
| [013](./013-scheduling-in-process.md) | Scheduling: in-process | Accepted |
| [014](./014-localization-indonesian-first.md) | Localization: Indonesian-first, strings centralized | Accepted |
| [015](./015-testing-money-math.md) | Testing: prioritize the money math | Accepted |
| [016](./016-deployment-targets-reference-infra.md) | Deployment targets & reference infra | Accepted |
| [017](./017-cicd-github-actions.md) | CI/CD: GitHub Actions | Accepted |
| [018](./018-release-and-versioning.md) | Release & versioning: tag-driven SemVer, operator upgrade contract | Accepted |
| [019](./019-cli-surface-and-runtime-config.md) | CLI surface & runtime config (the scaffold's contract) | Accepted |
| [020](./020-dev-environment.md) | Dev environment: one entry point, guards committed | Accepted |
