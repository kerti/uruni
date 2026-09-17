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

-- DeleteDuesTier removes a tier the fund never put anyone in (#232) - the
-- setup typo, the golongan renamed into existence twice, never a tier with
-- history behind it. No pre-check for members: member's composite FK
-- (fund_id, tier_id) refuses it on its own, and a COUNT(*) first would only
-- race it - the same reasoning DeleteAccount's own handler documents.
--
-- The caller runs this inside one transaction with DeleteDuesRatesByTier
-- below, rates first. Both orders of that pair matter: rates have their own
-- FK onto the tier, so the tier cannot go first, and if a member then
-- refuses the tier the whole transaction rolls back and the rates it had
-- already deleted come back with it.
-- name: DeleteDuesTier :exec
DELETE FROM dues_tier WHERE id = ? AND fund_id = ?;

-- Every rate belonging to one tier. Its own children, not history anyone was
-- charged under: a tier no member is in priced nothing, so its rates go with
-- it rather than standing in the way of deleting it.
-- name: DeleteDuesRatesByTier :exec
DELETE FROM dues_rate WHERE tier_id = ?;
