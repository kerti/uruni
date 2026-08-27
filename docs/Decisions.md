# Uruni — Decisions Log

*A running record of what we've decided and why. Anything here can still change.*

Last updated: 2026-08-13 (M4 planning — the one-fund rule and receipt-photo ownership)

## What belongs in this file

This is **not** a journal of every choice made in every PR — that way the file grows without bound and nobody reads it. Three homes, and only one of them is here:

- **Architectural or cross-cutting** → an **[ADR](./ADR/README.md)**, numbered and permanent.
- **The reasoning behind one slice's implementation choices** → **the PR body**, which is durable, searchable (`gh pr view <n>`), and attached to the diff it explains. It does not get copied here.
- **Standing decisions that outlive their PR and that no ADR owns** → **here**, a few lines each, linking to the PR or issue rather than restating it.

Same rule as the board: a doc may *point at* GitHub, never duplicate it.

## Related documents

- [`Positioning.md`](./Positioning.md) — the full product thesis and one-page positioning.
- [`../README.md`](../README.md) — public/contributor-facing framing, principles, non-goals.
- Treasurer interviews (spoken guide + completed interview #1) and the original Product Brief are kept in the **private working vault**, not this repo — the interview contains a real person's answers and must not go in a public, AGPL repo.

---

## Interview #1 findings (2026-08-01)

Respondent: a treasurer of ~2–3 months, looking after a work-unit (office room) fund. Small group: 8 people (was 10, may fall to 6). ~Rp 1–2 million/month. Records in a shared **Google Sheets** everyone can see; laptop-based today but *wants* mobile.

**What overturned our assumptions:**

- **The #1 pain is reconciliation, NOT chasing.** Her most-hated task is making the recorded balance match the real money. Money lives in two places — cash wallet + a **personal bank account** where kas is mixed with her salary/personal spending — and the main kas is mixed with incidental funds. People take cash to shop and forget to record it. Leftover incidental money is confusing to fold back in. This is the real job to be done.
- **Chasing late payers is NOT a pain.** She explicitly says nagging is fine ("enak-enak aja"); small group, everyone pays right after payday, in-person nudge works. → **Drop reminders/WhatsApp-chasing from the core.** (My earlier "chasing is the killer wedge" hypothesis was wrong for this persona.)
- **Transparency is already solved and low-priority for her — but must still exist.** Nobody asks "where's the money" because everyone sees the Sheet. She's never feared suspicion. But she names a **moral burden** ("beban moral") of holding shared money, and insists transparency stay a feature out of responsibility. → Transparency is *table stakes / hygiene*, not the emotional hook.
- **The emotional core was slightly wrong.** Not "fear of being suspected." Her peace of mind comes from **"when the recorded amount matches the actual money."** Reframe the core feeling around *reconciled = calm*, and her wish "tidak usah berpikir" (not having to think).

**What it confirmed:**

- Reluctant, accidental treasurer — exactly the target. ✔
- Small groups first. ✔
- Mobile-first is right *and* is the wedge vs Google Sheets: "memudahkan pencatatan transaksi di manapun" — quick capture from the phone, anywhere. ✔
- Both fund types (routine kas + incidental) are real. ✔
- No privacy concern from her at all (Q32). Handoff = "just hand over the Sheet and the money." Backup = "safe, it's all in Google Sheets."

**New concrete requirements surfaced:**

- **Per-member tiered dues** by rank (pelaksana 50k / fungsional pertama 70k / muda 80k / madya TBD). Amounts vary and change.
- **Two money locations** (cash + bank) that must be tracked and reconciled; real-world problem is kas sitting in a *personal* account mixed with private money.
- **Separate-yet-pooled purposes**: kas utama vs kas insidentil are physically merged, which *causes* the confusion. App should let her tag money to a purpose while it lives in one balance, and cleanly roll incidental leftovers into the main kas.
- **Pass-through**: some collected money is forwarded up to the "kas bidang" (parent org). Model needs "collect → forward."
- **Reimbursements are frequent and receipt-light.** People front small amounts (parkir 2rb, tempe 3rb) and claim back, often with no receipt. → Fast reimbursement entry; **receipts optional, never required.**
- The bar to switch off Google Sheets: it must be **simpler**, not more complex ("kalau malah bikin tambah rumit" = the dealbreaker).

---

## Decisions (updated)

1. **Purpose** — a small tool for the accidental treasurer of a community's shared fund. Not an RT operating system. *(confirmed)*
2. **Primary job to be done** — *keep the recorded balance matching the real money, with the least possible thinking.* Reconciliation + effortless capture is the core, above everything else. *(revised — was "transparency/anti-suspicion")*
3. **Emotional core** — *tenang karena catatan selalu cocok dengan uang yang ada* ("calm because the records always match the money"). Speaks to the moral burden, not a fear of suspicion. *(revised)*
4. **Monetization** — none, ever. Free forever. *(confirmed)*
5. **Data stance** — the project runs no data-harvesting production service. *(confirmed as a principle)*
6. **Architecture — DECIDED (2026-08-01): client-server, self-hostable only.** A mobile app talking to a community's **own always-on Uruni server** (single source of truth in the cloud). Chosen after interview #2 clarified she distrusts sync/offline ("which copy is valid?"), wants a server so a lost/broken phone loses nothing, and — crucially — **no longer needs Google Sheets** (she doesn't use it for herself and members never read it). This kills the Sheets-as-database and all local-first/offline options. Uruni the project **runs no production service and holds no data**; communities self-host. Reliability now depends on the host, so ship easy deploy (Docker/one-click) + backup guidance. *(was open → resolved)*
7. **Visibility** — treasurer's tool; the shared ledger drops to an **optional, on-demand export** (members never read it; kept only for moral-duty transparency). *(refined — demoted)*
8. **Fund model** — unified balance with **taggable purposes** (kas utama, kas insidentil, pass-through to kas bidang) and a clean way to roll incidental leftovers into the main kas. **Per-member tiered dues.** *(expanded)*
9. **Receipts** — optional, never required. Fast reimbursement flow. *(new)*
10. **Scope discipline** — hard "no feature creep" rule. **Drop reminders/chasing from the core** for now (revisit only if a larger-group persona validates it). *(refined)*
11. **License direction** — **AGPL-3.0** (consistent with "Balances"). Now an even better fit: with a self-hosted server, the network clause forces anyone hosting Uruni to keep it open. ~~App-store nuance for the mobile client to resolve at ship time.~~ **Moot as of ADR-008** — the client is a PWA served from the same origin, so there is no app-store distribution and no store-policy/AGPL tension to resolve. Copyright notice lives in `NOTICE`. *(reinforced; app-store caveat retired 2026-08-09)*
12. **Design language** — keep it *simpler than a spreadsheet* and reliable enough to replace it. **Downgrade** the emotional-context UI ("2 birthdays · 1 graduation") from signature to nice-to-have; **elevate** one-tap capture + an always-honest running balance + a reconciliation check as the signature. Plus Jakarta Sans / soft palette still fine, low priority. *(revised)*
14. **Adoption model (new, accepted trade-off)** — self-hosting means the reluctant treasurer needs a technical helper to run her instance. Interview #1's user has one. Uruni targets communities with access to someone technical; it is explicitly **not** a tap-to-install consumer app. Consistent with "don't generalize too hard."
13. **Build plan** — built with Claude Code in a separate repo (folder TBD). *(unchanged)*

