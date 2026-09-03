-- name: CreateDuesTier :one
INSERT INTO dues_tier (fund_id, name, created_at)
VALUES (?, ?, ?)
RETURNING id, fund_id, name, created_at;

-- GetDuesTierForFund is the only single-tier lookup, fund-scoped for the
-- reason GetMemberForFund is (#188).
-- name: GetDuesTierForFund :one
SELECT id, fund_id, name, created_at
FROM dues_tier
WHERE id = ? AND fund_id = ?;

-- name: ListDuesTiersByFund :many
SELECT id, fund_id, name, created_at
FROM dues_tier
WHERE fund_id = ?
ORDER BY id;

-- UpdateDuesTier renames a tier - reference data, not history.
-- name: UpdateDuesTier :one
UPDATE dues_tier
SET name = ?
WHERE id = ?
RETURNING id, fund_id, name, created_at;
