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
