# ADR-015 — Testing: prioritize the money math

**Status:** Accepted · implemented at M7 ([#377](https://github.com/kerti/uruni/issues/377)) · **superseded by [ADR-039](./039-e2e-every-operation-gated-in-ci.md)**: every treasurer operation has a Playwright journey, and CI runs them, `@smoke` on every PR and the full suite nightly. The coverage bars below carry over unchanged · [ADR index](./README.md)

**Decision.** **`go test`** for the backend, with the **ledger/reconciliation logic as the highest-priority target**; **Vitest** for client units; **Playwright** for a few end-to-end flows (record → balance → reconcile; public report renders). The money package and reconciliation are must-have coverage.

**Consequences.** A small but non-negotiable suite around PRD 7.8.

`internal/money` and `internal/ledger` carry a numeric coverage bar — **>= 90% and >= 85%** — reviewed at the PR that lands each slice rather than gated in CI; the harness and fixture mechanics underneath this ADR's highest-priority line are [ADR-028](./028-testing-the-trust-core.md). Both bars were met by every M3 slice (added 2026-08-13, with M3's close).

**How it was built.** All three legs exist: `go test` covers the backend with the ledger as its priority, Vitest covers client units since M1, and Playwright arrived with M6.3 (`web/playwright.config.ts`, `make e2e` over a seeded database from `uruni seed-e2e`). `golden-path.spec.ts` walks record -> balance -> reconcile, the first of the two flows this ADR names; `report.spec.ts` (M7) is the second, *the public report renders*. Further specs cover dues, members, Talangan, amplop, settings, receipts and restore.
