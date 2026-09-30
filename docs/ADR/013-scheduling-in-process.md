# ADR-013 — Scheduling: in-process

**Status:** Accepted · implemented at M6 ([#324](https://github.com/kerti/uruni/issues/324): `backup.RunScheduler`, an hourly `time.Ticker` in `serve` that writes the day's dump when one is due) — change only by adding a superseding ADR · [ADR index](./README.md)

**Decision.** An **in-process scheduler** in the Go app: a stdlib `time.Ticker` inside `serve`. No Redis, no separate worker, and no **robfig/cron** - the only scheduled job is ADR-012's daily dump, and one fixed cadence needs neither cron syntax nor a dependency. *(Narrowed by grill A, 2026-09-28.)*

**Consequences.** Fewer moving parts; scheduled work pauses if the app is down (acceptable — restart resumes).
