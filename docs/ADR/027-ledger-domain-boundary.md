# ADR-027 — The ledger domain boundary: one package, one transaction helper, sentinel errors

**Status:** Accepted · `draft` — editable in place until M3 closes; an edit made once code exists ships with that code · [ADR index](./README.md)

**Context.** M3's premise is domain services — derived balances, posting a transaction, transfer pairs, settling a reimbursement, rolling an incidental leftover, taking a reconciliation snapshot — as Go code over `internal/store`'s generated `Querier`, with no HTTP yet (M4 is a thin chi layer over whatever this ADR builds). `CLAUDE.md`'s proposed repo layout lists `internal/ledger`, `internal/members`, `internal/dues`, `internal/incidental` as sibling packages. That list was written before M2's schema existed; this ADR checks it against what the code actually needs.

## Decision

**One package: `internal/ledger`. No `internal/members`, `internal/dues`, `internal/incidental`.**

Every operation M3 owns — post a transaction, a transfer pair, settle a reimbursement, roll an incidental leftover, take a reconciliation snapshot, derive dues status — shares one primitive (insert one or more `"transaction"` rows inside one DB transaction) and one connection ([ADR-004](./004-database-sqlite-only.md)'s single `*sql.DB`). A transfer pair and the incidental leftover roll are **the same mechanism** — `reclass_purpose` vs. `between_accounts` is a parameter to one function, not a different code path (see the M3 plan). Splitting the package now would draw boundaries around table names rather than around the one real seam that exists (read vs. write, or "needs a `*sql.Tx`" vs. "doesn't"). `internal/members` is dropped entirely: member CRUD carries no derived invariant beyond what the schema already enforces, so M4 calls `store.Queries` directly for it — exactly how M2's own tests already treat members, with no domain wrapper.

`CLAUDE.md`'s proposed layout table was corrected to match in the same PR as this ADR, rather than left contradicting the decision an agent reads next.

Proposed file layout inside the package (an implementation detail, listed so a builder isn't guessing, not itself load-bearing): `ledger.go` (the type and the transaction helper), `transaction.go`, `transfer.go`, `reimbursement.go`, `reconciliation.go`, `dues.go`, `balance.go`, `errors.go`.

**Transaction ownership: the service holds both a `store.Querier` and the underlying `*sql.DB`.**

```go
type Ledger struct {
    db *sql.DB
    q  store.Querier // store.New(db) — for reads that are one SELECT
}

func (l *Ledger) withTx(ctx context.Context, fn func(store.Querier) error) error {
    tx, err := l.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback() // no-op once Commit has run
    if err := fn(store.New(tx)); err != nil {
        return err
    }
    return tx.Commit()
}
```

Read-only derived-balance methods use `l.q` directly — a single `SELECT` is already consistent, no transaction needed. **Every write goes through `withTx`, including a single-row insert**, for one uniform pattern instead of a per-operation judgment call about which write "needs" a transaction. Under [ADR-004](./004-database-sqlite-only.md)'s `SetMaxOpenConns(1)`, opening a transaction never contends with anything else the process is doing, so the uniformity is free. This keeps [ADR-005](./005-data-access-sqlc.md)'s stated benefit — a fakeable `Querier` — for the read-only methods, while being honest that the write methods need a real `*sql.Tx` and are therefore exercised against [ADR-028](./028-testing-the-trust-core.md)'s real SQLite harness, not a fake.

**Transfer pairs, atomically, one primitive for both uses.** `PostTransferBetweenAccounts` and `CloseIncidentalAndRoll` both call one unexported primitive inside `withTx`, which inserts one `transfer` row and then two `"transaction"` rows referencing it (`kind='transfer'`, `transfer_id` = that row's id), equal amount, opposite direction:

```go
type leg struct{ AccountID, PurposeID int64 }

func (l *Ledger) postTransferPair(ctx context.Context, fundID int64, kind string,
	from, to leg, amount money.Amount, occurredOn string) (store.Transfer, error)
```

**A leg does not carry its own direction, and that is the point.** The primitive assigns `out` at `from` and `in` at `to` by position. Were direction leg data, a caller could construct a both-`out` or both-`in` pair that writes two rows, satisfies every schema `CHECK` — they require only that `transfer_id` is set, never that the two legs oppose — and does **not** net to zero. That is a transfer which silently changes the fund's total, which is the single failure this whole mechanism exists to prevent. Deriving direction from position makes it unrepresentable rather than merely tested against.

The primitive validates nothing; each exported entry point checks its own argument shape first, per the rule below.

**Every exported write takes a params struct, not positional arguments.** `PostTransactionParams`, `PostTransferBetweenAccountsParams`, `PostDuesPaymentParams` and their successors: these methods carry four to eight fields, several of them `int64` ids that a positional call site can silently transpose, and a struct literal names each one at the point of the call. The two positional signatures still written below (`SettleReimbursement`, `TakeReconciliation`) predate that convention and are **specifications awaiting their slices** — [#41](https://github.com/kerti/uruni/issues/41) and [#44](https://github.com/kerti/uruni/issues/44) — not descriptions of shipped code. Each is synced to what actually shipped in the PR that implements it, which is the rule for editing a `draft` ADR that already has code behind it. The fund's total is unchanged by construction — one amount, two directions — which every caller's test asserts directly (`FundBalance` before `==` `FundBalance` after), mirroring `internal/db`'s existing `TestTransferKindRequiresATransfer`. `between_accounts`: same `purpose_id`, different `account_id`. `reclass_purpose` (the incidental leftover roll): same `account_id`, different `purpose_id` — and per the M3 plan, the *choice* of account is immaterial to correctness, since a same-account pair always nets to zero on that account regardless of which accounts the original contributions came in through. `CloseIncidentalAndRoll` therefore takes one `account_id` parameter with no derivation logic behind it.

**Closing an envelope and rolling its leftover are one call, not two.** `CloseIncidentalAndRoll(ctx, fundID, purposeID, accountID, closedOn)` posts the `reclass_purpose` pair and sets `incidental.closed_on` inside a single `withTx`; a leftover of zero — or a negative one, an envelope that disbursed more than it collected — closes it and posts nothing, returning `0` and no error, because an error inside `withTx` would roll the close back and nothing asks for an over-disbursed envelope to stay open. The one refusal is `ErrIncidentalAlreadyClosed`: a second roll would move money that already moved. PRD §7.5 describes one gesture ("on close, show the leftover and offer a one-tap roll into Kas Utama"), and splitting it across two calls means a crash between them leaves an envelope rolled but open — a state nothing in the schema forbids and nothing in the UI expects. The cost is that this package no longer *only* inserts: `incidental` is the one table M2 deliberately left without an immutability trigger, precisely because closing a collection moves no money and is a decision that gets revised, so an `UPDATE` here trips nothing and contradicts no non-negotiable.

**A reconciliation is one call, and it posts its own fix entries.** The snapshot tables are immutable and `CHECK (resolution <> 'adjusted' OR adjustment_transaction_id IS NOT NULL)` requires the fix row to exist before the line that names it — so the ordering is forced, and the only question is who inserts the fix.

```go
func (l *Ledger) TakeReconciliation(ctx context.Context, fundID int64, counts []AccountCount, fixes []Fix) (store.Reconciliation, error)
```

Inside one `withTx`, in this order: take the cutoff, read `recorded_amount` per counted account **through that cutoff**, insert each `Fix` as a `"transaction"`, then insert the `reconciliation` and its lines naming those ids. The sequence is the whole point. If the fix were posted through an ordinary `PostTransaction` call beforehand, the cutoff taken afterwards would already include it, `recorded_amount` would equal `actual_amount`, and a line labelled `entry_added` would be stored with `difference_amount = 0` — schema-legal, since the `CHECK` verifies arithmetic and not history, and a false record that no gap was ever found. Uruni exists to make that number trustworthy, so the call that writes it owns every input to it.

The caller therefore hands over raw fix data (account, purpose, direction, amount, date, note), never a transaction id. `AccountCount` is one counted location: an `account_id`, the `actual_amount` the treasurer counted, and the `resolution` she chose.

**Reimbursement settlement posts exactly one row, after a pre-check that exists to produce a named error, not to close a race that cannot happen.**

```go
func (l *Ledger) SettleReimbursement(ctx context.Context, fundID, reimbursementID, accountID int64, occurredOn string) (store.Transaction, error)
```

Fetches the reimbursement; if `WaivedOn != nil`, returns `ErrReimbursementWaived`. Checks the new `GetReimbursementSettlement` query (see the M3 plan's query inventory): `sql.ErrNoRows` means "not yet settled, proceed"; a row means `ErrReimbursementAlreadySettled`. Then one `CreateTransaction(kind='reimbursement', direction='out', reimbursement_id=…, amount=reimbursement.Amount, purpose_id=reimbursement.PurposeID)`. The `reimbursement_settled_once` partial index remains the actual guarantee; under `SetMaxOpenConns(1)` a race between the pre-check and the insert is structurally impossible, so the pre-check is purely about giving the caller a clean, named error instead of a raw `UNIQUE constraint failed` string — defense in depth against a future bug, never a concurrency requirement this code leans on.

**Error taxonomy: sentinel errors for the small enumerable set of business-state failures the domain must branch on anyway.** `var ErrReimbursementAlreadySettled = errors.New(...)`, `ErrReimbursementWaived`, `ErrIncidentalAlreadyClosed` (see the M3 plan), the `ErrInvalidArgument` category below, plus `internal/money`'s own overflow errors — wrapped with `%w`, checked with `errors.Is`. This is the complete list M3 needs; it is exactly the set of cases where the domain already has to read something before deciding whether to write, so returning a named error costs nothing extra.

**Argument-shape validation is checked directly by each exported function** — `amount <= 0`, a malformed date, an empty required field — because the message is better than the matching `CHECK`'s, not because the domain distrusts the schema. This is validation of the call's own inputs, not a re-derivation of a cross-row invariant.

Those failures are a **fourth error category**, `ErrInvalidArgument`, wrapped with `%w` and carrying the offending field in the message. Without it they would fall into "anything unrecognized" and M4 would answer a caller's typo with a 500. The category also keeps the domain safe to call from outside an HTTP handler — [ADR-012](./012-backup-and-export.md)'s import is the next caller, and it has no request validation layer in front of it at all.

**Everything else is not pre-validated or translated.** Composite-FK violations, an account that belongs to another fund, an unrecognized `kind` — none of it is checked before the write, because the IDs involved arrive from earlier `Querier` calls the domain itself made (an `account_id` from `ListAccountsByFund`, a `purpose_id` from `ListPurposesByFund`). A violation there is a domain bug, not a caller mistake, and surfaces wrapped generically (`fmt.Errorf("posting transfer leg: %w", err)`) for M4 to map to a 500 and a log line. **Immutability-trigger `RAISE(ABORT, …)` errors are unreachable from this package's own API**, because the only `UPDATE` in the package is `CloseIncidentalAndRoll`'s, against the one table that carries no such trigger; every other write is an `INSERT` (`CLAUDE.md` rule 3) — so there is no code path here that could trip `transaction_immutable_update` or its siblings, so no translation is written for a message this package can never provoke. (Those triggers are, and stay, exercised directly against raw SQL in `internal/db`'s schema tests — that is what proves the database itself refuses the mutation, independent of whether `internal/ledger` ever asks for one.)

**Does the domain layer re-validate invariants the schema already enforces? No, as a general policy.** The two apparent exceptions above — settled-once, waived — are not that policy in disguise. They exist because the domain has to make a branching *decision* based on the outcome (return a named error vs. proceed to write), not because duplicating a `CHECK` or a partial index buys safety `SetMaxOpenConns(1)` doesn't already provide. Two sources of truth for the same invariant is worse than one, unless the second one is answering a different question — here, "what should I tell the caller," which the schema cannot answer at all.

## Consequences

`internal/ledger` is the only new package M3 adds under `internal/`; `CLAUDE.md`'s layout table no longer lists `members/`, `dues/` or `incidental/` beside it.

Every write-path test needs the real harness ([ADR-028](./028-testing-the-trust-core.md)); read-only logic that operates on already-fetched rows (dues-status classification, once the query results are in hand) can be tested with a hand-rolled `store.Querier` fake or plain structs, at the test author's discretion — the fakeability [ADR-005](./005-data-access-sqlc.md) promised is real, just scoped to the reads.

M4's handlers become a thin mapping from this ADR's sentinel errors to HTTP status codes (400 for `ErrInvalidArgument`, 409 for the two reimbursement errors and `ErrIncidentalAlreadyClosed`, 500 for anything unrecognized) — this ADR is what M4 reads to write that switch, so its error list should stay short and exhaustive rather than grow ad hoc.
