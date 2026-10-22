-- name: CreateFund :one
INSERT INTO fund (name, currency, time_zone, report_slug, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, name, currency, time_zone, report_slug, created_at;

-- name: GetFund :one
SELECT id, name, currency, time_zone, report_slug, created_at
FROM fund
WHERE id = ?;

-- The public report's only way in (ADR-030 carve-out, ADR-035): the slug names
-- its fund, with no session. report_slug is UNIQUE: one row or sql.ErrNoRows.
-- name: GetFundByReportSlug :one
SELECT id, name, currency, time_zone, report_slug, created_at
FROM fund
WHERE report_slug = ?;

-- name: ListFunds :many
SELECT id, name, currency, time_zone, report_slug, created_at
FROM fund
ORDER BY id;

-- Renames the fund and/or changes its time zone; a NULL argument leaves that
-- column alone. Neither rewrites anything posted (ADR-041: every day and month is
-- derived from the zone, so setting the old one back restores every earlier
-- reading). The CHECK is the zone's validation. currency (an invariant through
-- 0.x) and report_slug (see UpdateFundReportSlug) are deliberately not settable here.
-- name: UpdateFund :one
UPDATE fund
SET name = coalesce(sqlc.narg('name'), name),
    time_zone = coalesce(sqlc.narg('time_zone'), time_zone)
WHERE id = sqlc.arg('id')
RETURNING id, name, currency, time_zone, report_slug, created_at;

-- Replaces the report's address (ADR-035's leak escape hatch). The old slug is
-- simply gone: no redirect, an ordinary unknown slug from the next read on.
-- name: UpdateFundReportSlug :one
UPDATE fund
SET report_slug = ?
WHERE id = ?
RETURNING id, name, currency, time_zone, report_slug, created_at;
