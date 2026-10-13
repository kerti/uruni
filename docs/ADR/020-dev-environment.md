# ADR-020 — Dev environment: one entry point, guards committed

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** A solo maintainer working with AI agents on a public AGPL repo that is developed against a *real* community's fund. Two failure modes matter more than convenience: real neighbours' names leaking into a public repo, and setup steps that exist only in someone's shell history. Balances has good guards but keeps its entire agent configuration in a gitignored `.claude/settings.local.json` full of absolute paths — nothing about it survives a clone.

**Decision.**

- **`make setup` is the only entry point**, idempotent, safe to re-run after any pull. It arms git hooks, arms the Claude Code hooks, installs web deps, and seeds `.env` from `.env.example`.
- **Commit guard:** `.githooks/pre-commit` (via `core.hooksPath`) blocks staged additions matching `.pii-patterns` — a local, gitignored denylist, because the real terms *are* the PII. It reports offending filenames only, never the matched content, so a blocked commit can't echo PII into a terminal or an agent's context. `--no-verify` is unsupported as policy.
- **Agent config is committed and portable:** behaviour lives in `.claude/settings.json` with scripts in `.claude/hooks/`, addressed via `${CLAUDE_PROJECT_DIR}` — never an absolute path. Only personal approvals and commit attribution stay in the gitignored `.claude/settings.local.json`, seeded from a committed `.example`. Four hooks: orient on session start (fast-forward `main` **only** when already on `main`, so a session never yanks you off a feature branch), refuse `git push` when `make check` fails, turn every subagent spawn into a permission prompt, and format on write.
- **`make check` mirrors `ci.yml` step for step.** When either changes, both change in the same PR. This is the whole point of it.
- **`make doctor`** reports on what a Makefile provably cannot do — install or authenticate Claude Code, grant it permissions, install the toolchain — rather than pretending to fix it.

**Consequences.** `jq` becomes a dev dependency (the hooks parse tool JSON with it; they no-op without it and `doctor` says so). Allow-rules in a committed `settings.json` need Claude Code's one-time workspace-trust prompt in a fresh clone. Because `.claude/` and `.githooks/` ship publicly under AGPL, they must stay free of real names — which is precisely why the denylist that protects them is the one file kept local.

## Amendments

An amendment corrects a statement of fact about the code that has since become false. It never changes a decision, a trade-off or an accepted cost — that is still a superseding ADR. See the [ADR index](./README.md) for the rule.

**2026-10-13 ([#509](https://github.com/kerti/uruni/issues/509))** — the first Decision bullet said `make setup` "seeds `.env` with a generated session secret."

There is no session secret to generate: [#119](https://github.com/kerti/uruni/issues/119) removed `URUNI_SESSION_SECRET` ([ADR-019](./019-cli-surface-and-runtime-config.md)'s amendments), since nothing read it. `make setup` seeds `.env` from `.env.example` and rewrites `URUNI_BASE_URL` to a loopback `http` origin, because the server refuses to start on the template's placeholder. The decision is unchanged: one idempotent entry point.

**2026-10-13 ([#509](https://github.com/kerti/uruni/issues/509))** — the agent-config bullet said "Three hooks" and listed session start, the push gate and format-on-write. [ADR-023](./023-agent-operating-model.md) added a fourth, `agent-gate.sh`, which turns every subagent spawn into a permission prompt. The decision is unchanged.
