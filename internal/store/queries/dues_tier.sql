-- name: CreateDuesTier :one
INSERT INTO dues_tier (fund_id, name, created_at)
VALUES (?, ?, ?)
RETURNING id, fund_id, name, created_at;

-- Fund-scoped like GetMemberForFund (#188).
-- name: GetDuesTierForFund :one
SELECT id, fund_id, name, created_at
FROM dues_tier
WHERE id = ? AND fund_id = ?;

-- By name, ignoring case (maintainer, 2026-10-05).
-- name: ListDuesTiersByFund :many
SELECT id, fund_id, name, created_at
FROM dues_tier
WHERE fund_id = ?
ORDER BY name COLLATE NOCASE, id;

-- UpdateDuesTier renames a tier - reference data, not history.
-- name: UpdateDuesTier :one
UPDATE dues_tier
SET name = ?
WHERE id = ?
RETURNING id, fund_id, name, created_at;

-- Removes a tier the fund never put anyone in (#232): the setup typo, never a
-- tier with history. "Ever": member_tier's composite (fund_id, tier_id) FK
-- refuses a tier any row names, a superseded one included (ADR-040). No
-- pre-check: the FK is the check, and a COUNT(*) first would only race it.
--
-- The caller runs this in one transaction after DeleteDuesRatesByTier: rates
-- FK onto the tier so it cannot go first, and if a member then refuses the
-- tier the transaction rolls back and the deleted rates come back.
-- name: DeleteDuesTier :exec
DELETE FROM dues_tier WHERE id = ? AND fund_id = ?;

-- A tier no member was ever in priced nothing, so its rates are not history
-- anyone was charged under and go with it.
-- name: DeleteDuesRatesByTier :exec
DELETE FROM dues_rate WHERE tier_id = ?;
