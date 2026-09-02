# ADR-019 — CLI surface & runtime config (the scaffold's contract)

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** The `Makefile`, `ci.yml`, and the repo's hooks were written *before* the scaffold and already invoke a binary that doesn't exist yet. That inversion is deliberate — the tooling is easier to reason about than the code it will drive — but it only works if the surface it assumes is pinned down. Otherwise the scaffold invents subcommand names ad hoc and the tooling silently rots on day one. That inversion made this the one ADR that sat `draft` while tooling already invoked it; M1.1–M1.3 built the surface (`serve`, `version`, `healthcheck`, then `migrate`), and the tag came off with it.

**Decision.** One binary, `uruni`, built from `./cmd/uruni`, with these subcommands and nothing else:

| Subcommand | Purpose | Lands |
|---|---|---|
| `serve` | Run the HTTP server — JSON API, SSR public report, embedded SPA. Applies pending migrations on boot. | M1.1 / M1.2 |
| `migrate up` / `down` / `status` | goose, embedded. `down` rolls back one step. | M1.3, with the store |
| `create-user <email> <password>` | Create or reset a local account (argon2id). | **M5** — needs a users table and argon2id |
| `seed-e2e` | Reset + migrate + seed the Playwright fixture database. Dev-only; refuses to run against a non-throwaway DB. | **when fixtures exist** — needs a domain to seed |
| `version` | Print the version/commit — the operator's half of the upgrade contract ([ADR-018](./018-release-and-versioning.md)). | M1.2 |
| `healthcheck` | Probe `/healthz` on the local `PORT`; exit 0 when healthy. Exists **only** because the runtime image is distroless — no shell, no curl — so a container `HEALTHCHECK` has nothing else to call. Added 2026-08-09. | M1.2 |

The **Lands** column is what makes the surface a contract rather than a wish: a subcommand the `Makefile` calls before its milestone fails with `unknown command`, which is the honest answer. `uruni` with no arguments lists only the subcommands that exist today.

Runtime config is environment variables only (no config file, no third-party config library):

| Variable | Default | Meaning |
|---|---|---|
| `URUNI_DB` | `./uruni.db` | SQLite file path. The only store — there is no `DATABASE_URL` ([ADR-004](./004-database-sqlite-only.md)). |
| `PORT` | `8080` | Listen port. |
| `URUNI_BASE_URL` | — | Public origin: the shareable report link is built from it, and its scheme decides the session cookie's `Secure` flag ([ADR-007](./007-auth-local-email-password.md)). Must be absolute. **Required** — the server refuses to start unset or on the placeholder value. |
| `SMTP_URL` | — | Optional, for emailed backups ([ADR-012](./012-backup-and-export.md)). Parsed and validated on boot; delivery is M8. |
| `URUNI_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` ([ADR-022](./022-logging-slog.md)). |
| `URUNI_LOG_FORMAT` | `text` | `text` \| `json` ([ADR-022](./022-logging-slog.md)). |

One `internal/config` package owns the table, with a single `Load() (Config, error)`, so **`os.Getenv` appears in exactly one place** in the binary and the table above can be checked against one file instead of against a grep of the tree. `Load` fails on the *first* problem: an operator fixes one line of `.env` and re-runs, and a wall of errors on boot reads worse than one line. Error messages name the variable but **never echo the value of `SMTP_URL`** — it carries a password, and a boot error is exactly what gets pasted into an issue.

`GET /healthz` is unauthenticated, returns 200 when the server is up, and is what the dev-server readiness poll and the container healthcheck use.

The compose stack does not restate the healthcheck — the image carries its own `HEALTHCHECK`, and `caddy` waits on `condition: service_healthy`. The image must also ship `/data`, `/uploads` and `/backups` **owned by uid 65532**: Docker seeds a fresh named volume from the image's directory at that path, so if the path is absent the volume lands `root:root` and the nonroot binary can neither open SQLite for writing nor store a receipt photo.

`version` reports what the linker stamped: `VERSION` and `COMMIT` build-args → `-X main.version` / `-X main.commit`, filled by `release.yml` from the pushed tag and its SHA. `COMMIT` needs a build-arg of its own because `.dockerignore` keeps `.git` out of the build context, so Go's own VCS stamping has nothing to read; a local `go build` has the reverse (no stamp, but a readable `.git`) and falls back to `debug.ReadBuildInfo`. An unstamped build reports `dev`, which is what it is.

