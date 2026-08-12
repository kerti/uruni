-- name: CreateFund :one
INSERT INTO fund (name, currency, report_slug, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, name, currency, report_slug, created_at;

-- name: GetFund :one
SELECT id, name, currency, report_slug, created_at
FROM fund
WHERE id = ?;

-- name: ListFunds :many
SELECT id, name, currency, report_slug, created_at
FROM fund
ORDER BY id;
