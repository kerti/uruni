-- Only a live record answers: an expired one (expires_at = now included) is as
-- good as absent, so correctness never waits on its removal. Written in the same
-- transaction as the rows it covers, by the ledger's idempotency helper (ADR-043).
-- name: GetIdempotencyKey :one
SELECT key, fingerprint, result, created_at, expires_at
FROM idempotency_key
WHERE key = sqlc.arg('key') AND expires_at > sqlc.arg('now');

-- name: CreateIdempotencyKey :exec
INSERT INTO idempotency_key (key, fingerprint, result, created_at, expires_at)
VALUES (?, ?, ?, ?, ?);

-- The purge: a side effect of a keyed write that found no record, in its own
-- transaction (ADR-043), never a ticker (ADR-013). It also clears an expired row
-- of the very key about to be inserted.
-- name: DeleteExpiredIdempotencyKeys :exec
DELETE FROM idempotency_key
WHERE expires_at <= sqlc.arg('now');

-- A restore returns the ledger to an older state, so a key from before it would
-- replay a success whose rows are gone (ADR-043, ADR-012).
-- name: DeleteAllIdempotencyKeys :exec
DELETE FROM idempotency_key;