**Consequences.** The Makefile is the executable form of this table, so a rename is a three-file change — binary, Makefile, this ADR — landing in one PR. `seed-e2e` guarding its own blast radius matters because the e2e target deletes a database file. Auto-migrate-on-`serve` keeps self-hosting to `docker compose up` with no migration step for the operator, at the cost of a slower first boot after an upgrade. Requiring `URUNI_BASE_URL` before *any* subcommand runs costs a bare `go run ./cmd/uruni healthcheck` outside `make` (which exports `.env`), and buys the guarantee that no instance boots un-configured — one required variable is the whole "did the operator edit `.env` at all?" check, and this is the variable whose wrong value is otherwise invisible until someone else opens the report link.

## Amendments

An amendment corrects a statement of fact about the code that has since become false. It never changes a decision, a trade-off or an accepted cost — that is still a superseding ADR. See the [ADR index](./README.md) for the rule.

**2026-08-28 ([#114](https://github.com/kerti/uruni/issues/114))** — the `create-user` row's Purpose column said "Create or reset a local account (argon2id). **The only way to mint a login.**"

`POST /api/register` now mints the login: it is the one-shot bootstrap account ADR-030 decision 2 requires, and it lands before `create-user` does — `create-user` still has nowhere to run until a users table exists, so it stays unimplemented at M5 despite appearing in this table since M1. The row's substance is unchanged: `create-user` is still the described command, still argon2id, still lands whenever a password-reset path is built (plausibly M10 — `ROADMAP.md`'s M9 is the last milestone listed, not necessarily the last before `v1.0.0`). Only the claim that it is the *only* way became false the moment registration shipped, so the sentence is dropped rather than reworded into something this ADR was never about deciding.

**2026-08-28 ([#114](https://github.com/kerti/uruni/issues/114))** — the runtime-config table called `URUNI_SESSION_SECRET` a "**Session signing key**", and the Consequences paragraph said requiring it buys the guarantee that "no instance ever **signs a session** with a value published in this repo."

Sessions turned out not to be signed. The session cookie this milestone shipped carries an opaque `crypto/rand` token and nothing else; every byte of session state lives in the `session` table server-side, so there is no cookie payload for a key to sign. `Config.SessionSecret` is still validated at boot and is, as of this PR, read by no other code. The requirement itself is untouched — an instance still refuses to start on the placeholder — so this is a wording correction, not a change of decision. Whether the variable earns its keep at all is a separate question this amendment does not answer.

**2026-09-02 ([#119](https://github.com/kerti/uruni/issues/119))** — the runtime-config table had a `URUNI_SESSION_SECRET` row ("Session secret. Server refuses to start unset or on the placeholder value."); the `Load` paragraph said errors never echo "`URUNI_SESSION_SECRET` or `SMTP_URL`" because "both are credentials"; and Consequences ended on requiring `URUNI_SESSION_SECRET` before any subcommand, which "buys the guarantee that no instance ever derives a session secret from a value published in this repo."

The variable is gone. The amendment above left open whether it earned its keep; it did not. Nothing read `Config.SessionSecret` outside this package's own tests, and nothing was going to: the session cookie carries an opaque `crypto/rand` token with every byte of state server-side, so there is no payload to key. Making it earn its keep by looking sessions up under `HMAC(secret, token)` was considered and rejected — it defends against an attacker who can read the SQLite file but not act on it, and that file also holds the whole ledger and `user.password_hash`; the "log out everywhere" lever it would buy is already `DELETE FROM session;` against the one account this instance has; and it turns a variable that costs nothing to lose into one that silently invalidates every session when a DB backup is restored without its matching `.env`.

**`URUNI_BASE_URL` becomes required in its place**, refused on the `.env.example` placeholder exactly as the secret was. The retired variable was the *only* required one, so it was carrying the "did the operator configure this instance at all?" check under a name that had nothing to do with it; dropping it with nothing in its place would let a self-hoster who edits nothing boot clean and wrong. `URUNI_BASE_URL` is the honest home for that check: it is already read, its placeholder is already recognisable, and getting it wrong fails *visibly* — a report link that works nowhere but the operator's own browser — where a wrong session secret produced nothing at all. It is not a credential, so its error echoes the value ([Decisions](../Decisions.md)).

This goes further than the amendment rule's letter — it retires a named cost, which the [index](./README.md) reserves for a superseding ADR. Recorded here on the maintainer's call: ADR-019 is the whole CLI-and-runtime-config contract, and restating that entire table to delete one row is the ceremony `CLAUDE.md`'s prime directive exists to refuse. The decision this ADR makes — env vars only, one `internal/config`, one `Load`, first error wins, refuse to boot un-configured — is unchanged; only which variable carries the last of those is different.
