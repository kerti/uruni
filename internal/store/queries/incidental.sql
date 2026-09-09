-- name: CreateIncidental :one
INSERT INTO incidental (purpose_id, occasion, target_amount, opened_on, closed_on, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING purpose_id, occasion, target_amount, opened_on, closed_on, created_at;

-- Fund-scoped through purpose, which is where the envelope's fund_id lives
-- (incidental is 1:1 with its purpose row and carries no fund_id of its own).
-- An id names a row, it does not prove the caller may see it: PRD section 6 allows a
-- server to hold more than one fund, so a bare WHERE purpose_id = ? would be a
-- cross-fund read the moment a second fund exists.
-- name: GetIncidental :one
SELECT i.purpose_id, i.occasion, i.target_amount, i.opened_on, i.closed_on, i.created_at
FROM incidental i
JOIN purpose p ON p.id = i.purpose_id
WHERE i.purpose_id = ? AND p.fund_id = ?;

-- Closing an envelope moves no money, so this is an UPDATE rather than a ledger
-- entry - incidental carries no immutability trigger for exactly this reason.
-- name: CloseIncidental :one
UPDATE incidental
SET closed_on = ?
WHERE purpose_id = ?
RETURNING purpose_id, occasion, target_amount, opened_on, closed_on, created_at;

-- The guard's one query (ADR-031): sql.ErrNoRows for a purpose_id that is
-- not an incidental at all (main, pass_through - PostTransaction's caller
-- reads that as "no guard applies", not as a schema violation), a row with
-- closed_on NULL for an open envelope, and a row with closed_on set for a
-- closed one. Deliberately not scoped by fund_id: PostTransaction already
-- holds a purpose_id it trusts (its own caller's fund-scoped picker, or the
-- schema's own composite FK once the write lands), and this check runs
-- before that write, not in place of it.
-- name: IncidentalClosedOnForPurpose :one
SELECT closed_on
FROM incidental
WHERE purpose_id = ?;

-- The way back (ADR-031): closed_on to NULL, the exact inverse of
-- CloseIncidental above and, like it, a plain UPDATE rather than a ledger
-- entry - reopening moves no money either. Ledger.ReopenIncidental fetches
-- the envelope through GetIncidental's fund-scoped join first, so by the
-- time this runs the purpose_id is already known to belong to the caller's
-- fund; this query itself stays unscoped, the same shape CloseIncidental
-- already uses.
-- name: ReopenIncidental :one
UPDATE incidental
SET closed_on = NULL
WHERE purpose_id = ?
RETURNING purpose_id, occasion, target_amount, opened_on, closed_on, created_at;

-- Joined through purpose because that is where fund ownership lives; incidental
-- has no fund_id of its own (it is 1:1 with a purpose row).
-- name: ListIncidentalsByFund :many
SELECT i.purpose_id, i.occasion, i.target_amount, i.opened_on, i.closed_on, i.created_at
FROM incidental i
JOIN purpose p ON p.id = i.purpose_id
WHERE p.fund_id = ?
ORDER BY i.opened_on, i.purpose_id;

-- name: ListOpenIncidentalsByFund :many
SELECT i.purpose_id, i.occasion, i.target_amount, i.opened_on, i.closed_on, i.created_at
FROM incidental i
JOIN purpose p ON p.id = i.purpose_id
WHERE p.fund_id = ? AND i.closed_on IS NULL
ORDER BY i.opened_on, i.purpose_id;
