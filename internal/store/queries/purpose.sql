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