---

## Architecture — resolved (was the big open question)

Interview #2 settled it. She distrusts sync/offline (won't reason about "which copy is valid"), wants an always-on server so a lost phone loses nothing, and doesn't need Google Sheets at all (never uses it herself; members never read it). Decision: **client-server, self-hostable only** — see Decision 6. The principle ("hold nothing") wins over the persona's convenience, accepting the adoption trade-off (Decision 14): the treasurer needs a technical helper to host, and interview #1's user has one.

## Open questions — resolved (2026-08-08)

Adit's answers, now reflected in the PRD:

- **Deployment:** prebuilt Docker images + a `docker compose` template (add TLS/reverse-proxy, e.g. Caddy, since the public report link needs HTTPS). Hosts pull, don't compile.
- **Backup:** full-data **JSON export** (canonical, restorable via import); optional **Excel workbook** as a human-readable secondary format.
- **Auth:** **local auth** for the single treasurer in v1 — self-contained, no per-instance external IdP setup. OIDC/OAuth optional later. (Reasoning: OAuth would reintroduce per-host credential friction; the public report page is unauthenticated anyway.)
- **Client platform:** **PWA** — installable, no app store; also dissolves the app-store licensing nuance.
- **Offline:** the app is **deliberately unavailable when disconnected** — no queue, no local store, no "which copy is valid?" ambiguity. Live connection required. (Per user preference over offline capability.)
- **Dedicated (non-personal) kas account:** deferred — "not now, maybe later."
- **Persona coverage:** n=1 accepted; no further interviews required before building.

