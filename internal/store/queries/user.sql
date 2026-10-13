-- name: CreateUser :one
INSERT INTO "user" (email, password_hash, created_at)
VALUES (?, ?, ?)
RETURNING id, email, password_hash, created_at;

-- Finds the login case-insensitively without a schema change (#500). New rows
-- are stored trimmed and lower-cased (auth.NormalizeEmail), but an older row
-- keeps whatever case it was typed in, so the lookup matches the normalized
-- form against lower(email) or the address exactly as stored. SQLite's lower()
-- folds ASCII only: the exact arm keeps a legacy row with a non-ASCII capital
-- findable. The COLLATE NOCASE column rides the schema epic (#523); user holds
-- one row, so the OR costs no index.
-- name: GetUserByEmail :one
SELECT id, email, password_hash, created_at
FROM "user"
WHERE lower(email) = sqlc.arg(normalized) OR email = sqlc.arg(exact);

-- How register (#114) knows whether the one-shot bootstrap account exists
-- (ADR-030): the gate is the count, not a duplicate-email collision.
-- name: CountUsers :one
SELECT CAST(COUNT(*) AS INTEGER) AS user_count
FROM "user";

-- create-user's reset half (#287): the email is the key, matched as
-- GetUserByEmail does (#500); the hash is computed by internal/auth.
-- name: UpdateUserPassword :one
UPDATE "user"
SET password_hash = sqlc.arg(password_hash)
WHERE lower(email) = sqlc.arg(normalized) OR email = sqlc.arg(exact)
RETURNING id, email, password_hash, created_at;

-- The backup export's read (ADR-012): the whole table, password hash included
-- - the backup download itself warns the treasurer this file holds the login.
-- user has no fund_id (ADR-030), so nothing to scope by; ordered by id only for
-- deterministic export bytes, a serialization tiebreak, not a semantic order.
-- name: ListUsers :many
SELECT id, email, password_hash, created_at
FROM "user"
ORDER BY id;
