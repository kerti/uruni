-- Only a live record answers: an expired one is as good as absent, so
-- correctness never waits on its removal. expires_at = now is expired. The
-- records are written in the same transaction as the rows they cover, by the
-- ledger's idempotency helper (ADR-043); never by a handler.
-- name: GetIdempotencyKey :one
SELECT key, fingerprint, result, created_at, expires_at
FROM idempotency_key
WHERE key = sqlc.arg('key') AND expires_at > sqlc.arg('now');

-- name: CreateIdempotencyKey :exec
INSERT INTO idempotency_key (key, fingerprint, result, created_at, expires_at)
VALUES (?, ?, ?, ?, ?);

-- The purge: a side effect of a keyed write that found no record, in that
-- write's own transaction (ADR-043), never a background ticker (ADR-013). It
-- also clears an expired row of the very key about to be inserted.
-- name: DeleteExpiredIdempotencyKeys :exec
DELETE FROM idempotency_key
WHERE expires_at <= sqlc.arg('now');

-- A restore returns the ledger to an older state, so a key from before it would
-- replay a success whose rows are gone (ADR-043, ADR-012).
-- name: DeleteAllIdempotencyKeys :exec
DELETE FROM idempotency_key;
