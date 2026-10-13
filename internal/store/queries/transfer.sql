-- name: CreateTransfer :one
-- corrects_transaction_id is nil for every transfer but a purpose correction
-- (ADR-033); the schema's CHECK requires NULL of anything not
-- kind='reclass_purpose'. reason (ADR-036) is 'roll' for
-- CloseIncidentalAndRoll, 'allocation' for a purpose move, nil for every other
-- pair - the CHECK holds it to a reclass_purpose pair that corrects nothing.
INSERT INTO transfer (fund_id, kind, corrects_transaction_id, reason, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, fund_id, kind, corrects_transaction_id, reason, created_at;

-- name: GetTransfer :one
SELECT id, fund_id, kind, corrects_transaction_id, reason, created_at
FROM transfer
WHERE id = ?;

-- name: ListTransfersByFund :many
SELECT id, fund_id, kind, corrects_transaction_id, reason, created_at
FROM transfer
WHERE fund_id = ?
ORDER BY id;

-- The effective peruntukan (ADR-033, #411): the tag this row's money is under
-- now. Each correction moves the whole row to a new tag, so its corrections
-- form a path starting at the stored purpose_id; the tag it ends on is the one
-- the money entered once more than it left, counting the stored tag as the
-- start. That needs no ordering of corrections (transfer ids are not an order
-- to rely on), and a row corrected and then corrected back ends where it began.
--
-- Which leg is "entering" depends on the row's direction: PostPurposeCorrection
-- puts the target on the leg moving the same way as the row, so
-- leg.direction = t.direction reads back what it wrote. A row nothing corrects
-- answers its own purpose_id.
-- name: EffectivePurposeForTransaction :one
SELECT CAST(COALESCE((
  SELECT leg.purpose_id
  FROM transfer c
  JOIN "transaction" leg ON leg.transfer_id = c.id
  WHERE c.corrects_transaction_id = t.id
  GROUP BY leg.purpose_id
  HAVING SUM(CASE WHEN leg.direction = t.direction THEN 1 ELSE -1 END)
         + (leg.purpose_id = t.purpose_id) = 1
  LIMIT 1
), t.purpose_id) AS INTEGER) AS effective_purpose_id
FROM "transaction" t
WHERE t.fund_id = ? AND t.id = ?;
