-- name: CreateReconciliationLine :one
INSERT INTO reconciliation_line (
  fund_id, reconciliation_id, account_id, recorded_amount, actual_amount,
  difference_amount, resolution, adjustment_transaction_id
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, reconciliation_id, account_id, recorded_amount, actual_amount,
          difference_amount, resolution, adjustment_transaction_id;

-- remainder_amount (#498) is what a line still has open: the part of its
-- difference its fix did not square, signed like difference_amount
-- (actual - recorded). left_open: the whole difference. matched: 0. adjusted
-- and entry_added: difference - signed(fix), where the fix is the entry the
-- line points at (+ for in, - for out). An entry_added line from before #498
-- points at nothing and reads as closed (0): its remainder was never recorded
-- and cannot be known. The same expression appears in
-- ListOpenReconciliationLinesByFund, ListReconciliationsPage and
-- ReportOpenReconciliationLines - keep the four in step.
--
-- In ListAccountsByFund's order: cash before bank, then by name.
-- name: ListReconciliationLines :many
SELECT rl.id, rl.fund_id, rl.reconciliation_id, rl.account_id, rl.recorded_amount, rl.actual_amount,
       rl.difference_amount, rl.resolution, rl.adjustment_transaction_id,
       CAST(CASE rl.resolution
         WHEN 'left_open' THEN rl.difference_amount
         WHEN 'matched' THEN 0
         ELSE CASE
           WHEN fix.id IS NULL THEN 0
           WHEN fix.direction = 'in' THEN rl.difference_amount - fix.amount
           ELSE rl.difference_amount + fix.amount
         END
       END AS INTEGER) AS remainder_amount
FROM reconciliation_line rl
JOIN account a ON a.id = rl.account_id
LEFT JOIN "transaction" fix ON fix.fund_id = rl.fund_id AND fix.id = rl.adjustment_transaction_id
WHERE rl.reconciliation_id = ?
ORDER BY CASE a.kind WHEN 'cash' THEN 0 ELSE 1 END, a.name COLLATE NOCASE, a.id;

-- The backup export's read (ADR-012): every line across every snapshot, not
-- filtered by resolution or superseded by a later count, since a restore needs
-- the whole frozen history. Ordered by id only for deterministic export bytes.
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

-- Gaps still open: a line whose remainder_amount (see ListReconciliationLines)
-- is non-zero - a left_open line, or an entry_added line whose entry did not
-- square the whole difference (#498) - while nothing later has weighed in on
-- the same location. A later snapshot with any line at all for that account_id
-- supersedes it (matched, entry_added, adjusted or left_open again), since the
-- question is only "has this location been counted since", not how that count
-- came out. Per location, not per snapshot: a later count that skips an
-- account leaves its gap open. "Later" means the owning reconciliation's
-- (performed_at, id), performed_at first. A snapshot is never edited
-- (ADR-024), so a superseded line stays as recorded; it just stops being open.
-- name: ListOpenReconciliationLinesByFund :many
SELECT rl.id, rl.fund_id, rl.reconciliation_id, rl.account_id, rl.recorded_amount,
       rl.actual_amount, rl.difference_amount, rl.resolution, rl.adjustment_transaction_id,
       CAST(CASE rl.resolution
         WHEN 'left_open' THEN rl.difference_amount
         WHEN 'matched' THEN 0
         ELSE CASE
           WHEN fix.id IS NULL THEN 0
           WHEN fix.direction = 'in' THEN rl.difference_amount - fix.amount
           ELSE rl.difference_amount + fix.amount
         END
       END AS INTEGER) AS remainder_amount
FROM reconciliation_line rl
JOIN reconciliation r ON r.fund_id = rl.fund_id AND r.id = rl.reconciliation_id
LEFT JOIN "transaction" fix ON fix.fund_id = rl.fund_id AND fix.id = rl.adjustment_transaction_id
WHERE rl.fund_id = ?
  AND (CASE rl.resolution
         WHEN 'left_open' THEN rl.difference_amount
         WHEN 'matched' THEN 0
         ELSE CASE
           WHEN fix.id IS NULL THEN 0
           WHEN fix.direction = 'in' THEN rl.difference_amount - fix.amount
           ELSE rl.difference_amount + fix.amount
         END
       END) <> 0
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
