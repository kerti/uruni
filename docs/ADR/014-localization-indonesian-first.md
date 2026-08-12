# ADR-014 — Localization: Indonesian-first, strings centralized

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** Ship **Indonesian only** for v1, but centralize copy: **react-i18next** (or a light equivalent) on the client, and centralized message strings for the Go-rendered report (`go-i18n` if needed). A second language stays additive.

**Where the boundary runs.** "Indonesian-first" is about the **treasurer's** surface — the SPA and the public report. The **operator's** surface is **English**: the CLI and its errors, server logs, `README`/`SELF-HOSTING`, and anything else a self-hoster reads at a terminal. Added 2026-08-10, after the M1.1 scaffold shipped Indonesian CLI errors on the strength of "Indonesian-first" alone.

**The code is a third surface, and it is English without exception** (added 2026-08-12, with M2's schema). Table names, column names, **enum values**, Go and TypeScript identifiers, test names and comments are English. Bahasa Indonesia appears only as a **UI label or a translation-file value** — never as something another program reads. So the routine purpose is `main` in the schema and on the wire, and *Kas Utama* on screen; `CONTEXT.md` records that mapping for every term that has one. Data a user types — a dues tier they name "pelaksana" — is data, not an identifier, and is exempt.

Without this line, "Indonesian-first" reads as licence to name an enum value `kas_utama`, which puts the presentation layer's language inside the database and inside the API. The question per string is now: treasurer, operator, or program?

**Consequences.** Slight upfront structure, no runtime cost, future-proof. Two audiences means one question per string — treasurer or operator? — and the answer decides its language.
