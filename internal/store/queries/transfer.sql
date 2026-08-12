-- name: CreateTransfer :one
INSERT INTO transfer (fund_id, kind, created_at)
VALUES (?, ?, ?)
RETURNING id, fund_id, kind, created_at;

-- name: GetTransfer :one
SELECT id, fund_id, kind, created_at
FROM transfer
WHERE id = ?;

-- name: ListTransfersByFund :many
SELECT id, fund_id, kind, created_at
FROM transfer
WHERE fund_id = ?
ORDER BY id;
