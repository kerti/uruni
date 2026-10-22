-- name: CreateUser :one
INSERT INTO "user" (email, password_hash, created_at)
VALUES (?, ?, ?)
RETURNING id, email, password_hash, created_at;

-- "user".email is COLLATE NOCASE, so `email = x` folds ASCII case by itself. New
-- rows are stored trimmed and lower-cased (auth.NormalizeEmail); an older row
-- keeps its typed case. NOCASE folds ASCII only while Go's lower-casing is full
-- Unicode, so the lookup keeps both the normalized form and the address as typed
-- (trimmed), which finds a legacy row with a non-ASCII capital. user holds one
-- row, so the OR costs no index.
-- name: GetUserByEmail :one
SELECT id, email, password_hash, created_at
FROM "user"
WHERE email = sqlc.arg(normalized) OR email = sqlc.arg(exact);

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
WHERE email = sqlc.arg(normalized) OR email = sqlc.arg(exact)
RETURNING id, email, password_hash, created_at;

-- The backup export's read (ADR-012): the whole table, password hash included
-- - the backup download itself warns the treasurer this file holds the login.
-- user has no fund_id (ADR-030), so nothing to scope by; ordered by id only for
-- deterministic export bytes, a serialization tiebreak, not a semantic order.
-- name: ListUsers :many
SELECT id, email, password_hash, created_at
FROM "user"
ORDER BY id;

-- The account page's reads and writes (#386): the one login, addressed by the
-- id its session carries. An email change stores the normalized form
-- (auth.NormalizeEmail), as register does.
-- name: GetUserByID :one
SELECT id, email, password_hash, created_at
FROM "user"
WHERE id = ?;

-- name: UpdateUserEmail :one
UPDATE "user"
SET email = ?
WHERE id = ?
RETURNING id, email, password_hash, created_at;

-- name: UpdateUserPasswordByID :one
UPDATE "user"
SET password_hash = ?
WHERE id = ?
RETURNING id, email, password_hash, created_at;
