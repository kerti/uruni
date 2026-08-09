# Uruni — Technical Design

**Version 0.3 · 2026-08-09 · Status: draft (stack confirmed: Go + React)**

Companion to [`PRD.md`](./PRD.md). The PRD owns *what* and *why*; this doc owns *how*. Written as lightweight ADRs (Architecture Decision Records): each is a single decision with context, options, the call, and consequences.

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

---

## ADR-001 — Overall shape: one Go binary, single origin

**Context.** Self-hosters should run as little as possible; a split SPA + separate API adds CORS, a static host, and more to deploy.

**Decision.** In production the **Go server is the single origin**: it exposes the JSON API, **server-renders the public report** (Go `html/template`), and **serves the React SPA** — with the built React assets **embedded into the binary via `embed.FS`**. One self-contained binary, one container.

**Dev vs prod.** In development, frontend and backend run **separately** for DX: Vite dev server (React hot-reload) with `/api` and `/report` proxied to the Go server. In production they collapse into the one binary.

**Consequences.** No CORS in prod; SPA client-side routes fall back to `index.html` except `/api/*` and `/report/*`. The build pipeline must compile the React bundle before the Go build so `embed.FS` can pick it up.

## ADR-002 — Languages: Go (server) + TypeScript/React (client)

**Decision.** **Go** for the backend, **TypeScript + React** for the client. No shared type generation across the boundary in v1 (kept simple, as in Balances); if drift becomes painful, generate TS types from the API later.

**Consequences.** Two languages, but both training-dense and familiar. Discipline on the API contract (document request/response shapes).

## ADR-003 — Frontend: React SPA (Vite) + backend: Go

**Context.** Built with AI assistance, so stack fluency is a first-class criterion; Balances is React + Go.

**Decision.** **React** (via Vite) for the treasurer app as a PWA; **Go** for the server. Confirmed (supersedes the earlier SvelteKit proposal).

**Why.** Highest AI-codegen accuracy, proven Balances patterns, Go's single-binary self-host. The public report is plain server-rendered Go templates (robust, no React needed for that page).

**Consequences.** Anchors the repo layout (`/web` React app, Go module at root or `/server`). Component/library ecosystem is React's (shadcn/ui etc. available if wanted).

## ADR-004 — Database: SQLite default, Postgres option

**Context.** One tiny instance per community, single writer, integrity-critical; self-host simplicity is priority. Balances uses Postgres on Neon.

**Decision.** **SQLite** as the default (no separate DB container; the whole deploy can be one binary + a file). Keep SQL portable so **PostgreSQL** is a config swap — the maintainer's own hosted instance may use **Neon Postgres** (see ADR-016).

**Consequences.** SQLite file on a mounted volume; use WAL mode. Staying within portable SQL keeps the Postgres path open. **Driver: pure-Go SQLite (`modernc.org/sqlite`)** so the image builds with `CGO_ENABLED=0` for a static/distroless binary (a cgo driver like `mattn/go-sqlite3` would force cgo and a heavier base image). Worth a one-line ADR at scaffold.

## ADR-005 — Data access: sqlc

**Context.** Want type-safe, transparent SQL in Go without a heavy ORM; must target both SQLite and Postgres.

**Decision.** **sqlc** — write SQL, generate type-safe Go. Migrations via **goose** (or golang-migrate).

**Alternative.** GORM (more magic, heavier). sqlc keeps the SQL explicit, which suits money code where you want to see exactly what runs.

**Consequences.** SQL lives in versioned files; keep dialect differences minimal to preserve SQLite↔Postgres portability.

## ADR-006 — Money is integer minor units (never floats)

**Decision.** Store and compute all amounts as **`int64`** integer rupiah (IDR is effectively whole-rupiah; if sub-rupiah ever matters, migrate the whole system to integer sen). No float arithmetic beyond display.

**Consequences.** A small money package in Go (add/subtract/parse) and formatting on the client via `Intl.NumberFormat('id-ID')`. Balances are derived by summing the integer ledger; reconciliation compares integers exactly.

## ADR-007 — Auth: local email/password now, OIDC later

