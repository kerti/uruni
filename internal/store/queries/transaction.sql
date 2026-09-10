-- name: CreateTransaction :one
INSERT INTO "transaction" (
  fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
  member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
  note, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
          member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
          note, created_at;

-- name: GetTransaction :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE id = ?;

-- The fund-scoped fetch a dues reversal fetches its target through (ADR-029):
-- WHERE fund_id = ? AND id = ?, not id alone, so a transaction belonging to
-- another fund reads as sql.ErrNoRows here rather than being found and only
-- later rejected by the composite FK. GetTransaction above stays as it is -
-- other callers already have their fund-scoped row in hand - this is the one
-- new caller that fetches a transaction by id from an untrusted request.
-- name: GetTransactionForFund :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE fund_id = ? AND id = ?;

-- The reversed-once pre-check, same shape as GetReimbursementSettlement and
-- GetOpeningBalance above: sql.ErrNoRows means "not yet reversed, proceed"
-- (the expected, non-error path); a row means it already has been. The
-- dues_payment_reversed_once partial unique index is the actual guarantee -
-- this pre-check only turns a raw constraint violation into a clean, named
-- error (ADR-029, mirroring ADR-027's ErrReimbursementAlreadySettled).
-- name: GetDuesPaymentReversal :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE fund_id = ? AND reverses_transaction_id = ?;

-- name: ListTransactionsByFund :many
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE fund_id = ?
ORDER BY occurred_on, id;

-- GET /api/transactions's real listing (#225, ADR-032 "Lists: paging and
-- search") - newest-first and keyset-paged on the same pair it orders by.
-- LIMIT/OFFSET was rejected outright: occurred_on is backdatable (PRD
-- section 7.2 defaults to today, editable), so a row can land in the middle
-- of a newest-first list between two page fetches and an offset would
-- silently skip or duplicate it. The row-value comparison below cannot:
-- cursor_occurred_on/cursor_id NULL on the first page (the whole clause
-- short-circuits true and nothing is excluded), and a later page passes the
-- previous page's last row, keeping only strictly older pairs regardless of
-- what was inserted since. cursor_id is CAST to INTEGER inside the tuple -
-- without it sqlc infers its Go type from the row-value's other element
-- (cursor_occurred_on, TEXT) instead of the id column it is actually
-- compared against, and generates CursorID as *string for what is an int64
-- primary key.
--
-- Search is every clause under q: a case-insensitive substring test over
-- note, purpose name and member name (LEFT JOIN - most rows carry no
-- member_id), plus an exact amount match. INSTR(LOWER(col), LOWER(q)) > 0
-- rather than "col COLLATE NOCASE LIKE q ESCAPE '$'" as ADR-032 literally
-- asks for: sqlc v1.31.1's SQLite query analyzer only registers the FIRST
-- "COLLATE ... LIKE <param> ESCAPE '<literal>'" clause in a query and
-- silently leaves every later occurrence of that exact construct as
-- un-rewritten literal text ("sqlc.narg('q_purpose')" etc, verbatim) in the
-- emitted SQL - a bare LIKE with no ESCAPE, or the same clause used only
-- once, both generate correctly, which is what makes this so easy to ship
-- unnoticed (confirmed by bisection: swapping ESCAPE for INSTR/LOWER below,
-- with nothing else changed, fixes it). No test in this file could have
-- caught it either - `go vet`/the compiler see a struct simply missing the
-- fields the broken clauses reference, `make sqlc`'s own generate step
-- exits 0, and the query is syntactically valid SQL that runs and returns
-- rows; it is only wrong once a purpose- or member-name search is exercised
-- against real data. INSTR does not interpret % or _ as wildcards at all
-- (it is a plain substring test), so this also drops the ESCAPE-and-wildcard-
-- escaping machinery ADR-032 assumed LIKE would need - "LIKE wildcards in q
-- are literal" is true here by construction, not by escaping.
--
-- member_id/dues_period are exact-match filters for
-- Dues/MemberPayments.tsx's payment history panel, not a Riwayat UI filter
-- (ADR-032 holds filters to M7) - undocumented in copy/UI on purpose.
--
-- page_limit is passed as page size + 1: the caller peeks at whether that
-- extra row came back to know whether a next page exists, then trims it
-- before building the response.
-- name: ListTransactionsPage :many
SELECT t.id, t.fund_id, t.account_id, t.purpose_id, t.direction, t.amount, t.occurred_on, t.kind,
       t.member_id, t.dues_period, t.reimbursement_id, t.transfer_id, t.reverses_transaction_id,
       t.note, t.created_at
FROM "transaction" t
JOIN purpose p ON p.id = t.purpose_id
LEFT JOIN member m ON m.id = t.member_id
WHERE t.fund_id = sqlc.arg('fund_id')
  AND (
    sqlc.narg('cursor_occurred_on') IS NULL
    OR (t.occurred_on, t.id) < (sqlc.narg('cursor_occurred_on'), CAST(sqlc.narg('cursor_id') AS INTEGER))
  )
  AND (
    sqlc.narg('q') IS NULL
    OR INSTR(LOWER(t.note), LOWER(sqlc.narg('q'))) > 0
    OR INSTR(LOWER(p.name), LOWER(sqlc.narg('q'))) > 0
    OR INSTR(LOWER(m.name), LOWER(sqlc.narg('q'))) > 0
    OR t.amount = sqlc.narg('q_amount')
  )
  AND (sqlc.narg('member_id') IS NULL OR t.member_id = sqlc.narg('member_id'))
  AND (sqlc.narg('dues_period') IS NULL OR t.dues_period = sqlc.narg('dues_period'))
ORDER BY t.occurred_on DESC, t.id DESC
LIMIT sqlc.arg('page_limit');

-- Every balance in Uruni is this shape: sum the ledger, never read a stored
-- total (CLAUDE.md rule 2). direction carries the sign, so the CASE is the
-- only place a minus appears. The CAST is not decoration - without it sqlc
-- emits interface{} for the aggregate and the trust core loses its compiler
-- exactly where it is needed most (ADR-024).
-- name: FundBalance :one
SELECT CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) AS INTEGER) AS balance_amount
FROM "transaction"
WHERE fund_id = ?;

