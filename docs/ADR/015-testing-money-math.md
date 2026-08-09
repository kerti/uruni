# ADR-015 — Testing: prioritize the money math

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** **`go test`** for the backend, with the **ledger/reconciliation logic as the highest-priority target**; **Vitest** for client units; **Playwright** for a few end-to-end flows (record → balance → reconcile; public report renders). The money package and reconciliation are must-have coverage.

**Consequences.** A small but non-negotiable suite around PRD 7.8.
