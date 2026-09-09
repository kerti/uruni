-- name: CreatePurpose :one
INSERT INTO purpose (fund_id, kind, name, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, fund_id, kind, name, created_at;

-- GetPurposeForFund is the only single-purpose lookup, fund-scoped for the
-- reason GetMemberForFund is (#188): an id belonging to another fund
-- answers sql.ErrNoRows rather than being found and only then rejected.
-- name: GetPurposeForFund :one
SELECT id, fund_id, kind, name, created_at
FROM purpose
WHERE id = ? AND fund_id = ?;

-- name: ListPurposesByFund :many
SELECT id, fund_id, kind, name, created_at
FROM purpose
WHERE fund_id = ?
ORDER BY id;

-- ListSelectablePurposesByFund is ListPurposesByFund with a closed
-- incidental's purpose excluded (ADR-031): GET /api/purposes?selectable=true
-- backs the everyday record form's picker, which stops offering what
-- PostTransaction's own guard would now refuse. The LEFT JOIN is what makes
-- "not an incidental at all" and "an incidental that's still open" the same
-- case - main and pass_through purposes have no incidental row to join
-- against, so i.closed_on reads NULL for them exactly as it does for an open
-- envelope, and both pass the filter. A closed envelope's purpose_id is the
-- only row this excludes, never a field added to purposeResponse - the
-- lifecycle lives in the filter, not on the wire shape every other caller
-- reads too.
-- name: ListSelectablePurposesByFund :many
SELECT p.id, p.fund_id, p.kind, p.name, p.created_at
FROM purpose p
LEFT JOIN incidental i ON i.purpose_id = p.id
WHERE p.fund_id = ? AND i.closed_on IS NULL
ORDER BY p.id;

-- UpdatePurposeName renames a purpose. The name is a label - a posted
-- transaction references the purpose by id, and nothing in the ledger reads
-- the text - so this is the same correction UpdateAccount makes for a
-- location. Which purposes may be renamed is the handler's call, not this
-- query's: kind is policy (only 'pass_through' today), not shape.
-- name: UpdatePurposeName :one
UPDATE purpose
SET name = ?
WHERE id = ?
RETURNING id, fund_id, kind, name, created_at;
