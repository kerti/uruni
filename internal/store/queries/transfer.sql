-- name: CreateTransfer :one
-- corrects_transaction_id is nil for every transfer but a purpose
-- correction (ADR-033) - between_accounts and CloseIncidentalAndRoll's own
-- reclass_purpose rolls both pass nil, the same NULL the schema's CHECK
-- requires of anything that is not kind='reclass_purpose'.
INSERT INTO transfer (fund_id, kind, corrects_transaction_id, created_at)
VALUES (?, ?, ?, ?)
RETURNING id, fund_id, kind, corrects_transaction_id, created_at;

-- name: GetTransfer :one
SELECT id, fund_id, kind, corrects_transaction_id, created_at
FROM transfer
WHERE id = ?;

-- name: ListTransfersByFund :many
SELECT id, fund_id, kind, corrects_transaction_id, created_at
FROM transfer
WHERE fund_id = ?
ORDER BY id;

-- The effective peruntukan lookup (ADR-033): the LATEST correction pointing
-- at transaction_id, both its legs' purpose ids, or sql.ErrNoRows when none
-- exists - the caller then falls back to the original row's own stored
-- purpose_id. "Latest" is transfer.id DESC: a correction's two legs share
-- one transfer row inserted once, so transfer.id already orders corrections
-- the same way occurred_on cannot (ADR-033's own date-is-not-a-field rule
-- means every correction of the same row can share a date).
--
-- Both legs, not just one: which leg is "the new tag" depends on the
-- ORIGINAL row's own direction, not on 'out' vs 'in' here - PostPurposeCorrection's
-- own doc comment works out why an 'out' original needs the target at its
-- 'out' leg while an 'in' original needs it at its 'in' leg. Deciding that
-- in SQL would mean joining back to the original row a second time for a
-- fact the caller already has in hand from its own first fetch; the caller
-- (effectivePeruntukan) picks the correct one instead.
-- name: LatestPurposeCorrectionForTransaction :one
SELECT ol.purpose_id AS out_purpose_id, il.purpose_id AS in_purpose_id
FROM transfer tr
JOIN "transaction" ol ON ol.transfer_id = tr.id AND ol.direction = 'out'
JOIN "transaction" il ON il.transfer_id = tr.id AND il.direction = 'in'
WHERE tr.fund_id = ? AND tr.corrects_transaction_id = ?
ORDER BY tr.id DESC
LIMIT 1;
