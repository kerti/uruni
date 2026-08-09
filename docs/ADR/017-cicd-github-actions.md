# ADR-017 — CI/CD: GitHub Actions

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** Public GitHub repo, AGPL-3.0, solo maintainer, release by pushing tags.

**Decision.**
- **`ci.yml`** on PRs and pushes to `main`: Go lint (golangci-lint) + `go test`, frontend lint/typecheck + Vitest, and a build check. A local **`make check`** mirrors CI so green-locally ≈ green-in-CI.
- **`workflows/release.yml`** on tag push (`v*`): build the single-origin image (multi-arch `linux/amd64,linux/arm64`, cross-compiled rather than QEMU-emulated) and publish to **GHCR** (`build-once`), so any tag's artifact can later be promoted without a rebuild. The tag is passed in as the `VERSION` build-arg — otherwise `uruni version` reports `dev` forever and the operator's upgrade contract ([ADR-018](./018-release-and-versioning.md)) is a lie. **There is no `deploy.yml`:** deploying a published image to the maintainer's instance is a manual ops step kept out of this repo ([ADR-016](./016-deployment-targets-reference-infra.md)).
- **Secret scanning** (gitleaks) and **CodeQL** on a public repo; **Dependabot** for deps.
- Branch protection on `main`: require PR + green CI, linear history (squash), admin bypass on (never lock out the solo maintainer).

**Deliberately simpler than Balances:** one deploy target (the maintainer's instance), **no** preview/demo/production environment split and **no** nightly upgrade-contract rehearsal until there's real production data to protect. Add them only when warranted.

**Consequences.** `.github/` carries `workflows/{ci,release,codeql,gitleaks}.yml`, plus `release.yml` (the release-*notes* config — note the name collision with the workflow), `dependabot.yml`, `PULL_REQUEST_TEMPLATE.md` and `ISSUE_TEMPLATE/`. Pin action SHAs before going public.

Two version pins have to move in lockstep or `make check` stops predicting CI: **golangci-lint** (`GOLANGCI_VERSION` in `ci.yml` ↔ `GOLANGCI_CI_VERSION` in the `Makefile`, which `make doctor` compares against your local binary) and the **golangci-lint-action major** — v8 drives golangci-lint v2, which is the schema `.golangci.yml` is written in. Action v6 silently ignores a v2 config.

Because the tooling was written before the code ([ADR-019](./019-cli-surface-and-runtime-config.md)), `ci.yml` and `codeql.yml` open with a `preflight` job that skips the backend/frontend jobs until `go.mod` and `web/package.json` actually exist. This keeps `main` green through the pre-scaffold window — "never tag a red `main`" only works if red means something — and goes permanently inert once M1 lands.
