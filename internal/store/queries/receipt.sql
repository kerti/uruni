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
