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

-- UpdateMember is a correction to reference data (issue #81), not a new
-- ledger event: a typo in name, a tier reassignment, or the two nullable
-- dates. name is COALESCE'd - a member's name is NOT NULL, so "leave alone"
-- is the only thing a nil argument can mean. tier_id, joined_on and
-- inactive_on are each nullable columns where "clear it" is a real, distinct
-- request from "leave alone" (a null sqlc.narg cannot say which), so each
-- gets its own set_* flag: the CASE only substitutes the new value - which
-- may itself be NULL - when the caller actually sent that field.
-- name: UpdateMember :one
UPDATE member
SET name        = COALESCE(sqlc.narg('name'), name),
    tier_id     = CASE WHEN CAST(sqlc.arg('set_tier_id')     AS INTEGER) = 1 THEN sqlc.narg('tier_id')     ELSE tier_id     END,
    joined_on   = CASE WHEN CAST(sqlc.arg('set_joined_on')   AS INTEGER) = 1 THEN sqlc.narg('joined_on')   ELSE joined_on   END,
    inactive_on = CASE WHEN CAST(sqlc.arg('set_inactive_on') AS INTEGER) = 1 THEN sqlc.narg('inactive_on') ELSE inactive_on END
WHERE id = sqlc.arg('id')
RETURNING id, fund_id, name, tier_id, joined_on, inactive_on, created_at;

-- DeleteMember relies on the composite foreign keys from "transaction" and
-- reimbursement to refuse this once a real row references the member (issue
-- #81) - no pre-check here, that would only race the constraint the schema
-- already enforces.
-- name: DeleteMember :exec
DELETE FROM member
WHERE id = ?;
