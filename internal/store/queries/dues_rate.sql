-- name: CreateDuesRate :one
INSERT INTO dues_rate (tier_id, amount, effective_from, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, tier_id, amount, effective_from, created_at;

-- GetDuesRate is the lookup PATCH and DELETE both do ahead of the write: an
-- UPDATE's RETURNING would answer an unknown id with sql.ErrNoRows, but a
-- DELETE affecting zero rows raises nothing at all.
-- name: GetDuesRate :one
SELECT id, tier_id, amount, effective_from, created_at
FROM dues_rate
WHERE id = ?;

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

-- UpdateDuesRate corrects a mistyped amount. effective_from stays fixed: a
-- rate filed against the wrong month is deleted and re-posted, not moved.
-- name: UpdateDuesRate :one
UPDATE dues_rate
SET amount = ?
WHERE id = ?
RETURNING id, tier_id, amount, effective_from, created_at;

-- DeleteDuesRate is what makes a wrong-month rate correctable at all, since
-- UNIQUE (tier_id, effective_from) refuses the corrected row otherwise.
-- name: DeleteDuesRate :exec
DELETE FROM dues_rate
WHERE id = ?;
