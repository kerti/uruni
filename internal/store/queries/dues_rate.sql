-- name: CreateDuesRate :one
INSERT INTO dues_rate (tier_id, amount, effective_from, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, tier_id, amount, effective_from, created_at;

-- The lookup PATCH and DELETE both do ahead of the write: an UPDATE's
-- RETURNING answers an unknown id with sql.ErrNoRows, but a DELETE affecting
-- zero rows raises nothing. dues_rate has no fund_id, so scope comes through
-- its tier (#188).
-- name: GetDuesRateForFund :one
SELECT dues_rate.id, dues_rate.tier_id, dues_rate.amount, dues_rate.effective_from, dues_rate.created_at
FROM dues_rate
JOIN dues_tier ON dues_tier.id = dues_rate.tier_id
WHERE dues_rate.id = ? AND dues_tier.fund_id = ?;

-- The rate in force for a period: the latest row whose effective_from is at or
-- before it. No row is a legitimate answer (a tier whose rate is not yet
-- decided). effective_from is 'YYYY-MM', so string order is chronological.
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

-- Corrects a mistyped amount. effective_from stays fixed: a rate filed against
-- the wrong month is deleted and re-posted, not moved.
-- name: UpdateDuesRate :one
UPDATE dues_rate
SET amount = ?
WHERE id = ?
RETURNING id, tier_id, amount, effective_from, created_at;

-- What makes a wrong-month rate correctable: UNIQUE (tier_id, effective_from)
-- refuses the corrected row otherwise.
-- name: DeleteDuesRate :exec
DELETE FROM dues_rate
WHERE id = ?;

-- The backup export's read (ADR-012): every rate across the fund's tiers,
-- scoped through dues_tier. Ordered by id only for deterministic export bytes,
-- not by effective date.
-- name: ListDuesRatesByFund :many
SELECT dues_rate.id, dues_rate.tier_id, dues_rate.amount, dues_rate.effective_from, dues_rate.created_at
FROM dues_rate
JOIN dues_tier ON dues_tier.id = dues_rate.tier_id
WHERE dues_tier.fund_id = ?
ORDER BY dues_rate.id;

-- The rates that price a roster (DuesStatusForPeriod): every tier's rates across
-- the fund, ordered tier then month, the order the Go fold needs. Folded per
-- (tier, period) as "the latest row at or before", so a roster costs one query
-- however many members it holds. This and the next query are the rate reads the
-- rate-correction work (ADR-040) points at the standing view.
-- name: ListDuesRatesForPricing :many
SELECT dues_rate.id, dues_rate.tier_id, dues_rate.amount, dues_rate.effective_from, dues_rate.created_at
FROM dues_rate
JOIN dues_tier ON dues_tier.id = dues_rate.tier_id
WHERE dues_tier.fund_id = ?
ORDER BY dues_rate.tier_id, dues_rate.effective_from;

-- The rates that price one member (OutstandingDuesForMember, paidThrough): those
-- of every tier the member has a standing row for. A tier named only by a
-- superseded row prices nothing, so it is not fetched.
-- name: ListDuesRatesForMemberPricing :many
SELECT dr.id, dr.tier_id, dr.amount, dr.effective_from, dr.created_at
FROM dues_rate dr
WHERE dr.tier_id IN (
  SELECT mt.tier_id FROM member_tier_standing mt
  WHERE mt.fund_id = ? AND mt.member_id = ? AND mt.tier_id IS NOT NULL
)
ORDER BY dr.tier_id, dr.effective_from;
