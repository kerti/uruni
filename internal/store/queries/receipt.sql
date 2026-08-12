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
