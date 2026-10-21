-- name: CreateAccount :one
INSERT INTO account (fund_id, kind, name, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, fund_id, kind, name, created_at, inactive_on;

-- Fund-scoped like GetMemberForFund (#188): another fund's account id answers
-- sql.ErrNoRows rather than being found and then rejected.
-- name: GetAccountForFund :one
SELECT id, fund_id, kind, name, created_at, inactive_on
FROM account
WHERE id = ? AND fund_id = ?;

-- Cash before bank, then by name (maintainer, 2026-10-05): the order she
-- counts in. Every list of locations reads this one order. id only breaks a
-- tie between identical names.
-- name: ListAccountsByFund :many
SELECT id, fund_id, kind, name, created_at, inactive_on
FROM account
WHERE fund_id = ?
ORDER BY CASE kind WHEN 'cash' THEN 0 ELSE 1 END, name COLLATE NOCASE, id;

-- A correction to a location's labels, not a ledger event: renaming,
-- re-kinding or retiring an account changes nothing already posted (nothing in
-- internal/ledger branches on kind; the schema's CHECK is its only rule).
-- COALESCE covers the NOT NULL name; inactive_on needs the set_inactive_on
-- flag because a null argument is ambiguous between "leave alone" and
-- "reinstate" (as in UpdateMember).
-- name: UpdateAccount :one
UPDATE account
SET name        = COALESCE(sqlc.narg('name'), name),
    kind        = COALESCE(sqlc.narg('kind'), kind),
    inactive_on = CASE WHEN CAST(sqlc.arg('set_inactive_on') AS INTEGER) = 1 THEN sqlc.narg('inactive_on') ELSE inactive_on END
WHERE id = sqlc.arg('id')
RETURNING id, fund_id, kind, name, created_at, inactive_on;

-- Leans on the composite FKs from "transaction" and reconciliation_line to
-- refuse once real data references the account. For a never-used duplicate
-- only; a used-then-retired account gets inactive_on via UpdateAccount.
-- name: DeleteAccount :exec
DELETE FROM account
WHERE id = ?;

-- Each location's opening-balance date, for the "this date is before the
-- opening balance" hint (#637). Read-only and derived: the opening entry is the
-- ledger's own row, so nothing is stored beside it. MIN because a location
-- could in principle carry more than one opening row; the earliest is the one
-- history starts from. Locations without an opening entry have no row here.
-- name: ListAccountOpeningDates :many
SELECT account_id, CAST(MIN(occurred_on) AS TEXT) AS opening_on
FROM "transaction"
WHERE fund_id = ? AND kind = 'opening'
GROUP BY account_id;
