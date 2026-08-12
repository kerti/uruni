-- name: CreateDuesTier :one
INSERT INTO dues_tier (fund_id, name, created_at)
VALUES (?, ?, ?)
RETURNING id, fund_id, name, created_at;

-- name: GetDuesTier :one
SELECT id, fund_id, name, created_at
FROM dues_tier
WHERE id = ?;

-- name: ListDuesTiersByFund :many
SELECT id, fund_id, name, created_at
FROM dues_tier
WHERE fund_id = ?
ORDER BY id;
