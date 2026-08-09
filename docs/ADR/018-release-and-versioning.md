# ADR-018 — Release & versioning: tag-driven SemVer, operator upgrade contract

**Status:** Accepted · [ADR index](./README.md)

**Context.** Uruni is self-hostable, so the version string is a **contract for the operator's `docker compose pull && up`**, not a marketing brand. (Learned wholesale from Balances ADR-0029/0033.)

**Decision.**
- **GitHub Flow:** short-lived branch → PR → **squash-merge** (one issue = one commit on `main`). The human merge is the sign-off; reviews advisory.
- **Batched, tag-driven SemVer pre-releases.** Several PRs land, then one `vX.Y.Z-alpha.N` tag cuts a release. **Milestone = minor** by convention (see [`ROADMAP.md`](../ROADMAP.md)); the version is the public contract.
- **Release notes auto-generated from PR labels** via `.github/release.yml` (`enhancement`→Added, `bug`→Fixed, `documentation`→Docs, `dependencies`→Deps). Label at merge time. Write a short **non-technical digest** (Added / Fixed / Behind the scenes) over the auto changelog — the treasurer audience matters.
- **Operator upgrade contract** (what a version *costs* the self-hoster): patch = drop-in; minor = additive migration, drop-in; major = breaking but data survives, "read the notes"; new repo = data can't forward-migrate.
- **Migrations:** goose, embedded; **renumber at merge** (filename prefix, not timestamps) so apply-order == merge-order; squashing allowed only in resettable envs; **immutability begins at the first production deploy.**
- **`0.x` is honestly unstable** — breaking changes ride *minor* bumps through the alpha ramp. First production tag turns on major-vs-minor discipline.
- **Self-host tag discipline:** a pinned `URUNI_TAG` in `.env.example` / `SELF-HOSTING.md` is bumped to the new release **before** tagging (Balances' recurring trap).

**Consequences.** Issues + PRs + GitHub Releases are the system of record for what changed. No hand-maintained CHANGELOG file.
