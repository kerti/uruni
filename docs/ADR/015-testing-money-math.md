# ADR-015 — Testing: prioritize the money math

**Status:** Accepted · `draft` — **partly** implemented, and the tag stays until the rest is: `go test`, Vitest and Playwright are all real, but the one flow the Playwright leg still owes, *the public report renders*, has no page to drive until M7 (see below) · [ADR index](./README.md)

**Decision.** **`go test`** for the backend, with the **ledger/reconciliation logic as the highest-priority target**; **Vitest** for client units; **Playwright** for a few end-to-end flows (record → balance → reconcile; public report renders). The money package and reconciliation are must-have coverage.

**Consequences.** A small but non-negotiable suite around PRD 7.8.

`internal/money` and `internal/ledger` carry a numeric coverage bar — **>= 90% and >= 85%** — reviewed at the PR that lands each slice rather than gated in CI; the harness and fixture mechanics underneath this ADR's highest-priority line are [ADR-028](./028-testing-the-trust-core.md). Both bars were met by every M3 slice (added 2026-08-13, with M3's close).

**Why this ADR keeps its `draft` tag while ADR-028 loses one.** All three legs above now exist: `go test` covers the backend with the ledger as its priority, Vitest covers client units since M1, and Playwright arrived with M6.3 (`web/playwright.config.ts`, `make e2e` over a seeded database from `uruni seed-e2e`). Its first spec, `golden-path.spec.ts`, walks record -> balance -> reconcile - the first of the two flows this ADR names - and further specs cover dues, members, Talangan, amplop, settings, receipts and restore. The second named flow, *the public report renders*, cannot be written before the report exists, which is M7's. The tag comes off when a decision is *fully* implemented ([the index](./README.md) has the rule), so it comes off with that spec.
