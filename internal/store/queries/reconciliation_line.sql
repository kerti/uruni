-- name: CreateReconciliationLine :one
INSERT INTO reconciliation_line (
  fund_id, reconciliation_id, account_id, recorded_amount, actual_amount,
  difference_amount, resolution, adjustment_transaction_id
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, reconciliation_id, account_id, recorded_amount, actual_amount,
          difference_amount, resolution, adjustment_transaction_id;

-- name: ListReconciliationLines :many
SELECT id, fund_id, reconciliation_id, account_id, recorded_amount, actual_amount,
       difference_amount, resolution, adjustment_transaction_id
FROM reconciliation_line
WHERE reconciliation_id = ?
ORDER BY account_id;

-- Whether the fund as a whole came out even. Cast so the trust core gets an
-- int64 rather than interface{} - sqlc's SQLite engine cannot infer the type of
-- a summed expression (ADR-024).
-- name: ReconciliationDifferenceTotal :one
SELECT CAST(COALESCE(SUM(difference_amount), 0) AS INTEGER) AS difference_amount
FROM reconciliation_line
WHERE reconciliation_id = ?;

-- Differences the treasurer chose to sleep on. They are not errors, and the
-- next snapshot is where they get picked up again - a line is never edited.
-- name: ListOpenReconciliationLinesByFund :many
SELECT id, fund_id, reconciliation_id, account_id, recorded_amount, actual_amount,
       difference_amount, resolution, adjustment_transaction_id
FROM reconciliation_line
WHERE fund_id = ? AND resolution = 'left_open'
ORDER BY reconciliation_id, account_id;
