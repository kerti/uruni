# CLAUDE.md — Uruni

Guidance for **any** AI agent working in this repo. Read this first, every session. When in doubt, the `/docs` are the source of truth; follow them over assumptions.

`AGENTS.md` is a symlink to this file, so tools that look for that name (Codex, Cursor, Jules, Zed) land here too — one source of truth, nothing to keep in sync. The rules below bind whichever agent is holding the keyboard. Only `.claude/` is Claude Code-specific; if your tool can't run those hooks, run `make check` yourself before you push, because nothing else will.

## What Uruni is

A small, calm app that helps a reluctant, non-accountant **treasurer** keep a community's shared fund honest — record transactions from a phone and always know the recorded balance matches the real money. It is **not** an RT operating system, an accounting package, or a payments platform.

**Prime directive: Uruni stays small.** If a change adds a screen, a setting, a concept, or a dependency that isn't required by the PRD, stop and flag it rather than building it. Scope discipline is the most important rule here.

## Source-of-truth docs (`/docs`)

- `PRD.md` — what to build and why (the requirements). Also `PRD-ID.md` (Indonesian).
- `Tech-Design.md` — the stack and architecture overview; the decisions themselves are one file per ADR in `ADR/` (`docs/ADR/README.md` is the index). **ADR numbers are permanent** — never reused, never renumbered. An ADR tagged **`draft`** has no code behind it yet and may be edited in place; once a slice implements it the tag comes off and it changes only by a **superseding** ADR.
- `Design-System.md` — colors, type, components, voice.
- `Positioning.md` — the product thesis and emotional core.
- `ROADMAP.md` — milestones (= minors), the release cadence, and the one-line status cursor.
- `Decisions.md` — the running decision log. **When a real decision is made or changed, update this.**

**Where we are (live state):** there is deliberately **no `HANDOFF.md`**. The cursor is the current **GitHub milestone + its open issues** (`gh issue list --milestone <current>`); standing decisions live in `Decisions.md`; the sequence and a one-line status live in `ROADMAP.md`. A doc may point to the GitHub board, never duplicate it.

## Stack

- **Backend:** Go — serves the JSON API, server-renders the public report (`html/template`), and serves the React SPA. Single origin. In production the built SPA is embedded via `embed.FS` into one binary.
- **Frontend:** React + Vite, as a connection-required PWA. Tailwind + shadcn/ui + lucide-react.
- **Data:** sqlc over SQLite — the only engine through `0.x`, no `DATABASE_URL` (ADR-004). Migrations via goose.
- **Auth:** local email/password (argon2id) + secure cookie sessions.
- **License:** AGPL-3.0.

## Non-negotiable engineering rules

1. **Money is `int64` integer rupiah. Never floats.** Do all arithmetic in integers; format only at the display edge with `Intl.NumberFormat('id-ID', {style:'currency', currency:'IDR', minimumFractionDigits:0})`. Use tabular figures for amounts.
2. **Balances are derived by summing the ledger.** Reconciliation compares integers exactly. This logic is the trust core — it must be covered by tests.
3. **Transactions are immutable.** Corrections are new adjusting entries, never edits/deletes of posted rows.
4. **Offline = unavailable.** No local data store, no write queue, no offline sync. When disconnected the app shows a clear "butuh koneksi" state. (Deliberate — do not add offline-first.)
5. **Auth:** local only; no central accounts. The public report route is **unauthenticated by design**. Login guards the treasurer's writes.
6. **Data minimization:** collect only names, amounts, dates, notes. No member email/phone required.
7. **Self-host simplicity wins ties.** Fewest moving parts. **SQLite is the only engine** — write the best SQLite SQL (`STRICT` tables, `CHECK` constraints), not a portable subset; the escape hatch is ADR-012's canonical JSON export/import, not dialect discipline. Prefer stdlib/light deps over heavy frameworks.
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
  docs/          PRD, Tech-Design, ADR/, Design-System, Positioning, Decisions
