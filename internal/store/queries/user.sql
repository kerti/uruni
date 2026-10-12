-- name: CreateUser :one
INSERT INTO "user" (email, password_hash, created_at)
VALUES (?, ?, ?)
RETURNING id, email, password_hash, created_at;

-- GetUserByEmail finds the login case-insensitively without a schema change
-- (#500). New rows are stored trimmed and lower-cased (auth.NormalizeEmail),
-- but a row written before that keeps whatever case it was typed in, so the
-- lookup matches either the normalized form against lower(email) or the
-- address exactly as stored. SQLite's lower() folds ASCII only: the exact arm
-- is what keeps a legacy row with a non-ASCII capital findable by the very
-- text it was registered with. The COLLATE NOCASE column rides the schema
-- epic (#523); user holds one row, so no index is lost to the OR.
-- name: GetUserByEmail :one
SELECT id, email, password_hash, created_at
FROM "user"
WHERE lower(email) = sqlc.arg(normalized) OR email = sqlc.arg(exact);

-- CountUsers is how register (#114) knows whether the one-shot bootstrap
-- account already exists (ADR-030 decision 2): the gate is the count, not a
-- duplicate-email collision.
-- name: CountUsers :one
SELECT CAST(COUNT(*) AS INTEGER) AS user_count
FROM "user";

-- UpdateUserPassword is create-user's reset half (#287): the email is the
-- key (matched as GetUserByEmail does, #500), and the hash is computed by
-- internal/auth before the call.
-- name: UpdateUserPassword :one
UPDATE "user"
SET password_hash = sqlc.arg(password_hash)
WHERE lower(email) = sqlc.arg(normalized) OR email = sqlc.arg(exact)
RETURNING id, email, password_hash, created_at;

-- ListUsers is the backup export's own read (ADR-012, #323): the whole
-- table, password hash included - the backup download itself is what
-- warns the treasurer this file holds the login. user carries no fund_id
-- (ADR-030), so there is nothing to scope this by; ordered by id purely for
-- the export's own deterministic byte order (CLAUDE.md's "no primary-key
-- order" binds a semantic guarantee, not a serialization tiebreak).
-- name: ListUsers :many
SELECT id, email, password_hash, created_at
FROM "user"
ORDER BY id;
