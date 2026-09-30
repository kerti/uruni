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

-- ListReconciliationLinesByFund is the backup export's own read (ADR-012,
-- #323): every line across every snapshot, matched or not - unlike
-- ListOpenReconciliationLinesByFund above, nothing here is filtered by
-- resolution or superseded by a later count, since a restore needs the
-- whole frozen history back, not just what is still open today. Ordered by
-- id purely for the export's own deterministic byte order.
-- name: ListReconciliationLinesByFund :many
SELECT id, fund_id, reconciliation_id, account_id, recorded_amount, actual_amount,
       difference_amount, resolution, adjustment_transaction_id
FROM reconciliation_line
WHERE fund_id = ?
ORDER BY id;

-- Whether the fund as a whole came out even. Cast so the trust core gets an
-- int64 rather than interface{} - sqlc's SQLite engine cannot infer the type of
-- a summed expression (ADR-024).
-- name: ReconciliationDifferenceTotal :one
SELECT CAST(COALESCE(SUM(difference_amount), 0) AS INTEGER) AS difference_amount
FROM reconciliation_line
WHERE reconciliation_id = ?;

-- Differences the treasurer chose to sleep on, but only while nothing later
-- has weighed in on the same location. A left_open line stops counting as
-- open the moment a later snapshot has any line at all for that account_id -
-- matched, entry_added, adjusted, or left_open again all supersede it, since
-- the question is only "has this location been counted since", not how that
-- count came out. This is per location, not per snapshot: a later count that
-- skips an account leaves that account's gap open regardless of what else the
-- later snapshot resolved. "Later" means the owning reconciliation's
-- (performed_at, id), performed_at first - a snapshot is never edited
-- (ADR-024), so a superseded line stays in the table exactly as recorded; it
-- just stops being "open".
-- name: ListOpenReconciliationLinesByFund :many
SELECT rl.id, rl.fund_id, rl.reconciliation_id, rl.account_id, rl.recorded_amount,
       rl.actual_amount, rl.difference_amount, rl.resolution, rl.adjustment_transaction_id
FROM reconciliation_line rl
JOIN reconciliation r ON r.fund_id = rl.fund_id AND r.id = rl.reconciliation_id
WHERE rl.fund_id = ?
  AND rl.resolution = 'left_open'
  AND NOT EXISTS (
    SELECT 1
    FROM reconciliation_line rl2
    JOIN reconciliation r2 ON r2.fund_id = rl2.fund_id AND r2.id = rl2.reconciliation_id
    WHERE rl2.fund_id = rl.fund_id
      AND rl2.account_id = rl.account_id
      AND (r2.performed_at > r.performed_at
           OR (r2.performed_at = r.performed_at AND r2.id > r.id))
  )
ORDER BY r.performed_at, r.id, rl.account_id;
