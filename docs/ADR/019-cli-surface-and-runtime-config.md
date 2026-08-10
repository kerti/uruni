# ADR-019 — CLI surface & runtime config (the scaffold's contract)

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** The `Makefile`, `ci.yml`, and the repo's hooks were written *before* the scaffold and already invoke a binary that doesn't exist yet. That inversion is deliberate — the tooling is easier to reason about than the code it will drive — but it only works if the surface it assumes is pinned down. Otherwise the scaffold invents subcommand names ad hoc and the tooling silently rots on day one. That inversion made this the one ADR that sat `draft` while tooling already invoked it; M1.1–M1.3 built the surface (`serve`, `version`, `healthcheck`, then `migrate`), and the tag came off with it.

**Decision.** One binary, `uruni`, built from `./cmd/uruni`, with these subcommands and nothing else:

| Subcommand | Purpose | Lands |
|---|---|---|
| `serve` | Run the HTTP server — JSON API, SSR public report, embedded SPA. Applies pending migrations on boot. | M1.1 / M1.2 |
| `migrate up` / `down` / `status` | goose, embedded. `down` rolls back one step. | M1.3, with the store |
| `create-user <email> <password>` | Create or reset a local account (argon2id). The only way to mint a login. | **M5** — needs a users table and argon2id |
| `seed-e2e` | Reset + migrate + seed the Playwright fixture database. Dev-only; refuses to run against a non-throwaway DB. | **when fixtures exist** — needs a domain to seed |
| `version` | Print the version/commit — the operator's half of the upgrade contract ([ADR-018](./018-release-and-versioning.md)). | M1.2 |
| `healthcheck` | Probe `/healthz` on the local `PORT`; exit 0 when healthy. Exists **only** because the runtime image is distroless — no shell, no curl — so a container `HEALTHCHECK` has nothing else to call. Added 2026-08-09. | M1.2 |

The **Lands** column is what makes the surface a contract rather than a wish: a subcommand the `Makefile` calls before its milestone fails with `unknown command`, which is the honest answer. `uruni` with no arguments lists only the subcommands that exist today.

Runtime config is environment variables only (no config file, no third-party config library):

| Variable | Default | Meaning |
|---|---|---|
| `URUNI_DB` | `./uruni.db` | SQLite file path. The only store — there is no `DATABASE_URL` ([ADR-004](./004-database-sqlite-only.md)). |
| `PORT` | `8080` | Listen port. |
| `URUNI_BASE_URL` | — | Public origin, used to build the shareable report link. Must be absolute if set. |
| `URUNI_SESSION_SECRET` | — | Session signing key. Server refuses to start unset or on the placeholder value. |
| `SMTP_URL` | — | Optional, for emailed backups ([ADR-012](./012-backup-and-export.md)). Parsed and validated on boot; delivery is M8. |
| `URUNI_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` ([ADR-022](./022-logging-slog.md)). |
| `URUNI_LOG_FORMAT` | `text` | `text` \| `json` ([ADR-022](./022-logging-slog.md)). |

One `internal/config` package owns the table, with a single `Load() (Config, error)`, so **`os.Getenv` appears in exactly one place** in the binary and the table above can be checked against one file instead of against a grep of the tree. `Load` fails on the *first* problem: an operator fixes one line of `.env` and re-runs, and a wall of errors on boot reads worse than one line. Error messages name the variable but **never echo the value of `URUNI_SESSION_SECRET` or `SMTP_URL`** — both are credentials, and a boot error is exactly what gets pasted into an issue.

`GET /healthz` is unauthenticated, returns 200 when the server is up, and is what the dev-server readiness poll and the container healthcheck use.

The compose stack does not restate the healthcheck — the image carries its own `HEALTHCHECK`, and `caddy` waits on `condition: service_healthy`. The image must also ship `/data`, `/uploads` and `/backups` **owned by uid 65532**: Docker seeds a fresh named volume from the image's directory at that path, so if the path is absent the volume lands `root:root` and the nonroot binary can neither open SQLite for writing nor store a receipt photo.

`version` reports what the linker stamped: `VERSION` and `COMMIT` build-args → `-X main.version` / `-X main.commit`, filled by `release.yml` from the pushed tag and its SHA. `COMMIT` needs a build-arg of its own because `.dockerignore` keeps `.git` out of the build context, so Go's own VCS stamping has nothing to read; a local `go build` has the reverse (no stamp, but a readable `.git`) and falls back to `debug.ReadBuildInfo`. An unstamped build reports `dev`, which is what it is.

**Consequences.** The Makefile is the executable form of this table, so a rename is a three-file change — binary, Makefile, this ADR — landing in one PR. `seed-e2e` guarding its own blast radius matters because the e2e target deletes a database file. Auto-migrate-on-`serve` keeps self-hosting to `docker compose up` with no migration step for the operator, at the cost of a slower first boot after an upgrade. Requiring `URUNI_SESSION_SECRET` before *any* subcommand runs costs a bare `go run ./cmd/uruni healthcheck` outside `make` (which exports `.env`), and buys the guarantee that no instance ever signs a session with a value published in this repo.
