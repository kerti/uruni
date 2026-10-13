-- name: CreateFund :one
INSERT INTO fund (name, currency, report_slug, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, name, currency, report_slug, created_at;

-- name: GetFund :one
SELECT id, name, currency, report_slug, created_at
FROM fund
WHERE id = ?;

-- The public report's only way in (ADR-030 carve-out, ADR-035): the slug names
-- its fund, with no session. report_slug is UNIQUE: one row or sql.ErrNoRows.
-- name: GetFundByReportSlug :one
SELECT id, name, currency, report_slug, created_at
FROM fund
WHERE report_slug = ?;

-- name: ListFunds :many
SELECT id, name, currency, report_slug, created_at
FROM fund
ORDER BY id;

-- Renames the fund. name is a display label nothing posted references, so this
-- changes no history. currency (an invariant through 0.x) and report_slug (the
-- report's unguessable address; see UpdateFundReportSlug) are deliberately not
-- settable here.
-- name: UpdateFund :one
UPDATE fund
SET name = ?
WHERE id = ?
RETURNING id, name, currency, report_slug, created_at;

-- Replaces the report's address (ADR-035's leak escape hatch). The old slug is
-- simply gone: no redirect, an ordinary unknown slug from the next read on.
-- name: UpdateFundReportSlug :one
UPDATE fund
SET report_slug = ?
WHERE id = ?
RETURNING id, name, currency, report_slug, created_at;
