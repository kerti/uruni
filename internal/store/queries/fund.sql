-- name: CreateFund :one
INSERT INTO fund (name, currency, report_slug, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, name, currency, report_slug, created_at;

-- name: GetFund :one
SELECT id, name, currency, report_slug, created_at
FROM fund
WHERE id = ?;

-- GetFundByReportSlug is the public report's only way in (ADR-030's
-- carve-out, ADR-035): the slug names its fund, with no session involved.
-- report_slug is UNIQUE, so this is one row or sql.ErrNoRows.
-- name: GetFundByReportSlug :one
SELECT id, name, currency, report_slug, created_at
FROM fund
WHERE report_slug = ?;

-- name: ListFunds :many
SELECT id, name, currency, report_slug, created_at
FROM fund
ORDER BY id;

-- UpdateFund renames the fund. name is a display label - it heads every
-- screen and the public report - and nothing posted references it, so this
-- changes no history (the same reasoning UpdateAccount's own rename rests
-- on). currency and report_slug are deliberately not settable here: one is
-- an invariant through 0.x, the other is the report's unguessable address
-- (which only UpdateFundReportSlug changes).
-- name: UpdateFund :one
UPDATE fund
SET name = ?
WHERE id = ?
RETURNING id, name, currency, report_slug, created_at;

-- UpdateFundReportSlug replaces the report's address (ADR-035's leak escape
-- hatch). The old slug is simply gone: no history, no redirect, so it is an
-- ordinary unknown slug to GetFundByReportSlug from the next read on.
-- name: UpdateFundReportSlug :one
UPDATE fund
SET report_slug = ?
WHERE id = ?
RETURNING id, name, currency, report_slug, created_at;
