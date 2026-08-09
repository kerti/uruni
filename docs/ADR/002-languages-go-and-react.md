# ADR-002 — Languages: Go (server) + TypeScript/React (client)

**Status:** Accepted · `draft` — no code implements this yet, so it may still be edited in place · [ADR index](./README.md)

**Decision.** **Go** for the backend, **TypeScript + React** for the client. No shared type generation across the boundary in v1 (kept simple, as in Balances); if drift becomes painful, generate TS types from the API later.

**Consequences.** Two languages, but both training-dense and familiar. Discipline on the API contract (document request/response shapes).
