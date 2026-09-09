-- name: CreateTransaction :one
INSERT INTO "transaction" (
  fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
  member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
  note, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
          member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
          note, created_at;

-- name: GetTransaction :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE id = ?;

-- The fund-scoped fetch a dues reversal fetches its target through (ADR-029):
-- WHERE fund_id = ? AND id = ?, not id alone, so a transaction belonging to
-- another fund reads as sql.ErrNoRows here rather than being found and only
-- later rejected by the composite FK. GetTransaction above stays as it is -
-- other callers already have their fund-scoped row in hand - this is the one
-- new caller that fetches a transaction by id from an untrusted request.
-- name: GetTransactionForFund :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE fund_id = ? AND id = ?;

-- The reversed-once pre-check, same shape as GetReimbursementSettlement and
-- GetOpeningBalance above: sql.ErrNoRows means "not yet reversed, proceed"
-- (the expected, non-error path); a row means it already has been. The
-- dues_payment_reversed_once partial unique index is the actual guarantee -
-- this pre-check only turns a raw constraint violation into a clean, named
-- error (ADR-029, mirroring ADR-027's ErrReimbursementAlreadySettled).
-- name: GetDuesPaymentReversal :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE fund_id = ? AND reverses_transaction_id = ?;

-- name: ListTransactionsByFund :many
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE fund_id = ?
ORDER BY occurred_on, id;

-- Every balance in Uruni is this shape: sum the ledger, never read a stored
-- total (CLAUDE.md rule 2). direction carries the sign, so the CASE is the
-- only place a minus appears. The CAST is not decoration - without it sqlc
-- emits interface{} for the aggregate and the trust core loses its compiler
-- exactly where it is needed most (ADR-024).
-- name: FundBalance :one
SELECT CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) AS INTEGER) AS balance_amount
FROM "transaction"
WHERE fund_id = ?;

-- name: AccountBalance :one
SELECT CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) AS INTEGER) AS balance_amount
FROM "transaction"
WHERE fund_id = ? AND account_id = ?;

-- name: PurposeBalance :one
SELECT CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) AS INTEGER) AS balance_amount
FROM "transaction"
WHERE fund_id = ? AND purpose_id = ?;

-- The frozen half of a reconciliation: the same sum as AccountBalance, cut off
-- at the snapshot's through_transaction_id. Ordered by id, not occurred_on, so
-- a correction backdated after the count keeps its higher id and stays outside
-- the old snapshot's arithmetic while counting toward today's balance.
-- name: AccountBalanceThrough :one
SELECT CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE -amount END), 0) AS INTEGER) AS balance_amount
FROM "transaction"
WHERE fund_id = ? AND account_id = ? AND id <= ?;

-- name: ListDuesPaymentsByMember :many
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, note, created_at
FROM "transaction"
WHERE member_id = ? AND kind = 'dues'
ORDER BY dues_period, id;

-- The envelope's net, everything it has ever posted against its own
-- purpose_id, rolls included. This is what CloseIncidentalAndRoll leans on
-- (ADR-031): collected minus disbursed here is exactly PurposeBalance for
-- this purpose, so a prior roll's own leg already nets the envelope to zero
-- by construction, and reopening, posting a late entry, and closing again
-- computes the *net delta* through this same unfiltered sum rather than new
-- arithmetic. Leftover is collected minus disbursed, computed in Go via
-- money.Amount.Sub, rather than a third column here.
--
-- Not what GET /api/incidentals/{purposeID} shows: that figure wants what
-- the occasion itself collected and spent, with a prior roll's leg excluded
-- - see IncidentalActivityTotals below. Conflating the two was #215; this
-- comment is the seam between them.
-- name: IncidentalTotals :one
SELECT
  CAST(COALESCE(SUM(CASE WHEN direction = 'in' THEN amount ELSE 0 END), 0) AS INTEGER) AS collected_amount,
  CAST(COALESCE(SUM(CASE WHEN direction = 'out' THEN amount ELSE 0 END), 0) AS INTEGER) AS disbursed_amount
FROM "transaction"
WHERE fund_id = ? AND purpose_id = ?;

-- What the occasion itself collected and disbursed (PRD section 7.5's
-- detail screen), as opposed to IncidentalTotals above's net-including-rolls. The
-- LEFT JOIN excludes only a reclass_purpose leg - the roll CloseIncidentalAndRoll
-- posts, in either direction (ADR-031's leftover-in as much as the original
-- leftover-out) - by transfer.kind, not by transaction.kind: every leg of
-- every transfer is posted as transaction.kind='transfer' regardless of
-- whether the transfer itself is 'between_accounts' or 'reclass_purpose', so
-- transaction.kind alone cannot tell a roll's leg from an ordinary transfer
-- between this envelope's own accounts, and this screen has no reason to
-- exclude the latter. tr.id IS NULL keeps every row the join found no
-- matching reclass_purpose transfer for - which is every kind but that one.
-- name: IncidentalActivityTotals :one
SELECT
  CAST(COALESCE(SUM(CASE WHEN t.direction = 'in' THEN t.amount ELSE 0 END), 0) AS INTEGER) AS collected_amount,
  CAST(COALESCE(SUM(CASE WHEN t.direction = 'out' THEN t.amount ELSE 0 END), 0) AS INTEGER) AS disbursed_amount
