-- name: CreateReconciliation :one
INSERT INTO reconciliation (fund_id, performed_at, through_transaction_id, note, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, fund_id, performed_at, through_transaction_id, note, created_at;

-- name: GetReconciliation :one
SELECT id, fund_id, performed_at, through_transaction_id, note, created_at
FROM reconciliation
WHERE id = ?;

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
