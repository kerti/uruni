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

-- UpdateDuesTier renames a tier - reference data, not history (issue #81).
-- name is the tier's only mutable field and is NOT NULL, so there is no
-- "leave alone vs. clear" ambiguity here the way there is on member: a
-- rename always names the new value.
-- name: UpdateDuesTier :one
UPDATE dues_tier
SET name = ?
WHERE id = ?
RETURNING id, fund_id, name, created_at;