FROM "transaction" t
LEFT JOIN transfer tr ON tr.id = t.transfer_id AND tr.kind = 'reclass_purpose'
WHERE t.fund_id = ? AND t.purpose_id = ? AND tr.id IS NULL;

-- The roster query behind "who has paid / partially / not yet" for one
-- dues_period, across every member in one pass rather than one query per
-- member.
-- name: DuesPaidByPeriod :many
-- The AND NOT EXISTS clause is ADR-029's half of the reversal design: a
-- reversed dues row itself never matched kind = 'dues' in the first place
-- (a reversal is posted as kind='adjustment'), so this drops the ORIGINAL
-- row from the sum once something reverses it - which is what makes a
-- reversed payment disappear from "paid" rather than simply counting twice.
SELECT member_id, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS paid_amount
FROM "transaction" t
WHERE t.fund_id = ? AND t.kind = 'dues' AND t.dues_period = ?
  AND NOT EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id)
GROUP BY member_id;

-- DuesPaidByMemberGroupedByPeriod is DuesPaidByPeriod's transpose: every
-- period one member has paid anything toward, in one pass over that
-- member's whole history rather than one query per period
-- (OutstandingDuesForMember's range walk). Same reversed-row exclusion
-- (ADR-029) and the same CAST(... AS TEXT) forcing dues_period non-null
-- LatestDuesPeriodPaidByMember already relies on - kind = 'dues' rows always
-- carry a dues_period (schema CHECK), so this is never actually NULL, only
-- untyped without the cast.
-- name: DuesPaidByMemberGroupedByPeriod :many
SELECT CAST(dues_period AS TEXT) AS dues_period, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS paid_amount
FROM "transaction" t
WHERE t.fund_id = ? AND t.member_id = ? AND t.kind = 'dues'
  AND NOT EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id)
GROUP BY dues_period;

-- The "paid in advance" signal. dues_period is 'YYYY-MM', so a lexicographic
-- MAX is also the chronological one. The CAST(... AS TEXT) is load-bearing,
-- not decoration: uncast, MAX(dues_period) generates interface{} - the same
-- untyped-interface trap ADR-024 documents for SUM, reached here through MAX
-- on a TEXT column - and a .(string) assertion on it is a live panic risk
-- since drivers commonly hand back []byte.
-- name: LatestDuesPeriodPaidByMember :many
-- The AND NOT EXISTS clause is ADR-029's fix for the specific bug the chosen
-- design exists to avoid: without it, a reversed row would still be the
-- chronological MAX(dues_period) for its member, reading as "paid in
-- advance" through a period that was reversed and is no longer paid at all.
SELECT member_id, CAST(MAX(dues_period) AS TEXT) AS latest_period
FROM "transaction" t
WHERE t.fund_id = ? AND t.kind = 'dues'
  AND NOT EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id)
GROUP BY member_id;

-- The reconciliation cutoff. Deliberately not an aggregate: SELECT
-- CAST(MAX(id) AS INTEGER) generates a non-nullable (int64, error), and a
-- bare aggregate with no GROUP BY still returns one row on an empty table
-- with the value NULL - so a fund's first-ever reconciliation would fail
-- with "converting NULL to int64 is unsupported". This form returns zero
-- rows instead, giving a clean sql.ErrNoRows the domain reads as "no ledger
-- yet".
-- name: MaxTransactionIDByFund :one
SELECT id
FROM "transaction"
WHERE fund_id = ?
ORDER BY id DESC
LIMIT 1;

-- The settle-once pre-check. Returns sql.ErrNoRows when the claim has not
-- been settled yet (the expected, non-error path) or the settling row when
-- it has.
-- name: GetReimbursementSettlement :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, note, created_at
FROM "transaction"
WHERE fund_id = ? AND reimbursement_id = ? AND kind = 'reimbursement';

-- The one-opening-per-account pre-check, same shape as
-- GetReimbursementSettlement above: no aggregate, so a fund with no opening
-- entry on this account yet returns a clean sql.ErrNoRows (the expected,
-- non-error path) rather than a NULL forced through an aggregate. A row means
-- one already exists.
-- name: GetOpeningBalance :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, note, created_at
FROM "transaction"
WHERE fund_id = ? AND account_id = ? AND kind = 'opening';
