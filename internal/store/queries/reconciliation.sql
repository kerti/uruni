-- name: CreateReconciliation :one
INSERT INTO reconciliation (fund_id, performed_at, through_transaction_id, note, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, fund_id, performed_at, through_transaction_id, note, created_at;

-- Fund-scoped: an id names a row, it does not prove the caller may see it.
-- PRD section 6 allows a server to hold more than one fund, so a bare lookup
-- by id alone would be a cross-fund read the moment a second fund exists.
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

-- GET /api/reconciliations's real listing (#227, ADR-032 "Lists: paging and
-- search") - newest-first and keyset-paged on (performed_at, id), the same
-- row-value-keyset shape ListReimbursementsPage/ListTransactionsPage use
-- (their own comments have the full reasoning). No search - a handful of
-- dated snapshots a year, per the issue.
--
-- open_difference_amount is the sum of ABS(difference_amount) across that
-- snapshot's still-open lines - the same figure Confirmation on /reconcile
-- computes client-side from a detail fetch, computed here so a list row can
-- show cocok (0) vs selisih without one. Cast so it lands as int64 rather
-- than interface{} - sqlc's SQLite engine cannot infer the type of a summed
-- expression (ADR-024).
--
-- page_limit is page size + 1, the same "peek at one extra row" trick the
-- other two paged lists use.
-- name: ListReconciliationsPage :many
SELECT r.id, r.fund_id, r.performed_at, r.through_transaction_id, r.note, r.created_at,
  CAST(COALESCE((
    SELECT SUM(ABS(rl.difference_amount))
    FROM reconciliation_line rl
    WHERE rl.reconciliation_id = r.id AND rl.resolution = 'left_open'
  ), 0) AS INTEGER) AS open_difference_amount
FROM reconciliation r
WHERE r.fund_id = sqlc.arg('fund_id')
  AND (
    sqlc.narg('cursor_performed_at') IS NULL
    OR (r.performed_at, r.id) < (sqlc.narg('cursor_performed_at'), CAST(sqlc.narg('cursor_id') AS INTEGER))
  )
ORDER BY r.performed_at DESC, r.id DESC
LIMIT sqlc.arg('page_limit');
