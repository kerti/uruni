# ADR-006 — Money is integer minor units (never floats)

**Status:** Accepted · [ADR index](./README.md)

**Decision.** Store and compute all amounts as **`int64`** integer rupiah (IDR is effectively whole-rupiah; if sub-rupiah ever matters, migrate the whole system to integer sen). No float arithmetic beyond display.

**Consequences.** A small money package in Go (add/subtract/parse) and formatting on the client via `Intl.NumberFormat('id-ID')`. Balances are derived by summing the integer ledger; reconciliation compares integers exactly.
