-- name: CreatePurpose :one
INSERT INTO purpose (fund_id, kind, name, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, fund_id, kind, name, created_at;

-- name: GetPurpose :one
SELECT id, fund_id, kind, name, created_at
FROM purpose
WHERE id = ?;

-- name: ListPurposesByFund :many
SELECT id, fund_id, kind, name, created_at
FROM purpose
WHERE fund_id = ?
ORDER BY id;