**Decision.** **Local auth** — email/password hashed with **argon2id** (`golang.org/x/crypto/argon2`), server-side sessions with httpOnly secure cookies (e.g. **alexedwards/scs**, or a minimal hand-rolled store given it's effectively one user). OIDC is an additive option later (`coreos/go-oidc`).

**Why.** Zero external setup for the host; the public report page (7.9) is unauthenticated, so login only guards the treasurer's writes.

**Consequences.** Ship secure defaults: login rate-limiting, strong cookie flags, HTTPS-only via Caddy.

## ADR-008 — PWA: installable shell, no offline data

**Decision.** Web app manifest + a **minimal service worker** (via `vite-plugin-pwa`) that caches only the app shell and shows a clear "butuh koneksi" state offline. **No IndexedDB / offline data** (PRD 7.2).

**Consequences.** The connection-required rule erases offline-sync — the hardest part of PWAs — by construction.

## ADR-009 — Reverse proxy & TLS: Caddy

**Decision.** **Caddy** in front of the Go app, automatic Let's Encrypt certificates. If an operator hosts under a wildcard subdomain, Caddy's **DNS-challenge** covers it.

**Consequences.** One extra container in compose; near-zero TLS config for a host with a domain.

## ADR-010 — Packaging & deployment

**Decision.**
- Multi-stage **Dockerfile**: build the React bundle, then `go build` embedding it → a small **distroless/scratch** image with a single binary. Publish to **GHCR**.
- A single **`docker-compose.yml`**: `app` + `caddy` (+ optional `postgres`). SQLite file, uploaded receipt photos, and backups live on **named volumes**.
- Config via **environment variables** / `.env` (base URL, session secret, optional SMTP, optional Postgres/Neon URL).

**Consequences.** This compose file *is* the make-or-break deployment UX; treat it as a first-class deliverable with a short README.

## ADR-011 — Receipt photos: local volume

**Decision.** Store optional uploaded images on a mounted **local volume**, path referenced in the DB; enforce a size cap and downscale on upload. Avoids an object-storage dependency.

**Consequences.** Backups must cover (or the docs must call out) the uploads volume; the JSON export references photo files rather than embedding them.

## ADR-012 — Backup & export implementation

**Decision.**
- **JSON export**: Go `encoding/json` serializes all tables into one **versioned** document; a matching **import** restores it.
- **Excel**: generated server-side with **excelize** — human-readable, not the restore path.
- **Scheduled dumps**: in-process scheduler writes periodic JSON to the backup volume (ADR-013).
- **Email delivery**: optional, via `net/smtp` (or **gomail**) using host-configured SMTP.

**Consequences.** The JSON schema needs a `version` field and a documented shape (own sub-doc when building).

## ADR-013 — Scheduling: in-process

**Decision.** An **in-process scheduler** in the Go app (**robfig/cron** or a `time.Ticker`). No Redis, no separate worker.

**Consequences.** Fewer moving parts; scheduled work pauses if the app is down (acceptable — restart resumes).

## ADR-014 — Localization: Indonesian-first, strings centralized

**Decision.** Ship **Indonesian only** for v1, but centralize copy: **react-i18next** (or a light equivalent) on the client, and centralized message strings for the Go-rendered report (`go-i18n` if needed). A second language stays additive.

**Consequences.** Slight upfront structure, no runtime cost, future-proof.

## ADR-015 — Testing: prioritize the money math

**Decision.** **`go test`** for the backend, with the **ledger/reconciliation logic as the highest-priority target**; **Vitest** for client units; **Playwright** for a few end-to-end flows (record → balance → reconcile; public report renders). The money package and reconciliation are must-have coverage.

**Consequences.** A small but non-negotiable suite around PRD 7.8.

## ADR-016 — Deployment targets & reference infra

**Context.** Uruni is open-source and self-hostable; it must not be coupled to any one provider. The maintainer also needs to actually host his wife's instance.

**Decision.** The **product ships provider-agnostic** (Docker image + compose, bring-your-own-domain, SQLite or any Postgres). A maintainer's own hosted instance (e.g. a small VPS, or a host like Fly.io with a managed Postgres such as Neon) is a **reference deployment** documented by example, not a requirement. All deployment-specific config — domains, provider projects, secrets — lives in a **private ops note / `.env` outside this repo**, never in the product spec.

**Note.** If a maintainer hosts several communities' instances, they — *as operator* — hold those instances' data: an operator choice, distinct from the project itself holding nothing.

## ADR-017 — CI/CD: GitHub Actions

**Context.** Public GitHub repo, AGPL-3.0, solo maintainer, release by pushing tags.

**Decision.**
- **`ci.yml`** on PRs and pushes to `main`: Go lint (golangci-lint) + `go test`, frontend lint/typecheck + Vitest, and a build check. A local **`make check`** mirrors CI so green-locally ≈ green-in-CI.
- **`workflows/release.yml`** on tag push (`v*`): build the single-origin image (multi-arch `linux/amd64,linux/arm64`, cross-compiled rather than QEMU-emulated) and publish to **GHCR** (`build-once`), so any tag's artifact can later be promoted without a rebuild. The tag is passed in as the `VERSION` build-arg — otherwise `uruni version` reports `dev` forever and the operator's upgrade contract (ADR-018) is a lie. **There is no `deploy.yml`:** deploying a published image to the maintainer's instance is a manual ops step kept out of this repo (ADR-016).
- **Secret scanning** (gitleaks) and **CodeQL** on a public repo; **Dependabot** for deps.
- Branch protection on `main`: require PR + green CI, linear history (squash), admin bypass on (never lock out the solo maintainer).

**Deliberately simpler than Balances:** one deploy target (the maintainer's instance), **no** preview/demo/production environment split and **no** nightly upgrade-contract rehearsal until there's real production data to protect. Add them only when warranted.

**Consequences.** `.github/` carries `workflows/{ci,release,codeql,gitleaks}.yml`, plus `release.yml` (the release-*notes* config — note the name collision with the workflow), `dependabot.yml`, `PULL_REQUEST_TEMPLATE.md` and `ISSUE_TEMPLATE/`. Pin action SHAs before going public.

Two version pins have to move in lockstep or `make check` stops predicting CI: **golangci-lint** (`GOLANGCI_VERSION` in `ci.yml` ↔ `GOLANGCI_CI_VERSION` in the `Makefile`, which `make doctor` compares against your local binary) and the **golangci-lint-action major** — v8 drives golangci-lint v2, which is the schema `.golangci.yml` is written in. Action v6 silently ignores a v2 config.

Because the tooling was written before the code (ADR-019), `ci.yml` and `codeql.yml` open with a `preflight` job that skips the backend/frontend jobs until `go.mod` and `web/package.json` actually exist. This keeps `main` green through the pre-scaffold window — "never tag a red `main`" only works if red means something — and goes permanently inert once M1 lands.

## ADR-018 — Release & versioning: tag-driven SemVer, operator upgrade contract

**Context.** Uruni is self-hostable, so the version string is a **contract for the operator's `docker compose pull && up`**, not a marketing brand. (Learned wholesale from Balances ADR-0029/0033.)

**Decision.**
- **GitHub Flow:** short-lived branch → PR → **squash-merge** (one issue = one commit on `main`). The human merge is the sign-off; reviews advisory.
- **Batched, tag-driven SemVer pre-releases.** Several PRs land, then one `vX.Y.Z-alpha.N` tag cuts a release. **Milestone = minor** by convention (see `ROADMAP.md`); the version is the public contract.
- **Release notes auto-generated from PR labels** via `.github/release.yml` (`enhancement`→Added, `bug`→Fixed, `documentation`→Docs, `dependencies`→Deps). Label at merge time. Write a short **non-technical digest** (Added / Fixed / Behind the scenes) over the auto changelog — the treasurer audience matters.
- **Operator upgrade contract** (what a version *costs* the self-hoster): patch = drop-in; minor = additive migration, drop-in; major = breaking but data survives, "read the notes"; new repo = data can't forward-migrate.
- **Migrations:** goose, embedded; **renumber at merge** (filename prefix, not timestamps) so apply-order == merge-order; squashing allowed only in resettable envs; **immutability begins at the first production deploy.**
- **`0.x` is honestly unstable** — breaking changes ride *minor* bumps through the alpha ramp. First production tag turns on major-vs-minor discipline.
- **Self-host tag discipline:** a pinned `URUNI_TAG` in `.env.example` / `SELF-HOSTING.md` is bumped to the new release **before** tagging (Balances' recurring trap).

**Consequences.** Issues + PRs + GitHub Releases are the system of record for what changed. No hand-maintained CHANGELOG file.

## ADR-019 — CLI surface & runtime config (the scaffold's contract)

**Context.** The `Makefile`, `ci.yml`, and the repo's hooks were written *before* the scaffold and already invoke a binary that doesn't exist yet. That inversion is deliberate — the tooling is easier to reason about than the code it will drive — but it only works if the surface it assumes is pinned down. Otherwise the scaffold invents subcommand names ad hoc and the tooling silently rots on day one.

**Decision.** One binary, `uruni`, built from `./cmd/uruni`, with these subcommands and nothing else:

| Subcommand | Purpose |
|---|---|
| `serve` | Run the HTTP server — JSON API, SSR public report, embedded SPA. Applies pending migrations on boot. |
| `migrate up` / `down` / `status` | goose, embedded. `down` rolls back one step. |
| `create-user <email> <password>` | Create or reset a local account (argon2id). The only way to mint a login. |
| `seed-e2e` | Reset + migrate + seed the Playwright fixture database. Dev-only; refuses to run against a non-throwaway DB. |
| `version` | Print the version/commit — the operator's half of the upgrade contract (ADR-018). |
| `healthcheck` | Probe `/healthz` on the local `PORT`; exit 0 when healthy. Exists **only** because the runtime image is distroless — no shell, no curl — so a container `HEALTHCHECK` has nothing else to call. Added 2026-08-09. |

Runtime config is environment variables only (no config file):

| Variable | Default | Meaning |
|---|---|---|
| `URUNI_DB` | `./uruni.db` | SQLite file path. |
| `DATABASE_URL` | — | Postgres DSN. When set, **takes precedence** over `URUNI_DB` (ADR-004). |
| `PORT` | `8080` | Listen port. |
| `URUNI_BASE_URL` | — | Public origin, used to build the shareable report link. |
| `URUNI_SESSION_SECRET` | — | Session signing key. Server refuses to start on the placeholder value. |
| `SMTP_URL` | — | Optional, for emailed backups (ADR-012). |

`GET /healthz` is unauthenticated, returns 200 when the server is up, and is what the dev-server readiness poll and the container healthcheck use.

The compose stack does not restate the healthcheck — the image carries its own `HEALTHCHECK`, and `caddy` waits on `condition: service_healthy`. The image must also ship `/data`, `/uploads` and `/backups` **owned by uid 65532**: Docker seeds a fresh named volume from the image's directory at that path, so if the path is absent the volume lands `root:root` and the nonroot binary can neither open SQLite for writing nor store a receipt photo.

**Consequences.** The Makefile is the executable form of this table, so a rename is a three-file change — binary, Makefile, this ADR — landing in one PR. `seed-e2e` guarding its own blast radius matters because the e2e target deletes a database file. Auto-migrate-on-`serve` keeps self-hosting to `docker compose up` with no migration step for the operator, at the cost of a slower first boot after an upgrade.

## ADR-020 — Dev environment: one entry point, guards committed

**Context.** A solo maintainer working with AI agents on a public AGPL repo that is developed against a *real* community's fund. Two failure modes matter more than convenience: real neighbours' names leaking into a public repo, and setup steps that exist only in someone's shell history. Balances has good guards but keeps its entire agent configuration in a gitignored `.claude/settings.local.json` full of absolute paths — nothing about it survives a clone.

**Decision.**

- **`make setup` is the only entry point**, idempotent, safe to re-run after any pull. It arms git hooks, arms the Claude Code hooks, installs web deps, and seeds `.env` with a generated session secret.
- **Commit guard:** `.githooks/pre-commit` (via `core.hooksPath`) blocks staged additions matching `.pii-patterns` — a local, gitignored denylist, because the real terms *are* the PII. It reports offending filenames only, never the matched content, so a blocked commit can't echo PII into a terminal or an agent's context. `--no-verify` is unsupported as policy.
- **Agent config is committed and portable:** behaviour lives in `.claude/settings.json` with scripts in `.claude/hooks/`, addressed via `${CLAUDE_PROJECT_DIR}` — never an absolute path. Only personal approvals and commit attribution stay in the gitignored `.claude/settings.local.json`, seeded from a committed `.example`. Three hooks: orient on session start (fast-forward `main` **only** when already on `main`, so a session never yanks you off a feature branch), refuse `git push` when `make check` fails, and format on write.
- **`make check` mirrors `ci.yml` step for step.** When either changes, both change in the same PR. This is the whole point of it.
- **`make doctor`** reports on what a Makefile provably cannot do — install or authenticate Claude Code, grant it permissions, install the toolchain — rather than pretending to fix it.

**Consequences.** `jq` becomes a dev dependency (the hooks parse tool JSON with it; they no-op without it and `doctor` says so). Allow-rules in a committed `settings.json` need Claude Code's one-time workspace-trust prompt in a fresh clone. Because `.claude/` and `.githooks/` ship publicly under AGPL, they must stay free of real names — which is precisely why the denylist that protects them is the one file kept local.

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

Error monitoring/observability, rate-limit specifics, a multi-environment (preview/demo/production) split, and any nightly migration-rehearsal CI — all deferred until there's real production data or a second deploy target to justify them. (CI/CD and release/versioning are now decided — ADR-017/018.)