-- name: AccountBalance :one
SELECT CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) AS INTEGER) AS balance_amount
FROM "transaction"
WHERE fund_id = ? AND account_id = ?;

-- name: PurposeBalance :one
SELECT CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) AS INTEGER) AS balance_amount
FROM "transaction"
WHERE fund_id = ? AND purpose_id = ?;

-- The frozen half of a reconciliation: the same sum as AccountBalance, cut off
-- at the snapshot's through_transaction_id. Ordered by id, not occurred_on, so
-- a correction backdated after the count keeps its higher id and stays outside
-- the old snapshot's arithmetic while counting toward today's balance.
-- name: AccountBalanceThrough :one
SELECT CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) AS INTEGER) AS balance_amount
FROM "transaction"
WHERE fund_id = ? AND account_id = ? AND id <= ?;

-- name: ListDuesPaymentsByMember :many
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, note, created_at
FROM "transaction"
WHERE member_id = ? AND kind = 'dues'
ORDER BY dues_period, id;

-- The envelope's net, everything it has ever posted against its own
-- purpose_id, rolls included. This is what CloseIncidentalAndRoll leans on
-- (ADR-031): collected minus disbursed here is exactly PurposeBalance for
-- this purpose, so a prior roll's own leg already nets the envelope to zero
-- by construction, and reopening, posting a late entry, and closing again
-- computes the *net delta* through this same unfiltered sum rather than new
-- arithmetic. Leftover is collected minus disbursed, computed in Go via
-- money.Amount.Sub, rather than a third column here.
--
-- Not what GET /api/incidentals/{purposeID} shows: that figure wants what
-- the occasion itself collected and spent, with a prior roll's leg excluded
-- - see IncidentalActivityTotals below. Conflating the two was #215; this
-- comment is the seam between them.
-- name: IncidentalTotals :one
SELECT
  CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE 0 END), 0) AS INTEGER) AS collected_amount,
  CAST(COALESCE(SUM(CASE WHEN direction = 'out' THEN amount ELSE 0 END), 0) AS INTEGER) AS disbursed_amount
FROM "transaction"
WHERE fund_id = ? AND purpose_id = ?;

-- What the occasion itself collected and disbursed (PRD section 7.5's
-- detail screen), as opposed to IncidentalTotals above's net-including-rolls. The
-- LEFT JOIN excludes only a reclass_purpose leg - the roll CloseIncidentalAndRoll
-- posts, in either direction (ADR-031's leftover-in as much as the original
-- leftover-out) - by transfer.kind, not by transaction.kind: every leg of
-- every transfer is posted as transaction.kind='transfer' regardless of
-- whether the transfer itself is 'between_accounts' or 'reclass_purpose', so
-- transaction.kind alone cannot tell a roll's leg from an ordinary transfer
-- between this envelope's own accounts, and this screen has no reason to
-- exclude the latter. tr.id IS NULL keeps every row the join found no
-- matching reclass_purpose transfer for - which is every kind but that one.
-- name: IncidentalActivityTotals :one
SELECT
  CAST(COALESCE(SUM(CASE WHEN t.direction = 'in' THEN t.amount ELSE 0 END), 0) AS INTEGER) AS collected_amount,
  CAST(COALESCE(SUM(CASE WHEN t.direction = 'out' THEN t.amount ELSE 0 END), 0) AS INTEGER) AS disbursed_amount
