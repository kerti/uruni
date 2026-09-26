-- name: CreateReceipt :one
INSERT INTO receipt (fund_id, transaction_id, reimbursement_id, path, uploaded_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, fund_id, transaction_id, reimbursement_id, path, uploaded_at;

-- name: ListReceiptsByTransaction :many
SELECT id, fund_id, transaction_id, reimbursement_id, path, uploaded_at
FROM receipt
WHERE transaction_id = ?
ORDER BY id;

-- name: ListReceiptsByReimbursement :many
SELECT id, fund_id, transaction_id, reimbursement_id, path, uploaded_at
FROM receipt
WHERE reimbursement_id = ?
ORDER BY id;

-- Fund-scoped fetch, the same shape as GetAccountForFund/GetReimbursement:
-- an id names a row, not permission to see it - and a receipt is the one
-- resource this API serves as raw bytes, so this is the only gate between a
-- session and someone else's photo (#153).
-- name: GetReceiptForFund :one
SELECT id, fund_id, transaction_id, reimbursement_id, path, uploaded_at
FROM receipt
WHERE id = ? AND fund_id = ?;

-- Batched, fund-scoped lookup for a whole page of rows at once (#154) - one
-- query per list response, never one per row (N+1). fund_id is checked
-- alongside the id list rather than trusted alone: an id belonging to
-- another fund must never surface here, the same rule GetReceiptForFund
-- enforces for a single row. Ordered by id ascending so a caller that
-- appends rows in the order they arrive keeps that same order per parent
-- id, matching receipt_ids' own "ordered by id ascending" contract.
-- name: ListReceiptIDsByTransactionIDs :many
SELECT transaction_id, id
FROM receipt
WHERE fund_id = ? AND transaction_id IN (sqlc.slice('transaction_ids'))
ORDER BY id;

-- Same shape as ListReceiptIDsByTransactionIDs above, for GET
-- /api/reimbursements's page.
-- name: ListReceiptIDsByReimbursementIDs :many
SELECT reimbursement_id, id
FROM receipt
WHERE fund_id = ? AND reimbursement_id IN (sqlc.slice('reimbursement_ids'))
ORDER BY id;

-- DeleteReceipt removes a wrong or duplicate photo - ADR-011 states plainly
-- that "a wrong photo is replaceable" and `receipt` rows are insertable and
-- deletable, unlike a ledger row. Scoped by id alone, the same shape as
-- DeleteAccount and DeleteReimbursement: the caller already fund-scoped the
-- row through GetReceiptForFund above before reaching this. It removes only
-- the database row - the file on disk is left in place; see
-- internal/http/receipts.go's own comment for why (ADR-011 already accepts
-- the orphan as cheaper than a transactional delete).
-- name: DeleteReceipt :exec
DELETE FROM receipt
WHERE id = ?;
