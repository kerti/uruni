# ADR-018 — Release & versioning: tag-driven SemVer, operator upgrade contract

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** Uruni is self-hostable, so the version string is a **contract for the operator's `docker compose pull && up`**, not a marketing brand. (Learned wholesale from Balances ADR-0029/0033.)

**Decision.**
- **GitHub Flow:** short-lived branch → PR → **squash-merge** (one issue = one commit on `main`). The human merge is the sign-off; reviews advisory.
- **Batched, tag-driven SemVer pre-releases.** Several PRs land, then one `vX.Y.Z-alpha.N` tag cuts a release. **Milestone = minor** by convention (see [`ROADMAP.md`](../ROADMAP.md)); the version is the public contract. **A milestone closes on a final, not another alpha** — the batch that meets the definition of done is tagged `vX.Y.0`, so every minor actually ships and the pilot has a non-prerelease `URUNI_TAG` to pin.
- **Release notes auto-generated from PR labels** via `.github/release.yml` (`enhancement`→Added, `bug`→Fixed, `documentation`→Docs, `dependencies`→Deps). Label at merge time. Write a short **non-technical digest** (Added / Fixed / Behind the scenes) over the auto changelog — the treasurer audience matters.
- **Operator upgrade contract** (what a version *costs* the self-hoster): patch = drop-in; minor = additive migration, drop-in; major = breaking but data survives, "read the notes"; new repo = data can't forward-migrate.
- ~~**Migrations:** goose, embedded; **renumber at merge** (filename prefix, not timestamps) so apply-order == merge-order; squashing allowed only in resettable envs.~~ **Superseded by [ADR-025](./025-one-migration-file-until-1.0.md)**: one migration file, edited in place, through the whole `0.x` ramp — nothing to renumber. **Immutability still begins at the first production deploy**, which is where 025 hands the file back.
- **`0.x` is honestly unstable** — breaking changes ride *minor* bumps through the alpha ramp. First production tag turns on major-vs-minor discipline.
- **Self-host tag discipline:** a pinned `URUNI_TAG` in `.env.example` / `SELF-HOSTING.md` is bumped to the new release **before** tagging (Balances' recurring trap).

**Consequences.** Issues + PRs + GitHub Releases are the system of record for what changed. No hand-maintained CHANGELOG file.
