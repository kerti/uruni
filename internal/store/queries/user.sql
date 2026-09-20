-- name: CreateUser :one
INSERT INTO "user" (email, password_hash, created_at)
VALUES (?, ?, ?)
RETURNING id, email, password_hash, created_at;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, created_at
FROM "user"
WHERE email = ?;

-- CountUsers is how register (#114) knows whether the one-shot bootstrap
-- account already exists (ADR-030 decision 2): the gate is the count, not a
-- duplicate-email collision.
-- name: CountUsers :one
SELECT CAST(COUNT(*) AS INTEGER) AS user_count
FROM "user";

-- UpdateUserPassword is create-user's reset half (#287): the email is the
-- key, and the hash is computed by internal/auth before the call.
-- name: UpdateUserPassword :one
UPDATE "user"
SET password_hash = ?
WHERE email = ?
RETURNING id, email, password_hash, created_at;
