-- name: CreateReimbursement :one
INSERT INTO reimbursement (fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at;

-- name: GetReimbursement :one
SELECT id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at
FROM reimbursement
WHERE id = ?;

-- name: ListReimbursementsByFund :many
SELECT id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at
FROM reimbursement
WHERE fund_id = ?
ORDER BY id;

-- What the fund still owes its members: neither settled by a payout nor
-- waived. Both halves are conditions SQLite cannot express as a CHECK across
-- tables, so the settle path filters on them here instead.
-- name: ListOutstandingReimbursementsByFund :many
SELECT r.id, r.fund_id, r.member_id, r.purpose_id, r.amount, r.incurred_on, r.waived_on, r.note, r.created_at
FROM reimbursement r
WHERE r.fund_id = ?
  AND r.waived_on IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM "transaction" t
    WHERE t.reimbursement_id = r.id AND t.kind = 'reimbursement'
  )
ORDER BY r.id;

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
