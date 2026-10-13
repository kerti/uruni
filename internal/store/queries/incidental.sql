-- name: CreateIncidental :one
INSERT INTO incidental (purpose_id, occasion, target_amount, opened_on, closed_on, minimum_per_member, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING purpose_id, occasion, target_amount, opened_on, closed_on, minimum_per_member, created_at;

-- Fund-scoped through purpose, where the envelope's fund_id lives. PRD section
-- 6 allows a server to hold more than one fund, so a bare WHERE purpose_id = ?
-- would be a cross-fund read the moment a second fund exists.
-- name: GetIncidental :one
SELECT i.purpose_id, i.occasion, i.target_amount, i.opened_on, i.closed_on, i.minimum_per_member, i.created_at
FROM incidental i
JOIN purpose p ON p.id = i.purpose_id
WHERE i.purpose_id = ? AND p.fund_id = ?;

-- Closing an envelope moves no money: an UPDATE, not a ledger entry, which is
-- why incidental carries no immutability trigger.
-- name: CloseIncidental :one
UPDATE incidental
SET closed_on = ?
WHERE purpose_id = ?
RETURNING purpose_id, occasion, target_amount, opened_on, closed_on, minimum_per_member, created_at;

-- The closed-envelope guard's one query (ADR-031): sql.ErrNoRows for a
-- purpose_id that is not an incidental (main, pass_through - "no guard
-- applies"), closed_on NULL for an open envelope, set for a closed one. Not
-- scoped by fund_id: PostTransaction already holds a trusted purpose_id, and
-- the composite FK still guards the write that follows.
-- name: IncidentalClosedOnForPurpose :one
SELECT closed_on
FROM incidental
WHERE purpose_id = ?;

-- A mistyped occasion, corrected in place (#264). Unscoped: Ledger.RenameIncidental
-- already ran the fund-scoped UpdatePurposeName in the same transaction.
-- Renaming moves no money.
-- name: UpdateIncidentalOccasion :one
UPDATE incidental
SET occasion = ?
WHERE purpose_id = ?
RETURNING purpose_id, occasion, target_amount, opened_on, closed_on, minimum_per_member, created_at;

-- The way back (ADR-031): closed_on to NULL, a plain UPDATE like
-- CloseIncidental. Unscoped: Ledger.ReopenIncidental fetches through
-- GetIncidental's fund-scoped join first.
-- name: ReopenIncidental :one
UPDATE incidental
SET closed_on = NULL
WHERE purpose_id = ?
RETURNING purpose_id, occasion, target_amount, opened_on, closed_on, minimum_per_member, created_at;

-- Sets or clears the envelope's target and per-member minimum (ADR-034, #381):
-- mutable, never a posted fact. Unscoped: Ledger.SetIncidentalParticipation
-- fetches through GetIncidental's fund-scoped join first.
-- name: UpdateIncidentalExpectations :one
UPDATE incidental
SET target_amount = ?, minimum_per_member = ?
WHERE purpose_id = ?
RETURNING purpose_id, occasion, target_amount, opened_on, closed_on, minimum_per_member, created_at;

-- Joined through purpose, where fund ownership lives.
-- name: ListIncidentalsByFund :many
SELECT i.purpose_id, i.occasion, i.target_amount, i.opened_on, i.closed_on, i.minimum_per_member, i.created_at
FROM incidental i
JOIN purpose p ON p.id = i.purpose_id
WHERE p.fund_id = ?
ORDER BY i.opened_on, i.purpose_id;

-- name: ListOpenIncidentalsByFund :many
SELECT i.purpose_id, i.occasion, i.target_amount, i.opened_on, i.closed_on, i.minimum_per_member, i.created_at
FROM incidental i
JOIN purpose p ON p.id = i.purpose_id
WHERE p.fund_id = ? AND i.closed_on IS NULL
ORDER BY i.opened_on, i.purpose_id;
