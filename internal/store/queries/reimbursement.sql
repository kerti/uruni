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
