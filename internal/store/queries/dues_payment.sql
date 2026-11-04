-- The header PostDuesPayments inserts before a payment's rows (ADR-048). It
-- holds nothing but its fund and its time; the rows carry everything else.
-- name: CreateDuesPayment :one
INSERT INTO dues_payment (fund_id, created_at)
VALUES (?, ?)
RETURNING id, fund_id, created_at;

-- name: ListDuesPaymentsByFund :many
SELECT id, fund_id, created_at
FROM dues_payment
WHERE fund_id = ?
ORDER BY id;

-- Every dues row of one payment, oldest month first (the anchor row is the
-- first, ADR-048), with what a whole-payment reversal needs to decide each row
-- (the same facts as GetReversalTarget). Fund-scoped: another fund's payment
-- reads as no rows.
-- name: ListDuesPaymentReversalTargets :many
SELECT t.id, t.fund_id, t.account_id, t.purpose_id, t.direction, t.amount, t.occurred_on, t.kind,
       t.member_id, t.dues_period, t.reimbursement_id, t.transfer_id, t.dues_payment_id, t.reverses_transaction_id,
       t.note, t.created_at,
       t.effective_purpose_id,
       t.reversed,
       CAST(EXISTS (SELECT 1 FROM reconciliation_line rl
                    WHERE rl.fund_id = t.fund_id AND rl.adjustment_transaction_id = t.id) AS INTEGER) AS is_reconciliation_fix,
       ei.closed_on AS effective_closed_on,
       CAST(EXISTS (SELECT 1 FROM reconciliation rc
                    WHERE rc.fund_id = t.fund_id AND rc.through_transaction_id >= t.id) AS INTEGER) AS counted
FROM ledger_entry t
LEFT JOIN incidental ei ON ei.purpose_id = t.effective_purpose_id
WHERE t.fund_id = ? AND t.dues_payment_id = ?
ORDER BY t.dues_period, t.id;

-- Every row of the dues payments a list page shows, for the group items of
-- GET /api/transactions (ADR-048): the payments' own dues rows AND the reversal
-- rows that undo them, each tagged with the payment it belongs to
-- (payment_id: the row's own, or - on a reversal - its original's). The Go side
-- builds the periods of a payment item from the first and of a reversal group
-- from the second (grouped by payment and date), and decides the payment's
-- reversibility from the same facts as GetReversalTarget. A reversal of a
-- non-dues row has no payment and is never returned. Fund-scoped; the order is
-- the Go side's job (it sorts by month), not an id order.
-- name: ListDuesGroupRows :many
WITH payment_rows AS (
  SELECT x.id, x.dues_payment_id AS payment_id
  FROM "transaction" x
  WHERE x.fund_id = sqlc.arg('fund_id') AND x.dues_payment_id IN (sqlc.slice('payment_ids'))
)
SELECT t.id, t.kind, t.occurred_on, t.dues_period, t.amount,
       pr.payment_id,
       t.reversed,
       CAST(EXISTS (SELECT 1 FROM reconciliation_line rl
                    WHERE rl.fund_id = t.fund_id AND rl.adjustment_transaction_id = t.id) AS INTEGER) AS is_reconciliation_fix,
       ei.closed_on AS effective_closed_on,
       CAST(EXISTS (SELECT 1 FROM reconciliation rc
                    WHERE rc.fund_id = t.fund_id AND rc.through_transaction_id >= t.id) AS INTEGER) AS counted
FROM ledger_entry t
LEFT JOIN "transaction" o ON o.id = t.reverses_transaction_id
JOIN payment_rows pr ON pr.id = t.id OR pr.id = o.id
LEFT JOIN incidental ei ON ei.purpose_id = t.effective_purpose_id;
