-- name: CreateAccount :one
INSERT INTO account (fund_id, kind, name, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, fund_id, kind, name, created_at;

-- name: GetAccount :one
SELECT id, fund_id, kind, name, created_at
FROM account
WHERE id = ?;

-- name: ListAccountsByFund :many
SELECT id, fund_id, kind, name, created_at
FROM account
WHERE fund_id = ?
ORDER BY id;
