# ADR-002 — Languages: Go (server) + TypeScript/React (client)

**Status:** Accepted · [ADR index](./README.md)

**Decision.** **Go** for the backend, **TypeScript + React** for the client. No shared type generation across the boundary in v1 (kept simple, as in Balances); if drift becomes painful, generate TS types from the API later.

**Consequences.** Two languages, but both training-dense and familiar. Discipline on the API contract (document request/response shapes).
