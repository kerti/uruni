# ADR-014 — Localization: Indonesian-first, strings centralized

**Status:** Accepted · [ADR index](./README.md)

**Decision.** Ship **Indonesian only** for v1, but centralize copy: **react-i18next** (or a light equivalent) on the client, and centralized message strings for the Go-rendered report (`go-i18n` if needed). A second language stays additive.

**Consequences.** Slight upfront structure, no runtime cost, future-proof.
