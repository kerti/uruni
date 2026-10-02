-- Restore's own queries (M6.39, #325, ADR-012): every table this package's
-- normal Create* queries also cover, plus two restore only needs. A normal
-- Create* never takes id - the schema assigns it - so restore, which must
-- preserve every id verbatim (ADR-012's obligation 2), needs its own insert
-- naming id explicitly instead of reusing them. The DeleteAll* queries are
-- restore's other own need: clearing a table in full before re-inserting
-- exactly what the file holds, the same "delete all sessions" shape
-- DeleteAllSessions (session.sql) already established for a password reset.
--
-- Table order below follows Document's own field order (internal/backup/
-- backup.go), itself the migration file's dependency order - not because
-- these statements must run in that order (internal/backup/restore.go's own
-- comment says why they need not), but so this file reads the same way the
-- schema and the export already do.

-- name: DeleteAllFunds :exec
DELETE FROM fund;

-- name: RestoreFund :exec
INSERT INTO fund (id, name, currency, report_slug, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteAllAccounts :exec
DELETE FROM account;

-- name: RestoreAccount :exec
INSERT INTO account (id, fund_id, kind, name, created_at, inactive_on)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteAllPurposes :exec
DELETE FROM purpose;

-- name: RestorePurpose :exec
INSERT INTO purpose (id, fund_id, kind, name, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteAllDuesTiers :exec
DELETE FROM dues_tier;

-- name: RestoreDuesTier :exec
INSERT INTO dues_tier (id, fund_id, name, created_at)
VALUES (?, ?, ?, ?);

-- name: DeleteAllDuesRates :exec
DELETE FROM dues_rate;

-- name: RestoreDuesRate :exec
INSERT INTO dues_rate (id, tier_id, amount, effective_from, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: DeleteAllMembers :exec
DELETE FROM member;

-- name: RestoreMember :exec
INSERT INTO member (id, fund_id, name, tier_id, joined_on, inactive_on, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAllTransfers :exec
DELETE FROM transfer;

-- name: RestoreTransfer :exec
INSERT INTO transfer (id, fund_id, kind, corrects_transaction_id, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteAllReimbursements :exec
DELETE FROM reimbursement;

-- name: RestoreReimbursement :exec
INSERT INTO reimbursement (id, fund_id, member_id, purpose_id, amount, incurred_on, waived_on, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAllTransactions :exec
DELETE FROM "transaction";

-- RestoreTransaction is the one insert restore.go must sequence with care
-- (ADR-012's obligation 1): "transaction" keeps its transaction_named_row_shape
-- BEFORE INSERT trigger live through a restore - on purpose, it is what
-- refuses a hand-edited or corrupted backup - so every row naming a
-- reverses_transaction_id must still be inserted after the row it names.
-- name: RestoreTransaction :exec
INSERT INTO "transaction" (
  id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
  member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id, note, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAllReceipts :exec
DELETE FROM receipt;

-- name: RestoreReceipt :exec
INSERT INTO receipt (id, fund_id, transaction_id, reimbursement_id, path, uploaded_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteAllReconciliations :exec
DELETE FROM reconciliation;

-- name: RestoreReconciliation :exec
INSERT INTO reconciliation (id, fund_id, performed_at, through_transaction_id, note, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteAllReconciliationLines :exec
DELETE FROM reconciliation_line;

-- name: RestoreReconciliationLine :exec
INSERT INTO reconciliation_line (
  id, fund_id, reconciliation_id, account_id, recorded_amount, actual_amount,
  difference_amount, resolution, adjustment_transaction_id
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAllIncidentals :exec
DELETE FROM incidental;

-- name: RestoreIncidental :exec
INSERT INTO incidental (purpose_id, occasion, target_amount, opened_on, closed_on, minimum_per_member, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAllIncidentalRecipients :exec
DELETE FROM incidental_recipient;

-- name: RestoreIncidentalRecipient :exec
INSERT INTO incidental_recipient (fund_id, purpose_id, member_id)
VALUES (?, ?, ?);