### New decision — public shareable report (amendment 2026-08-08)

A **public, unauthenticated report page** at a stable, unguessable link. Treasurer shares it once; valid for the life of the app (no rotation). Controls to pick the month and which reports to show. Safeguards: long random slug + `noindex`; treasurer chooses what's shown (aggregate by default, per-member payment status opt-in, since the page is public); optional "regenerate link" as a leak escape hatch. This partially revives transparency (previously demoted) but *without* member accounts — consistent with "no member portal."

## Resolved (2026-08-08, second pass)

- **Public report defaults:** show **everything**, with filters (month, purpose, member, in/out, dues status) so a public viewer can sift easily. Accepted trade-off: names + payment status are publicly visible.
- **Backup cadence:** manual JSON download **plus** optional host-enabled scheduled server-side dumps **plus** optional email delivery of periodic backups (needs SMTP).

No product-level open questions remain.

## Technical design (separate doc — started 2026-08-08)

Implementation choices live as ADRs in [`ADR/`](./ADR/README.md), kept out of the PRD so the product spec stays stable; [`Tech-Design.md`](./Tech-Design.md) holds the framing around them (constraints, stack at a glance, topology, what's still open) and indexes the set. **Stack confirmed 2026-08-08:** **Go** backend (single origin — serves the API, server-renders the public report, and serves the React bundle embedded via `embed.FS`) + **React** SPA (Vite, PWA) — chosen for AI-codegen accuracy and to mirror Balances · **sqlc** over **SQLite** (Postgres/Neon optional) · integer-only (`int64`) money · Go sessions + argon2id auth (OIDC later) · minimal PWA, no offline data · Caddy TLS · single Docker image + GHCR · local-volume receipt photos · JSON/Excel/scheduled/email backups · Indonesian-first i18n · `go test`/Vitest/Playwright with reconciliation math as the priority target. Dev keeps frontend/backend separate (Vite HMR + Go, proxied). Infra: product is provider-agnostic; the maintainer's own hosted instance is a separate reference deployment kept out of the repo (ADR-016).

Design system: [`Design-System.md`](./Design-System.md) created 2026-08-08 — **Tailwind + shadcn/ui + lucide** (own-your-code, CSS-var theming, training-dense), **Plus Jakarta Sans** (self-hosted), **Sage palette** (Forest `#1F5D50` primary, Sage accent, Cream bg, terracotta `#C96C4A` for "selisih", green `#2E7D5B` for reconciled "cocok"), soft rounded, mobile-first, integer money formatted `id-ID`.

Repo guardrail: [`../CLAUDE.md`](../CLAUDE.md) created 2026-08-08 — for Claude Code, encoding the prime directive (stay small), the non-negotiable engineering rules (int64 money, immutable ledger, offline-unavailable, local auth, data minimization, self-host simplicity, Indonesian-first), proposed repo layout, dev workflow, and the supervised vertical-slice build order (scaffold → data model → money/reconciliation+tests → API → auth → UI → public report → backup → deploy). Lives at the repo root.

## Release, CI & versioning (decided 2026-08-08)

GitHub public repo, AGPL-3.0, **GitHub Actions** CI, release by **pushing tags**. See Tech-Design ADR-017 (CI/CD) and ADR-018 (release & versioning), and [`ROADMAP.md`](./ROADMAP.md) for milestones. Summary: GitHub Flow + squash-merge · batched tag-driven SemVer pre-releases · auto-notes from PR labels + a non-technical digest · version = the operator upgrade contract (patch/minor/major/new-repo) · one goose migration file edited in place through `0.x` and frozen at the first production deploy ([ADR-025](./ADR/025-one-migration-file-until-1.0.md), superseding ADR-018's renumber-at-merge) · `0.x` breaking-rides-minor.

## Learnings applied from Balances (the maintainer's prior app)

Read Balances' docs (`docs/adr`, `docs/agents/{sdlc,release}.md`) 2026-08-08. What Uruni **adopts**: its release/SDLC/versioning system (above) almost wholesale; goose renumber-at-merge (since dropped — [ADR-025](./ADR/025-one-migration-file-until-1.0.md)); pinned self-host tag bumped pre-tag; non-technical release digest; secret-scanning/CodeQL/Dependabot on a public repo. What Uruni **deliberately scales down** (prime directive): one deploy target (no preview/demo/production split), no elaborate QA invariant matrix (money/reconciliation still fully tested). What Balances **validates** for Uruni: `int64` (Balances needs decimals only for FX/investments — inapplicable here) and **local-auth-first** (Balances started on Google OAuth then *added* local auth specifically for self-hosters; Uruni is self-host-only, so it skips OAuth entirely). Reference: [[balances-reference]].

## Dev tooling written before the code (decided 2026-08-09)

The `Makefile` was written **before** the scaffold, and the scaffold now follows it rather than the other way round. Rationale: tooling is easier to reason about than the code it drives, and pinning the surface first stops slice 1 from inventing subcommand names ad hoc. Captured as **ADR-019** (CLI surface: `uruni serve | migrate up|down|status | create-user | seed-e2e | version`, env-var config, `/healthz`) and **ADR-020** (one `make setup` entry point; guards committed).

Adapted from Balances, deliberately **not** copied wholesale — dropped `gen-ts-types` (ADR-002 rules out cross-boundary generation in v1), `upgrade-contract` (ADR-017 defers it), the QA invariant matrix, `licenses`, `brand`, and the dev Postgres container (SQLite needs none). Two changes were **fixes**, not preferences, and are worth carrying back upstream if Balances ever gets revisited:

1. **Agent config is committed and portable.** Balances keeps its entire Claude Code setup in a gitignored `.claude/settings.local.json` full of absolute paths, so a clone reproduces none of it. Uruni commits behaviour to `.claude/settings.json` + `.claude/hooks/*.sh` addressed via `${CLAUDE_PROJECT_DIR}`; only personal approvals stay local.
2. **Session start no longer hijacks the branch.** Balances' `SessionStart` hook runs `start-task` on any clean tree, and `start-task` ends in `git checkout main` — so opening a session on a clean feature branch silently moves you off it. Uruni's only fast-forwards when already on `main`.

The **pre-commit PII guard** carries over unchanged in spirit and matters more here: Uruni is developed against a real community's fund, and the repo is public AGPL. The denylist stays local because the terms themselves are the PII.

## Pre-commit vet of the tooling commit (2026-08-09)

The docs/tooling/CI skeleton was reviewed before its first commit. Most of it stood; these are the calls that came out of it and aren't recorded anywhere else.

1. **`README.md` was the pre-pivot draft and has been rewritten.** It still claimed *local-first, records on the treasurer's own device*, advertised reminders/payment-chasing, and said the licence was "to be finalized (MIT or Apache-2.0)" — contradicting Decision 6 (client-server, self-hostable only), Decision 10 (reminders dropped), and Decision 11 (AGPL-3.0) respectively. It was the only public-facing doc still describing the killed architecture. Rewritten against `Positioning.md` + `PRD.md`, and it now names the connection-required rule explicitly rather than leaving "local-first" to imply the opposite.
2. **CI abstains until there is code to check.** `ci.yml`/`codeql.yml` gained a `preflight` job that skips the backend/frontend jobs while `go.mod` and `web/package.json` don't exist. ADR-019 deliberately wrote the tooling first, which otherwise means `main` is red from the tooling commit until M1 lands — and "never tag a red `main`" is worthless if red is the normal state. Inert once M1 lands.
3. **`golangci-lint-action` moved v6 → v8.** `.golangci.yml` is schema v2, which only golangci-lint v2 reads; action v6 drives v1 and would have ignored the config silently. The version is now pinned in both `ci.yml` and the `Makefile`, and `make doctor` compares the local binary — the drift this catches is precisely what `make check` promises not to have.
4. **The release image was unusable as shipped.** Three separate faults: no `VERSION` build-arg, so `uruni version` would report `dev` for every tag and ADR-018's upgrade contract would be unverifiable; `/data`, `/uploads`, `/backups` absent from the image, so Docker would seed each named volume `root:root` and the nonroot binary could not open SQLite for writing; and Go pinned to 1.23 (EOL). Also now builds `linux/arm64` — cross-compiled via `$BUILDPLATFORM` + `GOARCH` rather than QEMU, which `CGO_ENABLED=0` (ADR-004's pure-Go driver) makes free.
5. **`healthcheck` added to the CLI surface** (ADR-019). Distroless has no shell and no curl, so a container `HEALTHCHECK` has nothing to call but the binary itself. Accepted as a real addition to a table that says "and nothing else" — one subcommand, no user-facing surface, and ADR-019 had already committed to `/healthz` being what "any container healthcheck" uses.
6. **`web/dist/.gitkeep` was unaddable.** `.gitignore` used the directory form `web/dist/`, which stops git descending and makes a `!` negation unreachable — while `CONTRIBUTING.md` instructs committing that exact file so `//go:embed web/dist` compiles on a fresh clone. Now `web/dist/*` + `!web/dist/.gitkeep`.
7. **The pii-guard now exempts licensing files.** `make hooks-install` seeds `.pii-patterns` with the maintainer's git identity, and AGPL's applying-notice wants that same name as the copyright holder — so the guard would have blocked the project from stating its own copyright. `LICENSE|NOTICE|COPYRIGHT|THIRD-PARTY-NOTICES|web/licenses/*` join the existing exemption, and the notice lives in a new `NOTICE` file rather than by editing the verbatim licence text.
8. **Two guards were narrower than they read.** The `PreToolUse` push gate only matched commands *starting* with `git push`, so `make check && git push` bypassed it; and `make web-stop` ran a bare `pkill -f 'npm run dev'`, which kills every other project's dev server on the machine (it also didn't match vite's real argv — npm execs the resolved script, not the `.bin/` shim). Both now anchored properly, the latter to `$(CURDIR)`.

### Third-party attribution — open, due at M1 (2026-08-09)

Uruni goes **out** under AGPL-3.0; the question is what comes **in**. Everything the stack calls for is permissive — Go stdlib (BSD-3), `modernc.org/sqlite` (BSD-3), goose (MIT), React / Vite / Tailwind / Radix / shadcn-ui / lucide (MIT) — so there is no inbound-vs-outbound licence conflict to resolve, and no copyleft dependency forcing anything. **Confirm this at M1 rather than assuming it**, since it is the moment `go.mod` and `package-lock.json` first exist.

What *is* an unmet obligation: MIT and BSD both require their copyright notice to travel with redistributions, and the release artifact is a single binary that **embeds the built SPA** — so shipping the image redistributes bundled MIT-licensed JavaScript with no notice attached. This is unresolved, not decided:

- The Balances-derived `licenses` make target was dropped as scope (see below), yet `.githooks/pre-commit` already exempts `THIRD-PARTY-NOTICES` and `web/licenses/*` from the PII guard. The guard is ready for an artifact nothing currently generates — deliberate, but the two facts read as a contradiction until this is settled.
- **Nothing is due before M9/first release**, since there is no distributed artifact until then. It does not block this commit or M1.
- Cheapest resolution consistent with staying small: a generated `THIRD-PARTY-NOTICES` (e.g. `go-licenses` + `license-checker`) produced at release time, not a hand-maintained file. Decide at M9.

Left open on purpose: **narrowing `Bash(git checkout *)`** in `.claude/settings.json`, which auto-approves `git checkout -- .` and can destroy uncommitted work with no prompt. Wants a human hand on the permissions file. *(applied by the maintainer 2026-08-09.)*

## ADRs are one file each (decided 2026-08-09)

`Tech-Design.md` had grown to 251 lines carrying twenty ADRs inline, and the ADR count only goes up from here — every slice from M2 on adds decisions. Split into [`ADR/`](./ADR/README.md), one file per decision (`NNN-slug.md`), with `Tech-Design.md` kept as the framing and index: constraints, stack at a glance, production topology, open questions, deferred.

Why keep `Tech-Design.md` rather than fold it into `ADR/README.md`: the non-ADR content needs a home, and ~10 files across docs, CI and the Makefile already reference decisions as "Tech-Design ADR-0NN" — that phrasing stays true. ADR bodies moved verbatim; the only edits were the added status line and cross-ADR references turned into links.

**Mutability rule, decided in the same PR.** The first draft of this said simply "supersede, never rewrite" — too rigid for twenty decisions that are still prose with no code behind them, and it would have forced the pending SQLite/Postgres grill to mint a superseding ADR before a single query exists. Split in two instead:

- **Numbers are permanent** — never reused, never renumbered. 73 bare "ADR-0NN" mentions across the repo depend on that.
- **Text is editable while an ADR is tagged `draft`**, meaning no code implements it. The tag comes off in the PR that implements it, and from then on the decision changes only by a superseding ADR. Migrations go the other way (ADR-025: one file edited in place through `0.x`, frozen at the first production deploy).

Enforcement is one line in `CLAUDE.md`'s definition of done: a slice drops the `draft` tag from every ADR it implements. At the split, **six were already implemented** by the tooling that shipped ahead of the code — 009 and 010 (`Caddyfile`, Dockerfile, compose), 016 (provider-agnostic packaging, no `deploy.yml`), 017 and 018 (workflows, release-notes config, branch protection), 020 (`Makefile`, committed guards) — and the other fourteen are `draft`.

## SQLite is the only engine through 0.x (decided 2026-08-09)

The first of three planned ADR grills — the cluster asking whether "keep SQL portable so Postgres is a config swap" ([ADR-004](./ADR/004-database-sqlite-only.md) + [ADR-005](./ADR/005-data-access-sqlc.md)) earns its cost. It does not. **sqlc generates per engine**: placeholders differ (`?` vs `$1`), generated types differ, so query files can't be shared — dual support means two schema dirs, two query dirs, two generated packages and a hand-written interface over them, a second data layer for the trust core. goose migrations don't port either. And `make check` would never have run Postgres, so the promise was unverified — an assertion the repo was nonetheless publishing to operators in four places (`.env.example`, `docker-compose.yml`, `SELF-HOSTING.md`, ADR-019's config table). Run pre-code, so it cost nothing to act on.

Five decisions came out of it:

1. **SQLite only through `0.x`.** `DATABASE_URL` is deleted, not deprecated. Postgres returns only via a superseding ADR with real demand behind it.
2. **Portability moves from the SQL layer to the data layer.** [ADR-012](./ADR/012-backup-and-export.md)'s versioned canonical JSON export + import *is* the engine-migration path, which promotes its import half from disaster-recovery convenience to load-bearing, with a required round-trip test. (Worth remembering when grill A comes for the backup surface — that's one mechanism in M8 that just became non-optional.) The honest cost: no exit ramp exists until M8 ships.
3. **The schema now uses SQLite properly**, not a portable subset — `STRICT` tables above all, since without them SQLite will store `"1000.50"` in an INTEGER column, which ADR-006's int64 rupiah cannot tolerate.
4. **sqlc survives, but its rationale was rebuilt.** Its stated context was dual-engine targeting; that premise died, so it now stands on money-code safety alone — explicit reviewable SQL, generated types, a fakeable `Querier`. Accepted cost: sqlc's SQLite engine is younger than its Postgres one, so a query it can't parse drops to hand-written `database/sql` rather than bending the schema.
5. **A single `*sql.DB` with `SetMaxOpenConns(1)`**, plus WAL / `busy_timeout` / `foreign_keys` / `synchronous=NORMAL`. Serializing everything makes `SQLITE_BUSY` structurally impossible — no retry logic and no intermittent failure in the ledger — at the cost of the public report queueing behind a write. Upgrade path if latency ever proves it matters: split writer(1)/reader(N) pools under WAL.

**Process note.** 004, 005, 012 and 019 are `draft`, so all four were edited in place per the mutability rule above. [ADR-016](./ADR/016-deployment-targets-reference-infra.md) is *implemented* and said the product ships "SQLite or any Postgres" — handled as a dated **erratum** striking three words rather than a superseding ADR, on the grounds that the clause was illustrative evidence for provider-agnosticism, not the decision, and the decision is unchanged. Issue #8's demo environment needs a superseding ADR of its own; it claims a number when that ADR is written, not before.

**Downstream:** issue #8 is unblocked — Fly.io with SQLite on ephemeral disk, no Neon, exactly as it proposed.

## Live state — no HANDOFF file (decided 2026-08-08)

Uruni leans on GitHub issues/PRs from the start, so there is **no standalone `HANDOFF.md`** (Balances' HANDOFF drifted to ~488 lines duplicating PRs/release notes before it was gutted — avoided here by not starting one). The four jobs it did are homed explicitly: **cursor / in-flight** → current GitHub milestone + open issues; **what changed** → issues + PRs + Releases; **standing decisions no issue/ADR owns** → this file; **you-are-here** → a single status line in `ROADMAP.md`. Rule that prevents drift: a doc may *point to* the GitHub board, never *copy* it. Build approach: seed repo with docs, then supervised **vertical slices** (data model → money/reconciliation + tests → API → auth → UI → public report → backup → deploy), reviewing between slices rather than full-auto.

## Language boundary: operator English, treasurer Indonesian (decided 2026-08-10)

The CLI, its errors and the server logs are the **operator's** surface and are **English**, like the self-hosting docs. **Indonesian is the treasurer's surface** — the SPA and the public report ([ADR-014](./ADR/014-localization-indonesian-first.md), still `draft`, now says so). Settled in review on [#14](https://github.com/kerti/uruni/pull/14) after the M1.1 scaffold shipped Indonesian CLI errors; [#11](https://github.com/kerti/uruni/issues/11)'s acceptance criteria were corrected to match.

## Dependency licences stay permissive (checked at M1.1, Go side at M1.3)

Audited on [#14](https://github.com/kerti/uruni/pull/14) and again on [#17](https://github.com/kerti/uruni/pull/17) once Go had dependencies — goose is MIT, `modernc.org/sqlite` and its support modules BSD-3, the rest of the transitive Go set MIT/BSD-3/Apache-2.0; the npm tree is MIT/ISC/Apache-2.0/BSD/0BSD/CC0/BlueOak plus OFL-1.1 (font) and MPL-2.0 (`lightningcss`, a Tailwind build-time dependency, AGPL-compatible and not linked into the binary). **Nothing copyleft-incompatible with AGPL-3.0 may land**; re-check when a dependency is added, not on a schedule.

*The scaffold's own implementation choices — oxlint, the trimmed shadcn install, dark mode off, the self-hosted font, the `all:` embed prefix — live in [#14](https://github.com/kerti/uruni/pull/14)'s body, per the rule at the top of this file.*

## The session secret is a boot gate, and errors never echo a credential (decided 2026-08-10)

Two calls made building the CLI's runtime config ([#11](https://github.com/kerti/uruni/issues/11), [ADR-019](./ADR/019-cli-surface-and-runtime-config.md)):

1. **`URUNI_SESSION_SECRET` is checked in `config.Load()`, so it gates *every* subcommand, not just `serve`.** Unset fails the same way the `.env.example` placeholder does — an absent secret is the worse of the two, and treating them differently would have meant the value that fails loudly is the one an operator at least noticed. The cost is real and accepted: a bare `go run ./cmd/uruni healthcheck` outside `make` (which exports `.env`) now needs the variable. The alternative — a second, laxer config path for the operator tools — buys a debugging convenience by adding a way to run the binary with no secret, which is the thing being prevented.
2. **A config error names the variable but never prints its value, for `URUNI_SESSION_SECRET` and `SMTP_URL`.** Both are credentials, and a boot failure is precisely the output an operator pastes into an issue. Everything else (`PORT`, the log variables) *does* echo, because seeing what was actually read is the difference between a one-minute fix and a puzzle. Same rule as [ADR-022](./ADR/022-logging-slog.md)'s "log IDs, not names or amounts", applied to errors rather than logs.

Also closed here: the **third** of the three release-image faults recorded above under CI hardening. `VERSION` was wired at that point but `COMMIT` was not, and `.dockerignore` keeps `.git` out of the build context — so Go's own VCS stamping had nothing to read and every tagged image would have reported `commit unknown`. It now has a build-arg of its own, filled from `github.sha`; a local `go build` has the mirror-image situation (no stamp, readable `.git`) and falls back to `debug.ReadBuildInfo`.

## Test data is generic English, and that is not the Bahasa rule bending (decided 2026-08-13)

`CLAUDE.md`'s "Bahasa Indonesia first" rule covers **the treasurer's surface** — the SPA and the public report. It was never about fixtures, and M3's tests had drifted into using Indonesian fund, member, account and purpose names because the domain they model is Indonesian.

Test data is now **generic English**: `Test Fund`, `Jane`, `Cash`, `Bank Account`, `Main`. Two reasons, neither of them style. Person-shaped names in a public AGPL repo are exactly what the pii-guard exists to keep out, and they arrive through the one door it cannot watch — a fixture nobody thinks of as data about anyone. And a Go identifier outlives the string it was named for: `sri := createMember(..., "Jane")` passes every test while reading worse than either name alone, which is how [#50](https://github.com/kerti/uruni/pull/50) found half its work.

The schema has used English identifiers since M2 ([ADR-024](./ADR/024-schema-conventions.md)); this is the same rule reaching the values underneath them. Indonesian stays where it belongs: [ADR-014](./ADR/014-localization-indonesian-first.md)'s UI labels and translation files.

## The one-fund rule is policy, and it needs a lock to be true (decided 2026-08-13)

M4's planning pass had to answer [#61](https://github.com/kerti/uruni/issues/61): who owns first-run setup. The slice-level reasoning lives in [#64](https://github.com/kerti/uruni/issues/64) and its PR; two things outlive it.

**"At most one fund" is an application-level refusal, deliberately not a schema constraint.** PRD §6 keeps multiple funds open at the *model* level, and a `CHECK` would need a migration to lift. The cost is that the refusal has no schema backstop, unlike `ErrOpeningBalanceExists` and `ErrReimbursementAlreadySettled` — whose Go pre-checks sit on a `UNIQUE` index that is the real guarantee. `SetMaxOpenConns(1)` protects one `*sql.DB`, not one database file, and nothing stopped a second `uruni serve` against the same one. So the guard gets a real backstop: a single-instance lock in `serve` ([#62](https://github.com/kerti/uruni/issues/62)), which also makes honest every other "`SetMaxOpenConns(1)` makes this safe" comment already in the tree.

**Receipt photos belong to M6** ([#73](https://github.com/kerti/uruni/issues/73)) — the backend and the optional-photo affordance land together, and [ADR-011](./ADR/011-receipt-photos-local-volume.md)'s `draft` tag drops there. They are explicitly **not** M4: the pre-auth stance that lets M4 ship open reasons about JSON write endpoints, and an unauthenticated multipart file upload is a different risk class. [ADR-012](./ADR/012-backup-and-export.md) already commits M8's export to covering the uploads volume, so this cannot drift further without dragging backup/export with it.

## A reimbursement claim gets an exit, and implemented ADRs get amendments (decided 2026-08-27)

Two decisions came out of [#103](https://github.com/kerti/uruni/issues/103), and only one of them is about reimbursements.

**Settlement was the only exit a claim had.** `waived_on` had been in the schema since M2 ([ADR-024](./ADR/024-schema-conventions.md)) and the ledger refused to settle a waived claim, but nothing could set it: no `UPDATE reimbursement` query existed anywhere. A claim the member forgave, or one the treasurer typed wrong, sat in "what the fund owes" forever. PRD §7.4 gains a waive and a correction, as `PATCH /api/reimbursements/{id}` and `DELETE /api/reimbursements/{id}` — **not** three verbs: waiving sets one column, so pairing it with the ordinary correction is what makes un-waiving free, and a claim someone waived by mistake would otherwise be as stuck as the one that started this.

**Both stop at settlement.** An unsettled claim is off the ledger, which is why editing it is not a hole in `CLAUDE.md` rule 3 — the schema says the same thing by giving `reimbursement` no immutability trigger. Once settled, the payout copied the claim's amount and purpose onto an immutable transaction, and a later correction would let the two disagree while both look authoritative. After that the only correction is an ordinary adjusting entry.

**Implemented ADRs can now be amended.** The rule was binary — `draft` is editable, implemented changes only by superseding — and it is built for *decisions*. #103 falsified two sentences of fact inside [ADR-027](./ADR/027-ledger-domain-boundary.md) (how many `UPDATE`s the ledger has; whether an FK violation is always a domain bug) without touching a single decision it records. A superseding ADR for that is ceremony, and a silent edit loses the fact that a reader's memory was ever right. So: correct in place, record the old wording in an `## Amendments` section with its date and issue, and never use it for a decision — the test is *would the ADR have been written differently if we had known?* The rule lives in [the ADR index](./ADR/README.md).

Note the inconsistency this leaves: `internal/http/errors.go` corrects a different ADR-027 omission in a code comment, written when editing an implemented ADR was not an option. It qualifies as an amendment under the new rule and was deliberately left alone rather than widen #103.
