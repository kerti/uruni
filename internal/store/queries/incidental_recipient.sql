-- The members an envelope is for (ADR-034). Unscoped: both callers
-- (Ledger.OpenIncidental, Ledger.SetIncidentalParticipation) already proved
-- purpose_id belongs to the caller's fund by a fund-scoped fetch earlier in
-- the same transaction.
-- name: CreateIncidentalRecipient :exec
INSERT INTO incidental_recipient (fund_id, purpose_id, member_id)
VALUES (?, ?, ?);

-- The full-replace half of SetIncidentalParticipation (ADR-034): a new list
-- clears the old one first. Unscoped for the reason CreateIncidentalRecipient
-- gives.
-- name: DeleteIncidentalRecipientsByPurpose :exec
DELETE FROM incidental_recipient
WHERE purpose_id = ?;

-- One envelope's recipients with the member name, so callers need no second
-- lookup. Ordered by name then id (as ListMembersPage), not insertion order,
-- which carries no meaning here.
-- name: ListIncidentalRecipients :many
SELECT ir.member_id, m.name AS member_name
FROM incidental_recipient ir
JOIN member m ON m.id = ir.member_id
WHERE ir.purpose_id = ?
ORDER BY m.name COLLATE NOCASE, m.id;

-- The backup export's read (ADR-012): raw table columns, no joined member_name,
-- since a restore reads member_id. The primary key is the (fund_id, purpose_id,
-- member_id) triple, so it orders by that, only for deterministic export bytes.
-- name: ListIncidentalRecipientsByFund :many
SELECT fund_id, purpose_id, member_id
FROM incidental_recipient
WHERE fund_id = ?
ORDER BY purpose_id, member_id;
