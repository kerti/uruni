-- name: CreateSession :one
INSERT INTO session (token, data, expires_at)
VALUES (?, ?, ?)
RETURNING token, data, expires_at;

-- GetSession only returns a row still inside its idle window; a session past
-- expires_at is functionally gone even before DeleteExpiredSessions sweeps it.
-- name: GetSession :one
SELECT token, data, expires_at
FROM session
WHERE token = ? AND expires_at > ?;

-- TouchSession is the sliding 30-day idle timeout (#113): every read that
-- proves the session still valid pushes expires_at forward by the same fixed
-- window, computed by the caller - there is no absolute cap to enforce here.
-- name: TouchSession :one
UPDATE session
SET expires_at = sqlc.arg('new_expires_at')
WHERE token = sqlc.arg('token') AND expires_at > sqlc.arg('not_before')
RETURNING token, data, expires_at;

-- name: DeleteSession :exec
DELETE FROM session
WHERE token = ?;

-- DeleteExpiredSessions is the lazy sweep: called as a side effect of a
-- session write, never by a background ticker (ADR-013's scope stays
-- untouched by this slice).
-- name: DeleteExpiredSessions :exec
DELETE FROM session
WHERE expires_at <= ?;
