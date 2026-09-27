# ADR-013 — Scheduling: in-process

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** An **in-process scheduler** in the Go app: a stdlib `time.Ticker` inside `serve`. No Redis, no separate worker, and no **robfig/cron** - the only scheduled job is ADR-012's daily dump, and one fixed cadence needs neither cron syntax nor a dependency. *(Narrowed by grill A, 2026-09-28.)*

**Consequences.** Fewer moving parts; scheduled work pauses if the app is down (acceptable — restart resumes).
