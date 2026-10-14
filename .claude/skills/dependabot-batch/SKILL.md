---
name: dependabot-batch
description: Consolidate open Dependabot PRs into one PR - apply every update at its exact version on one branch, regenerate the lockfile and THIRD-PARTY-NOTICES, amend go.mod's toolchain for a golang image bump, run make check, and open one PR without Closes lines so Dependabot retires its own PRs. Use when the maintainer asks to batch, consolidate or "do the Dependabot PRs", or when a Dependabot PR is red on the notices or toolchain check.
---

# Batch the Dependabot PRs

Why this exists (#594): `make check` fails when `THIRD-PARTY-NOTICES` is stale (#521), and a golang image bump must move `go.mod`'s `toolchain` line with it (`TestDockerfileGoMatchesToolchain`). Fixing each Dependabot PR on its own branch doesn't scale: a Dependabot PR someone pushed to stops rebasing itself, and strict up-to-date protection then costs one update-branch + CI + merge cycle per PR. One consolidated PR costs one.

CLAUDE.md's authority rules bind every step: ask before committing (a "PR it" is the grant for commit + push + open), never merge, never watch CI, never close, merge or comment on a Dependabot PR.

## 1. Inventory

```sh
gh pr list --author app/dependabot --state open --json number,title,headRefName,labels,files
```

For each PR note the ecosystem (gomod, npm, github-actions, docker, docker-compose), every package with its from -> to version (a grouped PR lists several in its body), and whether it is a **security** update. Show the maintainer the list before touching anything.

A PR that is green and touches neither the notices nor the toolchain can simply be merged by the maintainer - say so and leave it out unless asked to include it. A lone security update can go through this skill as a batch of one rather than waiting for the others.

## 2. Branch

From up-to-date `main`: `git switch main && git pull --ff-only && git switch -c chore/deps-batch-<yyyy-mm-dd>`.

## 3. Apply, one ecosystem at a time

Apply each ecosystem, then run its checks before moving to the next, so a failure names one ecosystem. Use the exact versions Dependabot proposed - never "latest".

- **gomod:** `go get <module>@<version>` per module, then `go mod tidy`. Check: `go build ./... && go test ./...`.
- **npm (`web/`):** in `web/`, run `npm install <pkg>@<version>` (`npm install -D` for a devDependency), keeping the range style `package.json` already uses for that package (`^` or exact). **npm regenerates `package-lock.json`; never hand-merge or cherry-pick a lockfile.** Check: `make web-test` and `(cd web && npm run build)`.
- **github-actions:** edit the `uses:` lines to the new full commit SHA with its `# vX.Y.Z` comment, exactly as Dependabot's PR diff has them (`gh pr diff <n>`). Keep every action SHA-pinned. If `actionlint` is installed, run it.
- **docker / docker-compose:** copy the new `tag@sha256:...` from the PR diff. For a **golang** image bump, set `go.mod`'s `toolchain` line to the same version (keep the `go` line - it moves only by deliberate review, see #578). Check: `go test -run TestDockerfileGoMatchesToolchain .`.

A Dependabot PR whose diff touches something else (a workflow input, a config file) - apply it from `gh pr diff <n>` verbatim and say what it was.

If an ecosystem's checks fail and the cause isn't obvious within that ecosystem, stop and report: which update, the failing output, and the options (drop that update from the batch, or fix it). Don't widen the batch into a refactor.

## 4. Regenerate and gate

```sh
make notices   # THIRD-PARTY-NOTICES from the built bundle and the linked Go modules
make check     # the strict gate, includes the notices diff
```

Report whether `THIRD-PARTY-NOTICES` changed and why (a package added or dropped from the bundle, a licence text changed, or a modernc bump - its own third-party file lists versions).

## 5. Commit and PR

Ask before committing unless the maintainer said "PR it". Commit message:

```
build(deps): batch Dependabot updates #<a>-#<z>
```

Body: one bullet per update - `package from -> to (#n)` - then a line for anything regenerated (lockfile, `THIRD-PARTY-NOTICES`, the toolchain line). End with the attribution lines the session's system-reminder gives.

Open one PR: label `dependencies`, the current milestone (`--milestone "<full title>"`), the same bullet list in the body, and **no `Closes #n` lines**. Dependabot sees that `main` already has each version and closes its own PRs; a `Closes` line would close them as "completed" under our name instead and hide a version Dependabot still thinks is pending. Report the PR link and stop - no CI watching.

## 6. After the maintainer merges

One call, not a loop:

```sh
gh pr list --author app/dependabot --state open --json number,title
```

Dependabot usually closes the superseded PRs within minutes of the merge, but only when it next re-checks. Report any still open with their numbers; the maintainer decides (wait, `@dependabot recreate`, or close). Never close them yourself.
