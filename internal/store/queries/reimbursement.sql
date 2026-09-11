-- name: CreateReimbursement :one
INSERT INTO reimbursement (fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at;

-- Fund-scoped on purpose: an id names a row, it does not prove the caller may
-- see it. PRD section 6 allows a server to hold more than one fund, so a bare
-- lookup by id alone would be a cross-fund read the moment a second fund
-- exists.
-- name: GetReimbursement :one
SELECT id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at
FROM reimbursement
WHERE id = ? AND fund_id = ?;

-- name: ListReimbursementsByFund :many
SELECT r.id, r.fund_id, r.member_id, r.purpose_id, r.amount, r.incurred_on, r.waived_on, r.note, r.created_at,
  CAST(EXISTS(
    SELECT 1 FROM "transaction" t
    WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
  ) AS INTEGER) AS settled
FROM reimbursement r
WHERE r.fund_id = ?
ORDER BY r.id;

-- What the fund still owes its members: neither settled by a payout nor
-- waived. Both halves are conditions SQLite cannot express as a CHECK across
-- tables, so the settle path filters on them here instead.
--
-- settled is a literal 0: every row this list returns is unsettled by
-- construction, and the wire shape must stay uniform with the full list.
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

-- GET /api/reimbursements's real listing (#226, ADR-032 "Lists: paging and
-- search") - newest-first and keyset-paged on (incurred_on, id), the same
-- shape as ListTransactionsPage (transaction.sql has the full reasoning for
-- row-value keyset over LIMIT/OFFSET, the CAST on cursor_id, and INSTR/LOWER
-- over "COLLATE NOCASE LIKE ... ESCAPE" - sqlc 1.31.1's bug with a repeated
-- ESCAPE clause applies here too, since q searches two columns).
--
-- Search (?q=) covers member name and note only - PRD section 7.4 gives a
-- claim no purpose-name search surface the way a transaction has one, and
-- ADR-032 named "member name and note" for this list specifically.
--
-- sqlc.narg('outstanding_only') gates the same pair of conditions
-- ListOutstandingReimbursementsByFund applies (waived_on IS NULL and no
-- settling transaction) so `settled` stays correctly computed in both
-- modes: literal 0 when outstanding_only narrows the WHERE clause itself
-- (every row satisfying it is unsettled by construction, same as that
-- query), the real EXISTS check otherwise.
--
-- page_limit is page size + 1, the same "peek at one extra row" trick
-- ListTransactionsPage uses to know whether a next page exists.
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
-- sqlc's SQLite engine cannot infer the type of a summed expression, and an
-- uncast aggregate is a silent failure that only surfaces at M3 (ADR-024).
-- name: OutstandingReimbursementTotal :one
SELECT CAST(COALESCE(SUM(r.amount), 0) AS INTEGER) AS total_amount
FROM reimbursement r
WHERE r.fund_id = ?
  AND r.waived_on IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM "transaction" t
    WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
  );

-- UpdateReimbursement corrects a claim that has not been settled yet - a
-- wrong amount, the wrong member, or the day it was actually spent. The
-- claim is off the ledger until it is settled (ADR-024), so this is an
-- UPDATE and not an adjusting entry; the settled check that keeps it that
-- way lives in internal/ledger, because it reads another table.
--
-- waived_on rides on the same statement rather than a route of its own:
-- waiving is one column, and pairing it with the ordinary correction is
-- what makes un-waiving free. Same COALESCE/set_* split as UpdateMember -
-- the four NOT NULL columns cannot mean "clear it", the two nullable ones
-- can, and only a set_* flag can tell that from "leave alone".
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

-- DeleteReimbursement removes a claim that should never have existed. It
-- leans on receipt's composite foreign key to refuse a claim that still has
-- a photo attached, the same way DeleteMember leans on its referencing
-- tables; the settled check is internal/ledger's, above.
-- name: DeleteReimbursement :exec
DELETE FROM reimbursement
WHERE id = ?;
