# ADR-014 — Localization: Indonesian-first, strings centralized

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** Ship **Indonesian only** for v1, but centralize copy: **react-i18next** (or a light equivalent) on the client, and centralized message strings for the Go-rendered report (`go-i18n` if needed). A second language stays additive.

**Where the boundary runs.** "Indonesian-first" is about the **treasurer's** surface — the SPA and the public report. The **operator's** surface is **English**: the CLI and its errors, server logs, `README`/`SELF-HOSTING`, and anything else a self-hoster reads at a terminal. Added 2026-08-10, after the M1.1 scaffold shipped Indonesian CLI errors on the strength of "Indonesian-first" alone.

**Consequences.** Slight upfront structure, no runtime cost, future-proof. Two audiences means one question per string — treasurer or operator? — and the answer decides its language.
