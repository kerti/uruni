-- member_tier (ADR-040): a member's tier, month by month. Every pricing read goes
-- through the member_tier_standing view (one standing row per member and month,
-- the highest correction); internal/db's guard test fails a query that reads
-- the table directly, except the ones named there.

-- name: CreateMemberTier :one
INSERT INTO member_tier (fund_id, member_id, tier_id, effective_from, correction, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, member_id, tier_id, effective_from, correction, created_at;

-- Every member's standing rows across the fund, for the readers that price a
-- whole roster at once: folded per member in Go, so no read adds a per-member
-- query. Ordered by month, the order the fold needs, never by id or created_at.
-- name: ListMemberTiersByFund :many
SELECT id, fund_id, member_id, tier_id, effective_from, correction, created_at
FROM member_tier_standing
WHERE fund_id = ?
ORDER BY member_id, effective_from;

-- One member's standing rows, oldest month first.
-- name: ListMemberTiersForMember :many
SELECT id, fund_id, member_id, tier_id, effective_from, correction, created_at
FROM member_tier_standing
WHERE fund_id = ? AND member_id = ?
ORDER BY effective_from;

-- The next-correction read (a named exception to the view rule): the highest
-- correction number any row of this member and month carries, or -1 when the
-- month has none. It must see superseded rows, which the view hides.
-- name: LatestMemberTierCorrection :one
SELECT CAST(COALESCE(MAX(correction), -1) AS INTEGER) AS latest
FROM member_tier
WHERE member_id = ? AND effective_from = ?;

-- The backup export's read (ADR-012, a named exception to the view rule): every
-- row, superseded ones included, because the trail is the data. Ordered by id
-- only for deterministic export bytes.
-- name: ListMemberTiersForExport :many
SELECT id, fund_id, member_id, tier_id, effective_from, correction, created_at
FROM member_tier
WHERE fund_id = ?
ORDER BY id;
