-- name: CreateDuesRate :one
INSERT INTO dues_rate (tier_id, amount, effective_from, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, tier_id, amount, effective_from, created_at;

-- The rate in force for a period: the latest row whose effective_from is at or
-- before it. One-sided intervals mean there is no end date to check, and no
-- row at all is a legitimate answer: a tier whose rate is not yet decided.
-- effective_from is 'YYYY-MM', so a string comparison is a chronological one.
-- name: GetEffectiveDuesRate :one
SELECT id, tier_id, amount, effective_from, created_at
FROM dues_rate
WHERE tier_id = ? AND effective_from <= ?
ORDER BY effective_from DESC
LIMIT 1;

-- name: ListDuesRatesByTier :many
SELECT id, tier_id, amount, effective_from, created_at
FROM dues_rate
WHERE tier_id = ?
ORDER BY effective_from;
