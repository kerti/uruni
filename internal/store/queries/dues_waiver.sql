-- A member's dues for one month, waived (ADR-050). Not a ledger fact: nothing
-- here is summed into a balance. Every read is fund-scoped; the period compares
-- as "YYYY-MM" text, which sorts chronologically (ADR-024).

-- name: CreateDuesWaiver :one
INSERT INTO dues_waiver (fund_id, member_id, dues_period, note, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, fund_id, member_id, dues_period, note, created_at;

-- Undoing a waiver deletes its row (it is not a ledger row, so immutability does
-- not reach it). The affected-row count tells the ledger whether the month was
-- waived at all.
-- name: DeleteDuesWaiver :execrows
DELETE FROM dues_waiver
WHERE fund_id = ? AND member_id = ? AND dues_period = ?;

-- One member's waivers, every month, for the single-member walk
-- (OutstandingDuesForMember, WaiveDues).
-- name: ListDuesWaiversByMember :many
SELECT id, fund_id, member_id, dues_period, note, created_at
FROM dues_waiver
WHERE fund_id = ? AND member_id = ?
ORDER BY dues_period;

-- A page of members' waivers in one pass (GET /api/members' arrears count).
-- name: ListDuesWaiversByMembers :many
SELECT id, fund_id, member_id, dues_period, note, created_at
FROM dues_waiver
WHERE fund_id = sqlc.arg('fund_id') AND member_id IN (sqlc.slice('member_ids'))
ORDER BY member_id, dues_period;

-- Every member's waivers for the months from_period..to_period inclusive: one
-- month (the status roster, the public report) or the twelve of the grid.
-- name: ListDuesWaiversInRange :many
SELECT id, fund_id, member_id, dues_period, note, created_at
FROM dues_waiver
WHERE fund_id = sqlc.arg('fund_id')
  AND dues_period >= sqlc.arg('from_period') AND dues_period <= sqlc.arg('to_period')
ORDER BY member_id, dues_period;

-- Every waiver of a fund, for the backup export; ordered only so two exports of
-- the same data are byte for byte the same.
-- name: ListDuesWaiversByFund :many
SELECT id, fund_id, member_id, dues_period, note, created_at
FROM dues_waiver
WHERE fund_id = ?
ORDER BY id;
