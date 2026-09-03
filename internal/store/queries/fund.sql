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

-- UpdateFund renames the fund. name is a display label - it heads every
-- screen and the public report - and nothing posted references it, so this
-- changes no history (the same reasoning UpdateAccount's own rename rests
-- on). currency and report_slug are deliberately not settable here: one is
-- an invariant through 0.x, the other is the report's unguessable address.
-- name: UpdateFund :one
UPDATE fund
SET name = ?
WHERE id = ?
RETURNING id, name, currency, report_slug, created_at;
