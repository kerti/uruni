-- name: CreatePurpose :one
INSERT INTO purpose (fund_id, kind, name, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, fund_id, kind, name, created_at;

-- Fund-scoped like GetMemberForFund (#188).
-- name: GetPurposeForFund :one
SELECT id, fund_id, kind, name, created_at
FROM purpose
WHERE id = ? AND fund_id = ?;

-- Kas Utama first, then every other pos by name whatever its kind (maintainer,
-- 2026-10-05); every list reads this order. id only breaks a tie.
-- name: ListPurposesByFund :many
SELECT id, fund_id, kind, name, created_at
FROM purpose
WHERE fund_id = ?
ORDER BY kind <> 'main', name COLLATE NOCASE, id;

-- ListPurposesByFund minus closed envelopes (ADR-031): backs the record form's
-- picker, which stops offering what PostTransaction's guard would refuse. The
-- LEFT JOIN treats "not an incidental" and "an open incidental" alike - main
-- and pass_through have no incidental row, so closed_on reads NULL for both.
-- The lifecycle lives in this filter, not on the shared wire shape.
-- name: ListSelectablePurposesByFund :many
SELECT p.id, p.fund_id, p.kind, p.name, p.created_at
FROM purpose p
LEFT JOIN incidental i ON i.purpose_id = p.id
WHERE p.fund_id = ? AND i.closed_on IS NULL
ORDER BY p.kind <> 'main', p.name COLLATE NOCASE, p.id;

-- Renames a purpose; a posted transaction references it by id and nothing in
-- the ledger reads the text. Which purposes may be renamed is
-- Ledger.RenamePurpose's call (policy, not shape). For an incidental this is
-- one half of the pair Ledger.RenameIncidental runs with
-- UpdateIncidentalOccasion, since the occasion is stored on both rows.
-- name: UpdatePurposeName :one
UPDATE purpose
SET name = ?
WHERE id = ?
RETURNING id, fund_id, kind, name, created_at;
