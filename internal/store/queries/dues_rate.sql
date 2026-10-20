-- dues_rate (ADR-040): a tier's price, month by month. A mistake in a rate is a
-- new row (the next correction of its month), never an UPDATE; a deletion is a
-- deleting correction (deleted = 1). Every pricing read goes through the
-- dues_rate_standing view (one row per tier and month that has a rate: the
-- highest correction, and none when that is a deletion); internal/db's guard
-- test fails a query that reads the table directly, except the ones named there
-- (GetDuesRateForFund, LatestDuesRateOfMonth, ListDuesRatesByFund). Another test
-- fails a statement that updates the table or deletes from it other than
-- DeleteDuesRatesByTier (dues_tier.sql) and restore's clear-all.

-- name: CreateDuesRate :one
INSERT INTO dues_rate (tier_id, amount, effective_from, correction, deleted, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, tier_id, amount, effective_from, correction, deleted, created_at;

-- The by-id lookup PATCH and DELETE start from (a named exception to the view
-- rule: it must find a row that no longer stands, to refuse it). dues_rate has
-- no fund_id, so scope comes through its tier (#188).
-- name: GetDuesRateForFund :one
SELECT dues_rate.id, dues_rate.tier_id, dues_rate.amount, dues_rate.effective_from, dues_rate.correction, dues_rate.deleted, dues_rate.created_at
FROM dues_rate
JOIN dues_tier ON dues_tier.id = dues_rate.tier_id
WHERE dues_rate.id = ? AND dues_tier.fund_id = ?;

-- The next-correction read (a named exception to the view rule): the row that
-- stands for this tier and month, deletion included, or no row when the month
-- has none. It must see a deleting correction, which the view hides, because it
-- decides between "next correction" and "UNIQUE collision". Ordered by
-- correction, never by id or created_at.
-- name: LatestDuesRateOfMonth :one
SELECT id, tier_id, amount, effective_from, correction, deleted, created_at
FROM dues_rate
WHERE tier_id = ? AND effective_from = ?
ORDER BY correction DESC
LIMIT 1;

-- The rate in force for a period: the latest month at or before it that still
-- has a rate. No row is a legitimate answer (a tier whose rate is not yet
-- decided, or whose only rates were deleted). effective_from is 'YYYY-MM', so
-- string order is chronological.
-- name: GetEffectiveDuesRate :one
SELECT id, tier_id, amount, effective_from, correction, created_at
FROM dues_rate_standing
WHERE tier_id = ? AND effective_from <= ?
ORDER BY effective_from DESC
LIMIT 1;

-- What the Golongan screen lists: the rates in force, oldest month first. A
-- month whose rate was deleted does not show.
-- name: ListDuesRatesByTier :many
SELECT id, tier_id, amount, effective_from, correction, created_at
FROM dues_rate_standing
WHERE tier_id = ?
ORDER BY effective_from;

-- The backup export's read (ADR-012, a named exception to the view rule): every
-- row of the fund's tiers, superseded and deleting ones included, because the
-- trail is the data. Ordered by id only for deterministic export bytes, not by
-- effective date.
-- name: ListDuesRatesByFund :many
SELECT dues_rate.id, dues_rate.tier_id, dues_rate.amount, dues_rate.effective_from, dues_rate.correction, dues_rate.deleted, dues_rate.created_at
FROM dues_rate
JOIN dues_tier ON dues_tier.id = dues_rate.tier_id
WHERE dues_tier.fund_id = ?
ORDER BY dues_rate.id;

-- The rates that price a roster (DuesStatusForPeriod): every tier's rates in
-- force across the fund, ordered tier then month, the order the Go fold needs.
-- Folded per (tier, period) as "the latest row at or before", so a roster costs
-- one query however many members it holds.
-- name: ListDuesRatesForPricing :many
SELECT dr.id, dr.tier_id, dr.amount, dr.effective_from, dr.correction, dr.created_at
FROM dues_rate_standing dr
JOIN dues_tier ON dues_tier.id = dr.tier_id
WHERE dues_tier.fund_id = ?
ORDER BY dr.tier_id, dr.effective_from;

-- The rates that price one member (OutstandingDuesForMember, paidThrough): those
-- of every tier the member has a standing row for. A tier named only by a
-- superseded row prices nothing, so it is not fetched.
-- name: ListDuesRatesForMemberPricing :many
SELECT dr.id, dr.tier_id, dr.amount, dr.effective_from, dr.correction, dr.created_at
FROM dues_rate_standing dr
WHERE dr.tier_id IN (
  SELECT mt.tier_id FROM member_tier_standing mt
  WHERE mt.fund_id = ? AND mt.member_id = ? AND mt.tier_id IS NOT NULL
)
ORDER BY dr.tier_id, dr.effective_from;
