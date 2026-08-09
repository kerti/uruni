# ADR-016 — Deployment targets & reference infra

**Status:** Accepted · [ADR index](./README.md)

**Context.** Uruni is open-source and self-hostable; it must not be coupled to any one provider. The maintainer also needs to actually host his wife's instance.

**Decision.** The **product ships provider-agnostic** (Docker image + compose, bring-your-own-domain, SQLite or any Postgres). A maintainer's own hosted instance (e.g. a small VPS, or a host like Fly.io with a managed Postgres such as Neon) is a **reference deployment** documented by example, not a requirement. All deployment-specific config — domains, provider projects, secrets — lives in a **private ops note / `.env` outside this repo**, never in the product spec.

**Note.** If a maintainer hosts several communities' instances, they — *as operator* — hold those instances' data: an operator choice, distinct from the project itself holding nothing.
