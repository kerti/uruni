# ADR-019 — CLI surface & runtime config (the scaffold's contract)

**Status:** Accepted · [ADR index](./README.md)

**Context.** The `Makefile`, `ci.yml`, and the repo's hooks were written *before* the scaffold and already invoke a binary that doesn't exist yet. That inversion is deliberate — the tooling is easier to reason about than the code it will drive — but it only works if the surface it assumes is pinned down. Otherwise the scaffold invents subcommand names ad hoc and the tooling silently rots on day one.

**Decision.** One binary, `uruni`, built from `./cmd/uruni`, with these subcommands and nothing else:

| Subcommand | Purpose |
|---|---|
| `serve` | Run the HTTP server — JSON API, SSR public report, embedded SPA. Applies pending migrations on boot. |
| `migrate up` / `down` / `status` | goose, embedded. `down` rolls back one step. |
| `create-user <email> <password>` | Create or reset a local account (argon2id). The only way to mint a login. |
| `seed-e2e` | Reset + migrate + seed the Playwright fixture database. Dev-only; refuses to run against a non-throwaway DB. |
| `version` | Print the version/commit — the operator's half of the upgrade contract ([ADR-018](./018-release-and-versioning.md)). |
| `healthcheck` | Probe `/healthz` on the local `PORT`; exit 0 when healthy. Exists **only** because the runtime image is distroless — no shell, no curl — so a container `HEALTHCHECK` has nothing else to call. Added 2026-08-09. |

Runtime config is environment variables only (no config file):

| Variable | Default | Meaning |
|---|---|---|
| `URUNI_DB` | `./uruni.db` | SQLite file path. |
| `DATABASE_URL` | — | Postgres DSN. When set, **takes precedence** over `URUNI_DB` ([ADR-004](./004-database-sqlite-default-postgres-option.md)). |
| `PORT` | `8080` | Listen port. |
| `URUNI_BASE_URL` | — | Public origin, used to build the shareable report link. |
| `URUNI_SESSION_SECRET` | — | Session signing key. Server refuses to start on the placeholder value. |
| `SMTP_URL` | — | Optional, for emailed backups ([ADR-012](./012-backup-and-export.md)). |

`GET /healthz` is unauthenticated, returns 200 when the server is up, and is what the dev-server readiness poll and the container healthcheck use.

The compose stack does not restate the healthcheck — the image carries its own `HEALTHCHECK`, and `caddy` waits on `condition: service_healthy`. The image must also ship `/data`, `/uploads` and `/backups` **owned by uid 65532**: Docker seeds a fresh named volume from the image's directory at that path, so if the path is absent the volume lands `root:root` and the nonroot binary can neither open SQLite for writing nor store a receipt photo.

**Consequences.** The Makefile is the executable form of this table, so a rename is a three-file change — binary, Makefile, this ADR — landing in one PR. `seed-e2e` guarding its own blast radius matters because the e2e target deletes a database file. Auto-migrate-on-`serve` keeps self-hosting to `docker compose up` with no migration step for the operator, at the cost of a slower first boot after an upgrade.
