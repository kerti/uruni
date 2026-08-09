# ADR-013 — Scheduling: in-process

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** An **in-process scheduler** in the Go app (**robfig/cron** or a `time.Ticker`). No Redis, no separate worker.

**Consequences.** Fewer moving parts; scheduled work pauses if the app is down (acceptable — restart resumes).
