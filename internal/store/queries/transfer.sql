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
-- now. Defined once as ledger_entry.effective_purpose_id (schema file); this
-- only reads it. A row nothing corrects answers its own purpose_id.
-- name: EffectivePurposeForTransaction :one
SELECT effective_purpose_id
FROM ledger_entry
WHERE fund_id = ? AND id = ?;
