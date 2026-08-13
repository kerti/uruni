# ADR-015 — Testing: prioritize the money math

**Status:** Accepted · `draft` — **partly** implemented, and the tag stays until the rest is: `go test` and Vitest are real, the Playwright leg is not (see below) · [ADR index](./README.md)

**Decision.** **`go test`** for the backend, with the **ledger/reconciliation logic as the highest-priority target**; **Vitest** for client units; **Playwright** for a few end-to-end flows (record → balance → reconcile; public report renders). The money package and reconciliation are must-have coverage.

**Consequences.** A small but non-negotiable suite around PRD 7.8.

`internal/money` and `internal/ledger` carry a numeric coverage bar — **>= 90% and >= 85%** — reviewed at the PR that lands each slice rather than gated in CI; the harness and fixture mechanics underneath this ADR's highest-priority line are [ADR-028](./028-testing-the-trust-core.md). Both bars were met by every M3 slice (added 2026-08-13, with M3's close).

**Why this ADR keeps its `draft` tag while ADR-028 loses one.** Two of the three legs above exist: `go test` covers the backend with the ledger as its priority, and Vitest covers client units since M1. **Playwright does not exist** — there is no config, no spec, and `make e2e` invokes an npm script that is not defined yet. The tag comes off when a decision is *fully* implemented ([the index](./README.md) has the rule), and the end-to-end leg is M6's work, since the flows it names (record -> balance -> reconcile, the public report rendering) have no UI to drive until then.
