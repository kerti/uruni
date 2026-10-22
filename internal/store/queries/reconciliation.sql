-- name: CreateReconciliation :one
INSERT INTO reconciliation (fund_id, performed_at, through_transaction_id, note, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, fund_id, performed_at, through_transaction_id, note, created_at;

-- Fund-scoped: PRD section 6 allows a server to hold more than one fund, so a
-- bare lookup by id would be a cross-fund read.
-- name: GetReconciliation :one
SELECT id, fund_id, performed_at, through_transaction_id, note, created_at
FROM reconciliation
WHERE id = ? AND fund_id = ?;

-- Newest first: the home screen wants the last count, not the first.
-- name: ListReconciliationsByFund :many
SELECT id, fund_id, performed_at, through_transaction_id, note, created_at
FROM reconciliation
WHERE fund_id = ?
ORDER BY performed_at DESC, id DESC;

-- name: LatestReconciliation :one
SELECT id, fund_id, performed_at, through_transaction_id, note, created_at
FROM reconciliation
WHERE fund_id = ?
ORDER BY performed_at DESC, id DESC
LIMIT 1;

-- GET /api/reconciliations (#227, ADR-032): newest-first, keyset-paged on
-- (performed_at, id) like ListTransactionsPage (which has the reasoning). No
-- search - a handful of snapshots a year.
--
-- open_difference_amount is the sum of ABS(remainder_amount) across the
-- snapshot's lines (#498; see ListReconciliationLines) - the figure
-- Confirmation on /reconcile computes client-side, here so a list row can show
-- cocok (0) vs selisih without a detail fetch. Cast so it lands as int64
-- rather than interface{}: sqlc's SQLite engine cannot infer the type of a
-- summed expression (ADR-024).
--
-- page_limit is page size + 1: peek at one extra row.
-- name: ListReconciliationsPage :many
SELECT r.id, r.fund_id, r.performed_at, r.through_transaction_id, r.note, r.created_at,
  CAST(COALESCE((
    SELECT SUM(ABS(CASE rl.resolution
         WHEN 'left_open' THEN rl.difference_amount
         WHEN 'matched' THEN 0
         ELSE CASE
           WHEN fix.id IS NULL THEN 0
           ELSE rl.difference_amount - fix.signed_amount
         END
       END))
    FROM reconciliation_line rl
    LEFT JOIN ledger_entry fix ON fix.fund_id = rl.fund_id AND fix.id = rl.adjustment_transaction_id
    WHERE rl.reconciliation_id = r.id
  ), 0) AS INTEGER) AS open_difference_amount
FROM reconciliation r
WHERE r.fund_id = sqlc.arg('fund_id')
  AND (
    sqlc.narg('cursor_performed_at') IS NULL
    OR (r.performed_at, r.id) < (sqlc.narg('cursor_performed_at'), CAST(sqlc.narg('cursor_id') AS INTEGER))
  )
ORDER BY r.performed_at DESC, r.id DESC
LIMIT sqlc.arg('page_limit');
