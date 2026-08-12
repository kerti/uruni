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
