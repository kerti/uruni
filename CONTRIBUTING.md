# Contributing to Uruni

Thanks for your interest. Uruni is a **small, opinionated** project — a calm tool for community treasurers — and it intends to stay that way. A little orientation saves a lot of back-and-forth.

## Start here

- **What it is and why:** [`docs/Positioning.md`](docs/Positioning.md) and [`docs/PRD.md`](docs/PRD.md).
- **The domain language:** [`CONTEXT.md`](CONTEXT.md) — use these exact words in code and copy.
- **How it's built:** [`docs/Tech-Design.md`](docs/Tech-Design.md), with each decision as its own ADR in [`docs/ADR/`](docs/ADR/README.md).
- **Look and voice:** [`docs/Design-System.md`](docs/Design-System.md).
- **Where the project is going:** [`docs/ROADMAP.md`](docs/ROADMAP.md); decisions in [`docs/Decisions.md`](docs/Decisions.md).
- **The rules a change must respect:** [`CLAUDE.md`](CLAUDE.md).

## The prime directive

**Uruni stays small.** The most valuable contributions are polish, clarity, accessibility, translations, and bug fixes — not new capabilities. Before opening a feature PR, open an issue and check it against the non-goals in `docs/PRD.md §4`. If it adds a screen, a setting, or a concept, it probably doesn't clear the bar.

## Local setup

Prerequisites: Go (see `go.mod`), Node 22 (`.nvmrc`), `jq`. Docker is only needed to exercise the self-host stack — the dev loop uses SQLite and runs without containers.

```sh
make setup     # hooks + Claude Code hooks + web deps + .env (idempotent)
make doctor    # what's installed, what's missing
make migrate-up
make run       # API on :8080
make web-dev   # Vite on :5173, proxies /api and /report -> :8080
```

`make` with no target lists everything. `make setup` also arms the **pre-commit PII guard** — see "Scrub the diff" below; it's the mechanism behind that rule.

If you work through AI agents, the repo ships its own configuration for them: pinned models and effort levels per role in `.claude/agents/`, and a hook that asks before any subagent is spawned. The rules are in [`CLAUDE.md`](CLAUDE.md), the reasoning in [ADR-023](docs/ADR/023-agent-operating-model.md), and [`.claude/README.md`](.claude/README.md) is the operator's cheat sheet — how to override the pinned defaults, and what each choice costs. Nothing there is required to contribute by hand.

Production build embeds the SPA into the Go binary — `make build` does it in the right order.

> Build-order gotcha: the `//go:embed all:web/dist` directive fails to compile if `web/dist` doesn't exist, so the web app must be built **before** the Go build (CI and the Dockerfile already do this in order). `web/dist/.gitkeep` is committed for exactly this reason — don't delete it, or a bare `go build`/`go vet` on a fresh clone fails before you've run the frontend build.

## How a change flows (GitHub Flow)

1. **An issue first.** For anything beyond a typo, open or claim one so the shape can be discussed.
2. **Decisions get recorded.** Architectural or hard-to-reverse calls go in `docs/ADR/` as a new numbered ADR (or `docs/Decisions.md`) before coding. Numbers are permanent; an ADR tagged `draft` (no code behind it yet) can be edited in place, an implemented one changes only by a superseding ADR. Implementing a `draft` ADR means dropping its tag in the same PR.
3. **Branch → PR → squash-merge.** `main` is protected and always releasable; the human merge is the sign-off.
4. **Label the PR** with one type (`enhancement` / `bug` / `documentation` / `dependencies`) at merge — unlabeled PRs fall through the auto-generated release notes.
5. **Migrations** stay in the same PR as their feature; **renumber at merge** (filename prefix, not timestamps) so apply-order == merge-order. They live in `internal/db/migrations/` and are embedded in the binary — `serve` applies what's pending on boot, so an operator never runs a migration step.
6. **Touching SQL means regenerating.** Queries live in `internal/store/queries/*.sql`; `make sqlc` regenerates `internal/store/`, and **the generated code is committed** so a reviewer sees exactly what the generator produced. sqlc is a separate install — `make doctor` says whether you have it. Name every projected expression (`SELECT 1 AS ok`, not `SELECT 1`): sqlc's SQLite engine mangles unnamed ones.

## Before you open a PR

- **Tests.** Backend → `make test`; frontend → `make web-test`. The **money and reconciliation logic is the trust core — it must be covered.**
- **`make check`** mirrors CI step for step: Go lint + test, frontend lint/typecheck/test, build. Green locally ≈ green in CI.
- **Scrub the diff.** No real names, real figures, or absolute local paths — this is a public repo. Use neutral fixtures and toy amounts. The pre-commit guard blocks additions matching your local `.pii-patterns`; add your own real-world terms to it (the file is gitignored — the terms themselves are the PII). Don't reach for `--no-verify`.
- **Copy is Bahasa Indonesia**, sentence case, warm (see `docs/Design-System.md`).

## Reporting bugs / requesting features

Open a GitHub issue. Remember the scope bar above — a polite "no, that belongs in another product" is a normal and expected answer here.
