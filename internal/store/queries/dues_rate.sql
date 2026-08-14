-- name: CreateDuesRate :one
INSERT INTO dues_rate (tier_id, amount, effective_from, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, tier_id, amount, effective_from, created_at;

-- GetDuesRate is the resolve-by-id lookup PATCH and DELETE /api/dues-rates/{id}
-- need ahead of the write (issue #81), the same "look it up, 404 if it's not
-- there" shape resolveDuesTier already uses for the tier routes: an UPDATE's
-- RETURNING clause would answer an unknown id with sql.ErrNoRows too, but a
-- DELETE affecting zero rows does not error at all, so the two routes need a
-- consistent pre-check rather than one that only accidentally works for one
-- of them.
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

-- UpdateDuesRate corrects a mistyped amount (issue #81). effective_from is
-- deliberately not editable here: the row's period is what UNIQUE (tier_id,
-- effective_from) polices, and a rate entered against the wrong month is
-- fixed by deleting it and creating a new one for the right one (below),
-- not by mutating the period in place.
-- name: UpdateDuesRate :one
UPDATE dues_rate
SET amount = ?
WHERE id = ?
RETURNING id, tier_id, amount, effective_from, created_at;

-- DeleteDuesRate is what makes a rate entered against the wrong month
-- correctable at all, since UNIQUE (tier_id, effective_from) otherwise
-- refuses the corrected row outright (issue #81). Nothing in the ledger
-- references a dues_rate - a dues payment stores the amount paid, not the
-- rate (ADR-027) - so there is no foreign key for SQLite to enforce here.
-- name: DeleteDuesRate :exec
DELETE FROM dues_rate
WHERE id = ?;
