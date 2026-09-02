-- name: CreateAccount :one
INSERT INTO account (fund_id, kind, name, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, fund_id, kind, name, created_at, inactive_on;

-- name: GetAccount :one
SELECT id, fund_id, kind, name, created_at, inactive_on
FROM account
WHERE id = ?;

-- name: ListAccountsByFund :many
SELECT id, fund_id, kind, name, created_at, inactive_on
FROM account
WHERE fund_id = ?
ORDER BY id;

-- UpdateAccount is a correction to a location's own label, not a ledger
-- event: renaming or retiring an account changes nothing already posted
-- (account.name is a label, not a posted fact). name is NOT NULL, so
-- COALESCE covers it the same way UpdateMember's does; inactive_on needs the
-- set_inactive_on flag for the same reason UpdateMember's does - a null
-- argument is ambiguous between "leave alone" and "reinstate".
-- name: UpdateAccount :one
UPDATE account
SET name        = COALESCE(sqlc.narg('name'), name),
    inactive_on = CASE WHEN CAST(sqlc.arg('set_inactive_on') AS INTEGER) = 1 THEN sqlc.narg('inactive_on') ELSE inactive_on END
WHERE id = sqlc.arg('id')
RETURNING id, fund_id, kind, name, created_at, inactive_on;

-- DeleteAccount leans on the composite foreign keys from "transaction" and
-- reconciliation_line to refuse it once real data references the account -
-- for a never-used duplicate only; a used-then-retired account gets
-- UpdateAccount's inactive_on instead.
-- name: DeleteAccount :exec
DELETE FROM account
WHERE id = ?;