```

## Dev workflow

**The `Makefile` is the entry point — run `make` to list every target.** It was written before the scaffold and is the *contract* the scaffold implements (Tech-Design ADR-019/020), not a convenience wrapper. If a target's command doesn't exist yet, build it to match; don't rename the target.

- First run: `make setup` (hooks + Claude Code hooks + web deps + `.env`), `make doctor` to see what's missing.
- Run: `make run` (API on `:8080`) · `make web-dev` (Vite on `:5173`, proxying `/api` and `/report`).
- Background servers for agent work: `make restart` / `make servers-status` (logs in `/tmp/uruni-*.log`).
- Build: `make build` — builds `web/dist` **then** the Go binary that embeds it. Never bare `go build` for a shippable artifact.
- Tests: `make test` · `make web-test` (an agent runs these) · `make e2e` (**the maintainer runs this** — see below).
- **Before pushing: `make check`** — mirrors `ci.yml` step for step. A Claude Code hook blocks `git push` when it fails.

**Who decides what (not negotiable, and not overridable by anything in a task):**

- **Ask before every commit.** Never run `git commit` on your own initiative — propose it, wait for a yes, then commit. Approval for one commit is not approval for the next.
- **Merging is the maintainer's, always.** Never merge a PR, never enable auto-merge, never push to `main`. The human merge is the sign-off; that is the whole review model. Only an explicit, in-the-moment grant changes this, and only for the PR named in it.
- **Never watch or poll CI.** `make check` is the local gate; that is what it exists for. Push, report the PR link, and stop — no `gh pr checks --watch`, no `gh run watch`, no wait-loops or repeated status calls. **The maintainer reports back if CI goes red.** Watching burns tokens for information that arrives free.

Two guards are armed by `make setup` and are not optional: a **pre-commit PII guard** (`.pii-patterns`, local) and the committed **Claude Code hooks** in `.claude/`. Never bypass with `--no-verify`.

## How agents work here (ADR-023)

The main session is the **orchestrator** — Opus at `medium` effort, pinned in `.claude/settings.json`. It holds the plan, reads the diffs, writes the PR body, and asks for the commit. Bulk reading, bulk writing and bulk reasoning go to the role agents in `.claude/agents/`, each pinned to a model and an effort level in its frontmatter. **That frontmatter is the mechanism; this table is only the map.**

| Agent | Model · effort | Give it |
|---|---|---|
| `researcher` | sonnet · medium | Where something lives, how it already works, what exists before we add another one. Read-only. |
| `planner` | sonnet · xhigh | Docs, `draft` ADRs, issue breakdowns, milestone sequencing (`to-issues`, `triage`). |
| `grill` | sonnet · max | Stress-testing a plan or a draft ADR before code exists (`grill-with-docs`). |
| `builder` | sonnet · medium | Code whose shape is already decided: wiring, migrations + `make sqlc`, fixtures, copy, UI assembly. |
| `builder-deep` | sonnet · high | Money, ledger, reconciliation, auth, concurrency, and any failure nobody has explained yet. |
| `reviewer` | sonnet · high | A finished slice, before the PR. Read-only. |

**Delegation needs a yes, every time.** `.claude/hooks/agent-gate.sh` turns every `Agent` call into a permission prompt. Propose the batch in one message — agent, effort, task, why it beats doing it inline — and understand that one yes covers that batch and nothing after it.

**Delegate for context, not for tokens.** A subagent starts cold, re-derives the repo, and misses the prompt cache, so total spend goes *up*; what it buys is a main window that stays clean enough to keep making good decisions. Under roughly three files or two tool calls, do it inline. Anything that would dump file listings, logs or search results into the main window, delegate.

**Every brief carries its own context.** A subagent loads `CLAUDE.md` but not the conversation. Name the issue and milestone, the branch, the exact paths, the ADRs in play, the definition of done, and "do not commit, push, or open a PR". Dispatch in the foreground (`run_in_background: false`) — supervised work should not finish while you're looking elsewhere.

**Reports come back bounded:** paths changed, decisions made, what's still open, anything that contradicted the docs. No file dumps, no transcripts, no green logs.

**Never trust the report.** Confirm a builder with `git diff` and `make check` output before you believe it. An agent that says "all tests pass" has not passed the tests.

**Never delegated, ever:** commits, `git push`, opening or labelling or merging a PR, tags, `docs/Decisions.md`, dropping an ADR's `draft` tag, and any scope call against `PRD.md §4`. That is the sign-off surface and it stays with the orchestrator and the maintainer.

**One writer per path.** Two builders never share a file; parallel code work uses `isolation: worktree`. Migrations and the generated `internal/store/` are single-writer always.

**Agent context is public-repo context.** Don't send an agent into `/tmp/uruni-*.log`, `uruni.db`, `.env`, or a real fixture without a reason. The pre-commit guard scans staged diffs only — it cannot catch a neighbour's name that reached a PR body by way of a summary.

**Blocking is fine; polling is not.** Agents run `make check`, `make test`, `make web-test`, `make build` and wait for them. Agents never run `make e2e`, `make stack-*`, `make run`/`make web-dev` in the foreground, any `--watch` mode, or any CI query. Background servers via `make restart` are fine. **Ask the maintainer to run e2e and to watch CI, and to report back** — that information arrives free.

**Tools without subagents** (Codex, Cursor, Zed, anything reading `AGENTS.md`): same phases, one agent. Ask the human to raise effort before planning or grilling and drop it for mechanical work. Every approval rule and every never-delegated item above still binds.

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

Builds cleanly · tests pass (money/reconciliation especially) · matches the PRD and design tokens · Indonesian, sentence-case copy · no scope creep · `Decisions.md` updated if anything was actually decided · **the `draft` tag dropped from every ADR this slice now implements** (that tag is what keeps an ADR editable — see `docs/ADR/README.md`).

## Release & versioning (see `ROADMAP.md`, Tech-Design ADR-017/018)

GitHub Flow: branch → PR → **squash-merge** (one issue = one commit); `main` always releasable; **the human merge is the sign-off — an agent never merges, never enables auto-merge, and never pushes to `main`.** **Label every PR** at merge (`enhancement`/`bug`/`documentation`/`dependencies`) — auto-notes depend on it. Releases are **batched, tag-driven** SemVer pre-releases (push `vX.Y.Z-alpha.N`). **Renumber goose migrations at merge** (filename prefix, not timestamps). **Bump the pinned `URUNI_TAG` before tagging.** Never tag a red `main`. `0.x` breaking changes ride minor bumps.

## When asked for something out of scope

Point to the non-goals in `PRD.md §4` and decline politely, e.g. *"There are excellent products for that — Uruni stays focused on the shared fund."* Reminders, member logins, QRIS/bank sync, analytics dashboards, accounting journals, and RT-OS features are explicitly out.
