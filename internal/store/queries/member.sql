-- name: CreateMember :one
INSERT INTO member (fund_id, name, tier_id, joined_on, inactive_on, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, name, tier_id, joined_on, inactive_on, created_at;

-- name: GetMember :one
SELECT id, fund_id, name, tier_id, joined_on, inactive_on, created_at
FROM member
WHERE id = ?;

-- name: ListMembersByFund :many
SELECT id, fund_id, name, tier_id, joined_on, inactive_on, created_at
FROM member
WHERE fund_id = ?
ORDER BY id;

-- UpdateMember is a correction to reference data, not a ledger event. name
-- is NOT NULL, so COALESCE covers it: a nil argument can only mean "leave
-- alone". The three nullable columns need the set_* flags, because there a
-- null argument is ambiguous between "leave alone" and "clear it" - the CASE
-- substitutes the new value, NULL included, only when the caller sent it.
-- name: UpdateMember :one
UPDATE member
SET name        = COALESCE(sqlc.narg('name'), name),
    tier_id     = CASE WHEN CAST(sqlc.arg('set_tier_id')     AS INTEGER) = 1 THEN sqlc.narg('tier_id')     ELSE tier_id     END,
    joined_on   = CASE WHEN CAST(sqlc.arg('set_joined_on')   AS INTEGER) = 1 THEN sqlc.narg('joined_on')   ELSE joined_on   END,
    inactive_on = CASE WHEN CAST(sqlc.arg('set_inactive_on') AS INTEGER) = 1 THEN sqlc.narg('inactive_on') ELSE inactive_on END
WHERE id = sqlc.arg('id')
RETURNING id, fund_id, name, tier_id, joined_on, inactive_on, created_at;

-- DeleteMember leans on the composite foreign keys from "transaction" and
-- reimbursement to refuse it once a real row references the member.
-- name: DeleteMember :exec
DELETE FROM member
WHERE id = ?;
