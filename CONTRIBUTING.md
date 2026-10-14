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

Prerequisites: Go (see `go.mod`), Node 24 (`.nvmrc`), `jq`. Docker is only needed to exercise the self-host stack — the dev loop uses SQLite and runs without containers.

```sh
make setup     # hooks + Claude Code hooks + web deps + .env (idempotent)
make doctor    # what's installed, what's missing
make run       # API on :8080; applies pending migrations on boot
make web-dev   # Vite on :5173, proxies /api and /report -> :8080
```

`make` with no target lists everything. `make setup` also arms the **pre-commit PII guard** — see "Scrub the diff" below; it's the mechanism behind that rule.

If you work through AI agents, the repo ships its own configuration for them: pinned models and effort levels per role in `.claude/agents/`, and a hook that asks before any subagent is spawned. The rules are in [`CLAUDE.md`](CLAUDE.md), the reasoning in [ADR-023](docs/ADR/023-agent-operating-model.md), and [`.claude/README.md`](.claude/README.md) is the operator's cheat sheet — how to override the pinned defaults, and what each choice costs. Nothing there is required to contribute by hand.

Production build embeds the SPA into the Go binary — `make build` does it in the right order.

> Build-order gotcha: the `//go:embed all:web/dist` directive fails to compile if `web/dist` doesn't exist, so the web app must be built **before** the Go build (CI and the Dockerfile already do this in order). `web/dist/.gitkeep` is committed for exactly this reason — don't delete it, or a bare `go build`/`go vet` on a fresh clone fails before you've run the frontend build.

## How a change flows (GitHub Flow)

1. **An issue first.** For anything beyond a typo, open or claim one so the shape can be discussed.
2. **Decisions get recorded.** Architectural or hard-to-reverse calls go in `docs/ADR/` as a new numbered ADR (or `docs/Decisions.md`) before coding. Numbers are permanent; an ADR tagged `draft` can be edited in place (once code exists behind it, the edit rides in the same PR as that code), its tag comes off with the last slice of the milestone that implements it, and an implemented one changes only by a superseding ADR — [`docs/ADR/README.md`](./docs/ADR/README.md) has the rule. Implementing a `draft` ADR means dropping its tag in the same PR.
3. **Branch → PR → squash-merge.** `main` is protected and always releasable; the human merge is the sign-off.
4. **Label the PR** with one type (`enhancement` / `bug` / `documentation` / `dependencies`) when you open it, and assign it to its milestone — the auto-generated release notes depend on both, and unlabeled PRs fall through them.
5. **Migrations** stay in the same PR as their feature. Through all of `0.x` there is exactly **one** file — `internal/db/migrations/00001_schema.sql`, edited in place, never a second one before `v1.0.0` ([ADR-025](./docs/ADR/025-one-migration-file-until-1.0.md)). It must always read as the complete current schema. Migrations are embedded in the binary — `serve` applies what's pending on boot, so an operator never runs a migration step.
6. **After pulling a schema change, run `make db-reset`.** goose records applied migrations by number, so it cannot tell that `00001` was edited. `serve` compares the live schema with the one the binary creates and refuses to start when they differ, naming what does; `make db-reset` is the fix. Resetting is the normal loop, not a recovery step; there is no data to lose before `v1.0.0`.
7. **Touching SQL means regenerating.** Queries live in `internal/store/queries/*.sql`; `make sqlc` regenerates `internal/store/`, and **the generated code is committed** so a reviewer sees exactly what the generator produced. sqlc is a separate install — `make doctor` says whether you have the exact version CI pins, since another version writes different code. Name every projected expression (`SELECT 1 AS ok`, not `SELECT 1`): sqlc's SQLite engine mangles unnamed ones.

## Before you open a PR

- **Tests.** Backend → `make test`; frontend → `make web-test`. The **money and reconciliation logic is the trust core — it must be covered.**
- **`make check`** mirrors CI step for step: Go lint + test, `go mod tidy -diff`, `sqlc diff` and govulncheck, then frontend lint/format/typecheck/test and the build. Green locally ≈ green in CI. govulncheck reads the vulnerability database, so it needs network access; the Go steps run on `go.mod`'s `toolchain` line, not whatever Go is on your `PATH`.
- **Dependencies change the licences we ship.** `THIRD-PARTY-NOTICES` is generated from the Go modules linked into the binary and the packages in the built web bundle (names only, no versions), and `make check` fails when it is stale. If it does, run `make notices` and commit the result.
- **Scrub the diff.** No real names, real figures, or absolute local paths — this is a public repo. Use neutral fixtures and toy amounts. The pre-commit guard blocks additions matching your local `.pii-patterns`; add your own real-world terms to it (the file is gitignored — the terms themselves are the PII). Don't reach for `--no-verify`.
- **Copy is Bahasa Indonesia**, sentence case, warm (see `docs/Design-System.md`).

## Forks that distribute a modified build

Uruni is AGPL-3.0, and section 13 says: if you modify it and let people use it over a network, you must offer those users the source of *your* version. The sign-in screen's "Kode sumber" link is how Uruni does that, and it points at upstream by default. **A fork that distributes a modified build must point it at its own source**: set `URUNI_SOURCE_URL` to your repository when you build the image.

```sh
docker build --build-arg URUNI_SOURCE_URL=https://github.com/you/uruni -t uruni .
```

The value becomes `VITE_SOURCE_URL` for the web build (`web/src/lib/project.ts`). Keep `LICENSE`, `NOTICE` and `THIRD-PARTY-NOTICES` in the image (the Dockerfile already copies them), and run `make notices` if your fork changes a dependency.

## Reporting bugs / requesting features

Open a GitHub issue. Remember the scope bar above — a polite "no, that belongs in another product" is a normal and expected answer here.
