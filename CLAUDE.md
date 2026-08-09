# CLAUDE.md — Uruni

Guidance for Claude Code working in this repo. Read this first, every session. When in doubt, the `/docs` are the source of truth; follow them over assumptions.

## What Uruni is

A small, calm app that helps a reluctant, non-accountant **treasurer** keep a community's shared fund honest — record transactions from a phone and always know the recorded balance matches the real money. It is **not** an RT operating system, an accounting package, or a payments platform.

**Prime directive: Uruni stays small.** If a change adds a screen, a setting, a concept, or a dependency that isn't required by the PRD, stop and flag it rather than building it. Scope discipline is the most important rule here.

## Source-of-truth docs (`/docs`)

- `PRD.md` — what to build and why (the requirements). Also `PRD-ID.md` (Indonesian).
- `Tech-Design.md` — the stack and architecture (ADRs).
- `Design-System.md` — colors, type, components, voice.
- `Positioning.md` — the product thesis and emotional core.
- `ROADMAP.md` — milestones (= minors), the release cadence, and the one-line status cursor.
- `Decisions.md` — the running decision log. **When a real decision is made or changed, update this.**

**Where we are (live state):** there is deliberately **no `HANDOFF.md`**. The cursor is the current **GitHub milestone + its open issues** (`gh issue list --milestone <current>`); standing decisions live in `Decisions.md`; the sequence and a one-line status live in `ROADMAP.md`. A doc may point to the GitHub board, never duplicate it.

## Stack

- **Backend:** Go — serves the JSON API, server-renders the public report (`html/template`), and serves the React SPA. Single origin. In production the built SPA is embedded via `embed.FS` into one binary.
- **Frontend:** React + Vite, as a connection-required PWA. Tailwind + shadcn/ui + lucide-react.
- **Data:** sqlc over SQLite by default (Postgres/Neon optional via config). Migrations via goose.
- **Auth:** local email/password (argon2id) + secure cookie sessions.
- **License:** AGPL-3.0.

## Non-negotiable engineering rules

1. **Money is `int64` integer rupiah. Never floats.** Do all arithmetic in integers; format only at the display edge with `Intl.NumberFormat('id-ID', {style:'currency', currency:'IDR', minimumFractionDigits:0})`. Use tabular figures for amounts.
2. **Balances are derived by summing the ledger.** Reconciliation compares integers exactly. This logic is the trust core — it must be covered by tests.
3. **Transactions are immutable.** Corrections are new adjusting entries, never edits/deletes of posted rows.
4. **Offline = unavailable.** No local data store, no write queue, no offline sync. When disconnected the app shows a clear "butuh koneksi" state. (Deliberate — do not add offline-first.)
5. **Auth:** local only; no central accounts. The public report route is **unauthenticated by design**. Login guards the treasurer's writes.
6. **Data minimization:** collect only names, amounts, dates, notes. No member email/phone required.
7. **Self-host simplicity wins ties.** Fewest moving parts. Keep SQL portable (SQLite↔Postgres). Prefer stdlib/light deps over heavy frameworks.
8. **Bahasa Indonesia first.** All user-facing copy in Indonesian, sentence case, warm and human (see Positioning voice). Centralize strings for future i18n.
9. **Use the design tokens** from `Design-System.md` — Forest `#1F5D50` primary, Sage accent, gentle semantic states (green = reconciled "cocok", terracotta = "selisih", never alarm-red for a normal discrepancy).

## Proposed repo layout

```
/                cmd/uruni/main.go, embed.go
  internal/
    money/       int64 money package (+ tests)
    ledger/      transactions, balances, reconciliation (+ tests) — the core
    members/ dues/ incidental/
    http/        router, handlers, session auth, report SSR
    store/       sqlc-generated queries
    db/          goose migrations
  web/           React + Vite app (embedded into the binary at build)
  docker/        Dockerfile, docker-compose.yml, Caddyfile
  docs/          PRD, Tech-Design, Design-System, Positioning, Decisions
```

## Dev workflow

**The `Makefile` is the entry point — run `make` to list every target.** It was written before the scaffold and is the *contract* the scaffold implements (Tech-Design ADR-019/020), not a convenience wrapper. If a target's command doesn't exist yet, build it to match; don't rename the target.

- First run: `make setup` (hooks + Claude Code hooks + web deps + `.env`), `make doctor` to see what's missing.
- Run: `make run` (API on `:8080`) · `make web-dev` (Vite on `:5173`, proxying `/api` and `/report`).
- Background servers for agent work: `make restart` / `make servers-status` (logs in `/tmp/uruni-*.log`).
- Build: `make build` — builds `web/dist` **then** the Go binary that embeds it. Never bare `go build` for a shippable artifact.
- Tests: `make test` · `make web-test` · `make e2e`.
- **Before pushing: `make check`** — mirrors `ci.yml` step for step. A Claude Code hook blocks `git push` when it fails.

Two guards are armed by `make setup` and are not optional: a **pre-commit PII guard** (`.pii-patterns`, local) and the committed **Claude Code hooks** in `.claude/`. Never bypass with `--no-verify`.

## Build order — supervised vertical slices

Build one slice at a time and **stop for review** before the next. Do not run ahead to full app construction.

1. **Scaffold** — Go module, Vite/React, Tailwind + shadcn, embed pipeline, Docker skeleton, `/docs` in place.
2. **Data model + migrations** — entities from PRD §6 (Fund, Account/Location, Purpose tag, Member, Dues rate, Transaction, Incidental, Reconciliation snapshot).
3. **money + ledger/reconciliation + tests** — the crux. Review this hardest; high test coverage required.
4. **Core API** — transactions, dues, incidental, pass-through, reconcile, balances.
5. **Auth** — local sessions.
6. **PWA UI** — the everyday loop: record → home (balance hero + reconciliation status) → reconcile flow.
7. **Public report** — SSR, filters (month/purpose/member/in-out/dues), stable unguessable slug, `noindex`, optional regenerate.
8. **Backup/export** — JSON (canonical + import), Excel, scheduled dumps, optional SMTP email.
9. **Deploy** — Dockerfile, compose, Caddy, and a short self-host README.

## Definition of done (per slice)

Builds cleanly · tests pass (money/reconciliation especially) · matches the PRD and design tokens · Indonesian, sentence-case copy · no scope creep · `Decisions.md` updated if anything was actually decided.

## Release & versioning (see `ROADMAP.md`, Tech-Design ADR-017/018)

GitHub Flow: branch → PR → **squash-merge** (one issue = one commit); `main` always releasable; the human merge is the sign-off. **Label every PR** at merge (`enhancement`/`bug`/`documentation`/`dependencies`) — auto-notes depend on it. Releases are **batched, tag-driven** SemVer pre-releases (push `vX.Y.Z-alpha.N`). **Renumber goose migrations at merge** (filename prefix, not timestamps). **Bump the pinned `URUNI_TAG` before tagging.** Never tag a red `main`. `0.x` breaking changes ride minor bumps.

## When asked for something out of scope

Point to the non-goals in `PRD.md §4` and decline politely, e.g. *"There are excellent products for that — Uruni stays focused on the shared fund."* Reminders, member logins, QRIS/bank sync, analytics dashboards, accounting journals, and RT-OS features are explicitly out.
