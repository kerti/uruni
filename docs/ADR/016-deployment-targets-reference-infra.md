# ADR-016 — Deployment targets & reference infra

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md)

**Context.** Uruni is open-source and self-hostable; it must not be coupled to any one provider. The maintainer also needs to actually host a real community's instance.

**Decision.** The **product ships provider-agnostic** (Docker image + compose, bring-your-own-domain, SQLite on a volume). A maintainer's own hosted instance (e.g. a small VPS, or a host like Fly.io) is a **reference deployment** documented by example, not a requirement. All deployment-specific config — domains, provider projects, secrets — lives in a **private ops note / `.env` outside this repo**, never in the product spec.

**Erratum 2026-08-09.** The decision above originally read "SQLite or any Postgres", and named "Fly.io with a managed Postgres such as Neon" as the reference-deployment example. [ADR-004](./004-database-sqlite-only.md) narrowed the engine to **SQLite only through `0.x`**, so both mentions are struck as stale fact rather than by a superseding ADR: they were illustrative evidence for provider-agnosticism, not the decision. The decision itself is unchanged — and SQLite on a volume couples to no provider at all.

**Note.** If a maintainer hosts several communities' instances, they — *as operator* — hold those instances' data: an operator choice, distinct from the project itself holding nothing.