FROM "transaction" t
LEFT JOIN transfer tr ON tr.id = t.transfer_id AND tr.kind = 'reclass_purpose'
WHERE t.fund_id = ? AND t.purpose_id = ? AND tr.id IS NULL;

-- The roster query behind "who has paid / partially / not yet" for one
-- dues_period, across every member in one pass rather than one query per
-- member.
-- name: DuesPaidByPeriod :many
-- The AND NOT EXISTS clause is ADR-029's half of the reversal design: a
-- reversed dues row itself never matched kind = 'dues' in the first place
-- (a reversal is posted as kind='adjustment'), so this drops the ORIGINAL
-- row from the sum once something reverses it - which is what makes a
-- reversed payment disappear from "paid" rather than simply counting twice.
SELECT member_id, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS paid_amount
FROM "transaction" t
WHERE t.fund_id = ? AND t.kind = 'dues' AND t.dues_period = ?
  AND NOT EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id)
GROUP BY member_id;

-- DuesPaidByMemberGroupedByPeriod is DuesPaidByPeriod's transpose: every
-- period one member has paid anything toward, in one pass over that
-- member's whole history rather than one query per period
-- (OutstandingDuesForMember's range walk). Same reversed-row exclusion
-- (ADR-029) and the same CAST(... AS TEXT) forcing dues_period non-null
-- LatestDuesPeriodPaidByMember already relies on - kind = 'dues' rows always
-- carry a dues_period (schema CHECK), so this is never actually NULL, only
-- untyped without the cast.
-- name: DuesPaidByMemberGroupedByPeriod :many
SELECT CAST(dues_period AS TEXT) AS dues_period, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS paid_amount
FROM "transaction" t
WHERE t.fund_id = ? AND t.member_id = ? AND t.kind = 'dues'
  AND NOT EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id)
GROUP BY dues_period;

-- The "paid in advance" signal. dues_period is 'YYYY-MM', so a lexicographic
-- MAX is also the chronological one. The CAST(... AS TEXT) is load-bearing,
-- not decoration: uncast, MAX(dues_period) generates interface{} - the same
-- untyped-interface trap ADR-024 documents for SUM, reached here through MAX
-- on a TEXT column - and a .(string) assertion on it is a live panic risk
-- since drivers commonly hand back []byte.
-- name: LatestDuesPeriodPaidByMember :many
-- The AND NOT EXISTS clause is ADR-029's fix for the specific bug the chosen
-- design exists to avoid: without it, a reversed row would still be the
-- chronological MAX(dues_period) for its member, reading as "paid in
-- advance" through a period that was reversed and is no longer paid at all.
SELECT member_id, CAST(MAX(dues_period) AS TEXT) AS latest_period
FROM "transaction" t
WHERE t.fund_id = ? AND t.kind = 'dues'
  AND NOT EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id)
GROUP BY member_id;

-- The reconciliation cutoff. Deliberately not an aggregate: SELECT
-- CAST(MAX(id) AS INTEGER) generates a non-nullable (int64, error), and a
-- bare aggregate with no GROUP BY still returns one row on an empty table
-- with the value NULL - so a fund's first-ever reconciliation would fail
-- with "converting NULL to int64 is unsupported". This form returns zero
-- rows instead, giving a clean sql.ErrNoRows the domain reads as "no ledger
-- yet".
-- name: MaxTransactionIDByFund :one
SELECT id
FROM "transaction"
WHERE fund_id = ?
ORDER BY id DESC
LIMIT 1;

-- The settle-once pre-check. Returns sql.ErrNoRows when the claim has not
-- been settled yet (the expected, non-error path) or the settling row when
-- it has.
-- name: GetReimbursementSettlement :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, note, created_at
FROM "transaction"
WHERE fund_id = ? AND reimbursement_id = ? AND kind = 'reimbursement';

-- The one-opening-per-account pre-check, same shape as
-- GetReimbursementSettlement above: no aggregate, so a fund with no opening
-- entry on this account yet returns a clean sql.ErrNoRows (the expected,
-- non-error path) rather than a NULL forced through an aggregate. A row means
-- one already exists.
-- name: GetOpeningBalance :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, note, created_at
FROM "transaction"
WHERE fund_id = ? AND account_id = ? AND kind = 'opening';

