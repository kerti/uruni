# `.claude/` — operator notes

For the maintainer, not the agents. Claude Code loads `settings.json`, `agents/*.md` and `hooks/*.sh`; it does not load this file, which is why the reference material lives here rather than in `CLAUDE.md` — that one is read into **every** subagent's context on **every** launch, so a line added there is paid on each delegation.

The rules the agents follow are in [`CLAUDE.md`](../CLAUDE.md); the reasoning is [ADR-023](../docs/ADR/023-agent-operating-model.md). This file is the cheat sheet for driving them.

## What's here

| Path | Loaded when | Purpose |
|---|---|---|
| `settings.json` | Session start | Pins the orchestrator (`opus` · `medium`), wires the hooks, allows the safe Bash verbs |
| `settings.local.json` | Session start | Yours, gitignored. Personal approvals + commit attribution. Seeded by `make claude-install` |
| `agents/*.md` | On delegation | One role per file; `model` and `effort` in the frontmatter are the only thing that actually binds those choices |
| `hooks/session-start.sh` | Session start | Orients; fast-forwards `main` only when already on `main` |
| `hooks/pre-push-gate.sh` | Before every Bash call | Denies `git push` when `make check` fails |
| `hooks/agent-gate.sh` | Before every Agent call | Turns every subagent spawn into a permission prompt |
| `hooks/format-file.sh` | After Edit/Write | Formats what was just written |

`make claude-install` re-arms the executable bit on all four hooks. Re-run it after any pull that touches `.claude/`.

## Overriding the pinned defaults

The session runs Opus at `medium` because `settings.json` says so. To go deeper for one piece of work:

| You want | Do this |
|---|---|
| Deeper reasoning, **one turn** | Put `ultrathink` anywhere in the prompt. Session effort is untouched — nothing to undo afterwards. Reach for this first. |
| Deeper reasoning, **this session** | `/effort max` — `max` is always session-only |
| A different level **going forward** | `/effort high` (or `low`/`medium`/`xhigh`) |
| Hand control back to the project default | `/effort auto` |
| A different model | `/model` — left/right arrows move the effort slider inline |
| **No delegation**, do it in the main session | Answer *no* to the agent-gate prompt, or say "do this inline". The orchestrator holds every tool the role agents have. |
| Set it at launch | `claude --effort xhigh` |

Two ways this bites:

- **`/effort low|medium|high|xhigh` persists across sessions.** Set it once and it silently outlives the task you set it for. `/effort auto` is the undo. `max` and `ultrathink` never persist, which is why they're the safer reach.
- **`CLAUDE_CODE_EFFORT_LEVEL` beats everything** — the project file, `/effort`, and `--effort` alike, silently. If effort isn't behaving, `env | grep CLAUDE_CODE_EFFORT` first.

The session header shows the active level next to the model name, and the footer flashes it at startup and on every change. That's the ground truth when the layers disagree.

## The cost model

Rates per million tokens, checked **2026-08-12**:

| | Input | Output |
|---|---|---|
| Opus 5 | $5 | $25 |
| Sonnet 5 | **$2** ($3 from 2026-09-01) | **$10** ($15 from 2026-09-01) |

Sonnet's introductory pricing runs through **2026-08-31**; after that the Opus→Sonnet saving narrows from 2.5× to 1.67×. Cache reads cost ≈0.1× the input rate; cache writes 1.25× (5-minute TTL) or 2× (1-hour).

Three things follow, in the order they affect the bill:

**1. Effort is the biggest lever, and it isn't structural.** Opus 5 defaults to `high`. Dropping the orchestrator to `medium` cuts thinking tokens billed at $25/MTok on every turn, including the trivial ones. This saves more than delegation does.

**2. Delegation is a context optimization that happens to pay off.** Total tokens go *up* — a subagent re-derives the repo cold and misses the prompt cache. What it buys is a main window that doesn't carry the exploration. A 15-file investigation (~60k tokens) inline on Opus costs ~$0.30 to read and then ~$0.60 more in cached re-reads across the next twenty turns, and eats 60k of the window; the same work in a `researcher` costs ~$0.17 and returns 800 tokens. Roughly 5×, almost all of it from what *doesn't* stay resident.

**3. It inverts below the floor.** Re-reading something the orchestrator already has cached costs $0.50/MTok on Opus (0.1 × $5) against $2/MTok cold on Sonnet — a **4× loss**. Under about three files or two tool calls, inline wins. This is why a brief should be small and self-contained rather than a context dump: the dump is the expensive part.

**The fixed tax:** `CLAUDE.md` is ~3.4k tokens and loads into every subagent, so each delegation starts ~$0.007 in the hole before doing anything. Negligible per call — but it means the length of that file is now a running cost, not a style question.
