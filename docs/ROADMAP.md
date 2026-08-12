# Uruni — Roadmap

**Version 0.1 · 2026-08-08 · Status: draft**

How Uruni gets built and released. Milestones map 1:1 to the supervised build slices in [`../CLAUDE.md`](../CLAUDE.md); the release mechanics come from [`Tech-Design.md`](./Tech-Design.md) ADR-017/018 (adapted from the Balances project's proven scheme).

## How we version & release

- **SemVer pre-releases, tag-driven.** `main` is always releasable. Branch → PR → squash-merge; several PRs batch up, then one `vX.Y.Z-alpha.N` tag cuts a release (pushing the tag builds the image to GHCR).
- **Milestone = minor**, by convention. Within a milestone the `alpha.N` counter advances per batch; the batch that meets the milestone's definition of done is tagged `vX.Y.0` **final** rather than another alpha (`v0.3.0-alpha.1` → `v0.3.0` → `v0.4.0-alpha.1`). The `Version` column below is that final.
- **`0.x` is honestly unstable.** Breaking changes ride minor bumps during the ramp, and **the schema is one file edited in place** the whole way ([ADR-025](./ADR/025-one-migration-file-until-1.0.md)) — so upgrading a `0.x` instance can mean starting the data over. The first real deployment to a live treasurer's instance is the **production** moment — that's when that file freezes, migrations become immutable, and major-vs-minor discipline turns on (likely `v1.0.0`, but the number isn't reserved).
- **The version is the operator's upgrade contract**, not a brand: patch = drop-in, minor = additive migration drop-in, major = breaking-but-survivable (read the notes), new repo = data can't forward-migrate.

## Cadence — when we cut

Cut an **`alpha.N`** whenever a slice (or a coherent batch within one) lands green on `main` and is worth looking at. When a batch *also* meets the milestone's definition of done, tag it `vX.Y.0` final instead of another alpha — so **decide DoD before tagging the last batch**, or you pay a duplicate tag and a second multi-arch build on the same commit (`release.yml` rebuilds per tag; nothing promotes an existing image). No calendar dates yet — the rhythm is *slice → review → tag*, sized to a solo, AI-assisted, part-time pace. (We can add target dates once availability is known.)

## Milestones

| Milestone | Version | Slice | Outcome |
|---|---|---|---|
| **M1 Scaffold** | `v0.1.0` | 1 | Go module, Vite/React, Tailwind + shadcn, `embed.FS` pipeline, Docker skeleton, `/docs` in place, CI green. |
| **M2 Data model** | `v0.2.0` | 2 | Migrations for PRD §6 entities (Fund, Account/Location, Purpose tag, Member, Dues rate, Transaction, Incidental, Reconciliation snapshot). **Deletes M1.3's no-op baseline migration** — the real schema becomes `00001`. |
| **M3 Money & reconciliation** | `v0.3.0` | 3 | The `money` (int64) package + ledger/reconciliation logic, **heavily tested**. The trust core. Review hardest. |
| **M4 Core API** | `v0.4.0` | 4 | Transactions, dues, incidental, pass-through, reconcile, balances over the model. |
| **M5 Auth** | `v0.5.0` | 5 | Local email/password + sessions. |
| **M6 Everyday UI** ★ | `v0.6.0` | 6 | The loop: record → home (balance hero + reconciliation status) → reconcile. **First pilotable build** — deployable for the treasurer to actually try. |
| **M7 Public report** | `v0.7.0` | 7 | SSR report page, filters, stable unguessable slug, `noindex`, optional regenerate. |
| **M8 Backup/export** | `v0.8.0` | 8 | JSON (canonical + import), Excel, scheduled dumps, optional SMTP email. |
| **M9 Self-host & deploy** | `v0.9.0` | 9 | Dockerfile, compose, Caddy, `SELF-HOSTING.md`, pinned `URUNI_TAG`. Hardening for a real operator. |
| **Production** | `v1.0.0` | — | First real, maintained deployment for a live treasurer. Migration immutability begins; upgrade contract goes live. |

★ **M6 is the real milestone to aim for** — everything before it is scaffolding toward the moment the treasurer can record → see balance → reconcile on her phone. Ship a private pilot at `v0.6.x` before polishing M7–M9.

## Definition of done (per milestone)

Builds clean · tests pass (money/reconciliation especially) · matches PRD + design tokens · Indonesian, sentence-case copy · no scope creep · `Decisions.md` updated if anything was actually decided · a tag cut with auto-generated notes + a short non-technical digest.

## Release hygiene (learned from Balances)

- **Label each PR at merge time** (`enhancement`/`bug`/`documentation`/`dependencies`) — unlabeled PRs fall through the auto-notes.
- **One migration file until `v1.0.0`** — `00001_schema.sql`, edited in place, never a second file (ADR-025). Nothing to renumber; `make db-reset` after pulling a schema change.
- **Bump the pinned `URUNI_TAG` before tagging**, not after — the tagged tree must recommend itself.
- **Release notes are a non-technical digest** (Added / Fixed / Behind the scenes) with the raw changelog folded beneath — written for a treasurer, not a developer.
- **Never tag a red `main`.**

## Public-repo hardening

The repo is **already public**, so this stopped being a pre-flight checklist and became a status. What is still open is tracked as an **issue** — nothing below is a to-do that lives only in this file.

**Done**, and verifiable where it lives rather than listed here: branch protection, Private Vulnerability Reporting and the release labels are the repo's GitHub settings; the `NOTICE`, the pii-guard's licensing exemptions, `web/dist/.gitkeep`, the image owner, the pure-Go SQLite driver and the SHA-pinned Actions are in the tree.

**Nothing open.** Anything found later is an issue, not a line here.

**Coverage needs a token after all.** Codecov rejects tokenless uploads even from a public repo (`Token required - not valid tokenless upload`), and the action defaults to not failing the build — so uploads died silently through M1.1–M1.2 with no report, no PR comment and no hint in a green CI. `CODECOV_TOKEN` is now a repo secret and `ci.yml` passes it as an input (2026-08-10).

**Standing, not a task.** *"Scrub for real names, figures and local paths"* is never closed — the pii-guard enforces it on every commit ([ADR-020](./ADR/020-dev-environment.md)).

## Deliberately NOT doing (staying small)

Balances is a bigger app and carries machinery Uruni should *not* copy wholesale:

- **One environment**, not preview/demo/production — the maintainer's instance is the only deploy target until there's reason for more.
- **No elaborate QA invariant matrix** — but the money/reconciliation core still gets real, non-negotiable test coverage.
- **No multi-currency/decimal** — Uruni is IDR-only, so `int64` is correct and simpler (Balances needs decimals for FX/investments; that complexity doesn't apply here).
- **No OAuth/central accounts** — Balances added local auth *for* self-hosters after starting on Google OAuth; Uruni is self-host-only, so it starts and stays on local auth.

## Live state — where we are

There is **no `HANDOFF.md`** and no standalone status doc. The live board is **GitHub**; docs may point to it, never copy it. Homes for each kind of state:

- **What's in flight / the cursor** → the current **GitHub milestone** and its open issues (`gh issue list --milestone <current>`).
- **What changed** → issues + PRs + GitHub Releases.
- **What's next / the sequence** → this roadmap + open issues.
- **Standing decisions no issue or ADR owns** → [`Decisions.md`](./Decisions.md).
- **You-are-here** → the one status line below (changes ~once per milestone; never holds shipped detail).

**Status:** **M3 — money & reconciliation** next; M2's schema is complete, with every PRD §6 entity in one migration and its invariants enforced by the database.

## Open

- Add target dates once a working rhythm is known.
