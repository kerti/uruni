-- name: CreateReimbursement :one
INSERT INTO reimbursement (fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at;

-- Fund-scoped: PRD section 6 allows a server to hold more than one fund, so a
-- bare lookup by id would be a cross-fund read.
-- name: GetReimbursement :one
SELECT id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at
FROM reimbursement
WHERE id = ? AND fund_id = ?;

-- One claim with its settled fact, for GET /api/reimbursements/{id}: the settle
-- and correct screens must know whether it is still owed. Fund-scoped.
-- name: GetReimbursementWithSettled :one
SELECT r.id, r.fund_id, r.member_id, r.purpose_id, r.amount, r.incurred_on, r.waived_on, r.note, r.created_at,
  CAST(EXISTS(
    SELECT 1 FROM "transaction" t
    WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
  ) AS INTEGER) AS settled
FROM reimbursement r
WHERE r.id = ? AND r.fund_id = ?;

-- name: ListReimbursementsByFund :many
SELECT r.id, r.fund_id, r.member_id, r.purpose_id, r.amount, r.incurred_on, r.waived_on, r.note, r.created_at,
  CAST(EXISTS(
    SELECT 1 FROM "transaction" t
    WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
  ) AS INTEGER) AS settled
FROM reimbursement r
WHERE r.fund_id = ?
ORDER BY r.id;

-- What the fund still owes its members: neither settled by a payout nor waived.
-- A CHECK cannot span tables, so the settle path filters here instead.
-- settled is a literal 0 (unsettled by construction), keeping the wire shape
-- uniform with the full list.
-- name: ListOutstandingReimbursementsByFund :many
SELECT r.id, r.fund_id, r.member_id, r.purpose_id, r.amount, r.incurred_on, r.waived_on, r.note, r.created_at,
  0 AS settled
FROM reimbursement r
WHERE r.fund_id = ?
  AND r.waived_on IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM "transaction" t
    WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
  )
ORDER BY r.id;

-- GET /api/reimbursements (#226, ADR-032): newest-first, keyset-paged on
-- (incurred_on, id) like ListTransactionsPage (transaction.sql has the
-- reasoning for row-value keyset, the CAST on cursor_id, and INSTR/LOWER over
-- "COLLATE NOCASE LIKE ... ESCAPE" - the sqlc 1.31.1 repeated-ESCAPE bug
-- applies here too, since q searches two columns).
--
-- Search (?q=) covers member name and note only (ADR-032, PRD section 7.4).
--
-- outstanding_only applies the same two conditions as
-- ListOutstandingReimbursementsByFund (waived_on IS NULL, no settling
-- transaction), so `settled` is a literal 0 in that mode (unsettled by
-- construction) and the real EXISTS check otherwise.
--
-- page_limit is page size + 1: peek at one extra row.
-- name: ListReimbursementsPage :many
SELECT r.id, r.fund_id, r.member_id, r.purpose_id, r.amount, r.incurred_on, r.waived_on, r.note, r.created_at,
  CASE WHEN CAST(sqlc.arg('outstanding_only') AS INTEGER) = 1 THEN 0 ELSE
    CAST(EXISTS(
      SELECT 1 FROM "transaction" t
      WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
    ) AS INTEGER)
  END AS settled
FROM reimbursement r
LEFT JOIN member m ON m.id = r.member_id
WHERE r.fund_id = sqlc.arg('fund_id')
  AND (
    sqlc.narg('cursor_incurred_on') IS NULL
    OR (r.incurred_on, r.id) < (sqlc.narg('cursor_incurred_on'), CAST(sqlc.narg('cursor_id') AS INTEGER))
  )
  AND (
    sqlc.narg('q') IS NULL
    OR INSTR(LOWER(r.note), LOWER(sqlc.narg('q'))) > 0
    OR INSTR(LOWER(m.name), LOWER(sqlc.narg('q'))) > 0
  )
  AND (
    CAST(sqlc.arg('outstanding_only') AS INTEGER) = 0
    OR (
      r.waived_on IS NULL
      AND NOT EXISTS (
        SELECT 1 FROM "transaction" t
        WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
      )
    )
  )
ORDER BY r.incurred_on DESC, r.id DESC
LIMIT sqlc.arg('page_limit');

-- The outstanding total, cast so it lands as int64 rather than interface{}:
-- sqlc's SQLite engine cannot infer the type of a summed expression (ADR-024).
-- name: OutstandingReimbursementTotal :one
SELECT CAST(COALESCE(SUM(r.amount), 0) AS INTEGER) AS total_amount
FROM reimbursement r
WHERE r.fund_id = ?
  AND r.waived_on IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM "transaction" t
    WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
  );

-- Corrects a claim not yet settled. A claim is off the ledger until settled
-- (ADR-024), so this is an UPDATE, not an adjusting entry; the settled check
-- lives in internal/ledger because it reads another table.
--
-- waived_on rides on the same statement, which makes un-waiving free. Same
-- COALESCE/set_* split as UpdateMember: the four NOT NULL columns cannot mean
-- "clear it", the two nullable ones can, and only a set_* flag tells that from
-- "leave alone".
-- name: UpdateReimbursement :one
UPDATE reimbursement
SET member_id   = COALESCE(sqlc.narg('member_id'), member_id),
    purpose_id  = COALESCE(sqlc.narg('purpose_id'), purpose_id),
    amount      = COALESCE(sqlc.narg('amount'), amount),
    incurred_on = COALESCE(sqlc.narg('incurred_on'), incurred_on),
    note        = CASE WHEN CAST(sqlc.arg('set_note')      AS INTEGER) = 1 THEN sqlc.narg('note')      ELSE note      END,
    waived_on   = CASE WHEN CAST(sqlc.arg('set_waived_on') AS INTEGER) = 1 THEN sqlc.narg('waived_on') ELSE waived_on END
WHERE id = sqlc.arg('id')
RETURNING id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at;

-- Removes a claim that should never have existed. Leans on receipt's composite
-- FK to refuse a claim with a photo attached; the settled check is
-- internal/ledger's.
-- name: DeleteReimbursement :exec
DELETE FROM reimbursement
WHERE id = ?;
