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

-- Fund-scoped: a receipt is the one resource this API serves as raw bytes, so
-- this is the only gate between a session and someone else's photo (#153).
-- name: GetReceiptForFund :one
SELECT id, fund_id, transaction_id, reimbursement_id, path, uploaded_at
FROM receipt
WHERE id = ? AND fund_id = ?;

-- Batched, fund-scoped lookup for a whole page of rows (#154): one query per
-- list response, never one per row. fund_id is checked alongside the id list,
-- so another fund's id never surfaces. Callers append in arrival order, which
-- keeps receipt_ids in upload order per parent.
-- name: ListReceiptIDsByTransactionIDs :many
SELECT transaction_id, id
FROM receipt
WHERE fund_id = ? AND transaction_id IN (sqlc.slice('transaction_ids'))
ORDER BY uploaded_at, id;

-- Same, for GET /api/reimbursements's page.
-- name: ListReceiptIDsByReimbursementIDs :many
SELECT reimbursement_id, id
FROM receipt
WHERE fund_id = ? AND reimbursement_id IN (sqlc.slice('reimbursement_ids'))
ORDER BY uploaded_at, id;

-- The backup export's read (ADR-012): every receipt row, so the zip's receipts/
-- folder and uruni.json name the same files (path is the on-disk filename
-- under URUNI_UPLOADS_DIR, ADR-011). Ordered by id only for deterministic
-- export bytes.
-- name: ListReceiptsByFund :many
SELECT id, fund_id, transaction_id, reimbursement_id, path, uploaded_at
FROM receipt
WHERE fund_id = ?
ORDER BY id;

-- Removes a wrong or duplicate photo; receipt rows are deletable, unlike a
-- ledger row (ADR-011). Scoped by id alone: the caller fund-scoped the row via
-- GetReceiptForFund. Only the database row goes - the file on disk stays, an
-- orphan ADR-011 accepts as cheaper than a transactional delete.
-- name: DeleteReceipt :exec
DELETE FROM receipt
WHERE id = ?;
