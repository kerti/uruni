-- name: CreateSession :one
INSERT INTO session (token, data, expires_at)
VALUES (?, ?, ?)
RETURNING token, data, expires_at;

-- What a session store writes back for a session the request loaded (#601):
-- an UPDATE, never an insert, so a row another actor deleted in the meantime
-- (a restore, a password or email change) stays deleted and the caller reads 0
-- rows affected. A new token goes through CreateSession instead. Being one
-- statement, it also keeps two concurrent requests on one cookie from
-- colliding: a delete-then-insert pair interleaves under ADR-004's
-- SetMaxOpenConns(1) and the second INSERT trips session.token's primary key.
-- name: UpdateSession :execrows
UPDATE session
SET data = ?, expires_at = ?
WHERE token = ?;

-- Only a row still inside its idle window; a session past expires_at is gone
-- even before DeleteExpiredSessions sweeps it.
-- name: GetSession :one
SELECT token, data, expires_at
FROM session
WHERE token = ? AND expires_at > ?;

-- The sliding 30-day idle timeout (#113): every read that proves the session
-- valid pushes expires_at forward by a caller-computed window. No absolute cap.
-- name: TouchSession :one
UPDATE session
SET expires_at = sqlc.arg('new_expires_at')
WHERE token = sqlc.arg('token') AND expires_at > sqlc.arg('not_before')
RETURNING token, data, expires_at;

-- name: DeleteSession :exec
DELETE FROM session
WHERE token = ?;

-- The lazy sweep: a side effect of a session commit, at most hourly (#529),
-- never a background ticker (ADR-013).
-- name: DeleteExpiredSessions :exec
DELETE FROM session
WHERE expires_at <= ?;

-- Signs every device out, for a password reset (#287): the instance holds one
-- login (ADR-030), so "every session" is that account's, and a reset that left
-- an old cookie working would not be a reset.
-- name: DeleteAllSessions :exec
DELETE FROM session;

-- Signs every OTHER device out, for a treasurer changing her own email or
-- password (#386): the session making the change keeps working, the rest stop.
-- The sibling of DeleteAllSessions, which a reset by the operator uses.
-- name: DeleteOtherSessions :exec
DELETE FROM session
WHERE token <> ?;
