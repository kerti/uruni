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

-- The fund-scoped fetch a dues reversal gets its target through (ADR-029): a
-- transaction of another fund reads as sql.ErrNoRows rather than being found
-- and later rejected by the composite FK. GetTransaction stays unscoped for
-- callers that already hold a fund-scoped row; this is the one that fetches by
-- id from an untrusted request.
-- name: GetTransactionForFund :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, reverses_transaction_id,
       note, created_at
FROM "transaction"
WHERE fund_id = ? AND id = ?;

-- The reversed-once pre-check, like GetReimbursementSettlement: sql.ErrNoRows
-- means "not yet reversed, proceed"; a row means it already has been. The
-- dues_payment_reversed_once index is the actual guarantee - this only turns a
-- raw constraint violation into a named error (ADR-029).
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

-- GET /api/transactions (#225, ADR-032): newest-first, keyset-paged on the pair
-- it orders by. LIMIT/OFFSET was rejected: occurred_on is backdatable (PRD
-- section 7.2), so a row can land mid-list between two page fetches and an
-- offset would skip or duplicate it. The row-value comparison cannot:
-- cursor_occurred_on/cursor_id are NULL on the first page (the clause
-- short-circuits true), and a later page passes the previous page's last row,
-- keeping only strictly older pairs. cursor_id is CAST to INTEGER inside the
-- tuple because otherwise sqlc infers its Go type from the tuple's other
-- element (TEXT) and generates CursorID as *string for an int64 key.
--
-- Search is every clause under q: a case-insensitive substring test over note,
-- purpose name and member name (LEFT JOIN - most rows carry no member_id),
-- plus an exact amount match. INSTR(LOWER(col), LOWER(q)) > 0 rather than
-- "col COLLATE NOCASE LIKE q ESCAPE '$'" as ADR-032 literally asks for: sqlc
-- v1.31.1 only registers the FIRST such clause in a query and silently leaves
-- every later one as literal text ("sqlc.narg('q_purpose')" verbatim) in the
-- emitted SQL. Nothing catches it: the compiler sees a struct missing fields,
-- `make sqlc` exits 0, and the query is valid SQL that returns rows; it is
-- only wrong once a purpose- or member-name search runs on real data. INSTR
-- also treats % and _ as plain characters, so "LIKE wildcards in q are
-- literal" holds by construction, not by escaping.
--
-- member_id, purpose_id, month and direction are Riwayat -> Transaksi's filters
-- (#424, the public report's four); dues_period is Dues/MemberPayments.tsx's
-- payment history panel alone. month is "YYYY-MM", prefix-compared against
-- occurred_on's "YYYY-MM-DD" (the schema's CHECK keeps that form). All compose
-- with q and the keyset cursor.
--
-- page_limit is page size + 1: the caller peeks at the extra row to know
-- whether a next page exists, then trims it.
--
-- The columns after created_at (#257) carry every fact a system-created row's
-- display label needs, so the client never makes a second request - see
-- TransactionList.tsx. Every join is fund-scoped by construction, not by an
-- extra fund_id clause: each id it joins on reaches this table only through a
-- composite FK, so another fund's row can never be joined.
--
--   - account_name: this row's account - opening balance and a reconciliation
--     fix label themselves by it. INNER join: account_id is NOT NULL and
--     DeleteAccount refuses an account any transaction references.
--   - member_name/settlement_member_name: the dues member for a dues payment
--     or reversal (t.member_id), or, separately, the settled claim's member
--     for a reimbursement payout (t.member_id is NULL on kind='reimbursement',
--     ADR-024). Two columns, not one SQL COALESCE: sqlc 1.31.1 infers
--     COALESCE(a, b) of two LEFT-JOINed nullable columns as NOT NULL, so the
--     merge (at most one is non-NULL per row) happens in Go, in
--     toTransactionResponse, where the compiler still sees a pointer.
--   - claim_note: the settled claim's note, NULL except on a
--     kind='reimbursement' row.
--   - transfer_kind plus four transfer_from/to_account/purpose_name columns:
--     only meaningful for kind='transfer' rows. tf/tt are the transfer's two
--     legs, found by direction ('out' is the pair's from leg, 'in' its to leg;
--     internal/ledger/transfer.go) - never by which leg *this* row is, so both
--     legs carry identical from/to facts. Four plain columns rather than a SQL
--     CASE choosing by transfer.kind: sqlc 1.31.1 cannot type such a CASE (it
--     lands as interface{}, losing the compile-time check ADR-024 relies on),
--     and forcing a type with CAST claims "never NULL" (see
--     LatestDuesPeriodPaidByMember) - false for these. toTransactionResponse
--     picks the account or purpose pair in Go.
--   - transfer_corrects_transaction_id: the transfer's link (ADR-033, #267),
--     non-NULL exactly on a correction pair's two legs - what tells them from a
--     roll's, which has the same kind='transfer', kind='reclass_purpose' shape.
--   - transfer_reason: the transfer's reason (ADR-036) - 'roll', 'allocation',
--     or NULL (a correction, a between_accounts pair, or a pair predating the
--     column, which reads as a roll). Riwayat labels an allocation "Pindah pos"
--     from it.
--   - effective_purpose_id: the tag this row's money is under NOW - its own
--     purpose_id until a correction (ADR-033) moves it, then the end of the
--     path its corrections walk. Read from ledger_entry, which defines it once
--     (#628; the schema file has the reasoning, #411). An uncorrected row
--     answers itself and "has this been corrected?" is effective <> stored.
--
--     The list keeps rendering the STORED tag (ADR-033): the ledger sums stored
--     tags, so showing the effective one would put the screen out of step with
--     the balances. This column is for the correction dialog, which must build
--     its pair from where the money actually is, and for the marker that says a
--     correction exists.
-- name: ListTransactionsPage :many
SELECT t.id, t.fund_id, t.account_id, t.purpose_id, t.direction, t.amount, t.occurred_on, t.kind,
       t.member_id, t.dues_period, t.reimbursement_id, t.transfer_id, t.reverses_transaction_id,
       t.note, t.created_at,
       a.name AS account_name,
       m.name AS member_name,
       rm.name AS settlement_member_name,
       rb.note AS claim_note,
       tr.kind AS transfer_kind,
       fa.name AS transfer_from_account_name,
       ta.name AS transfer_to_account_name,
       fp.name AS transfer_from_purpose_name,
       tp.name AS transfer_to_purpose_name,
       tr.corrects_transaction_id AS transfer_corrects_transaction_id,
       tr.reason AS transfer_reason,
       t.effective_purpose_id
FROM ledger_entry t
JOIN purpose p ON p.id = t.purpose_id
JOIN account a ON a.id = t.account_id
LEFT JOIN member m ON m.id = t.member_id
LEFT JOIN reimbursement rb ON rb.id = t.reimbursement_id
LEFT JOIN member rm ON rm.id = rb.member_id
LEFT JOIN transfer tr ON tr.id = t.transfer_id
LEFT JOIN "transaction" tf ON tf.transfer_id = t.transfer_id AND tf.direction = 'out'
LEFT JOIN "transaction" tt ON tt.transfer_id = t.transfer_id AND tt.direction = 'in'
LEFT JOIN account fa ON fa.id = tf.account_id
LEFT JOIN account ta ON ta.id = tt.account_id
LEFT JOIN purpose fp ON fp.id = tf.purpose_id
LEFT JOIN purpose tp ON tp.id = tt.purpose_id
WHERE t.fund_id = sqlc.arg('fund_id')
  AND (
    sqlc.narg('cursor_occurred_on') IS NULL
    OR (t.occurred_on, t.id) < (sqlc.narg('cursor_occurred_on'), CAST(sqlc.narg('cursor_id') AS INTEGER))
  )
  AND (
    sqlc.narg('q') IS NULL
    OR INSTR(LOWER(t.note), LOWER(sqlc.narg('q'))) > 0
    OR INSTR(LOWER(p.name), LOWER(sqlc.narg('q'))) > 0
    OR INSTR(LOWER(m.name), LOWER(sqlc.narg('q'))) > 0
    OR t.amount = sqlc.narg('q_amount')
  )
  AND (sqlc.narg('member_id') IS NULL OR t.member_id = sqlc.narg('member_id'))
  AND (sqlc.narg('dues_period') IS NULL OR t.dues_period = sqlc.narg('dues_period'))
  AND (sqlc.narg('purpose_id') IS NULL OR t.purpose_id = sqlc.narg('purpose_id'))
  AND (sqlc.narg('month') IS NULL OR substr(t.occurred_on, 1, 7) = sqlc.narg('month'))
  AND (sqlc.narg('direction') IS NULL OR t.direction = sqlc.narg('direction'))
ORDER BY t.occurred_on DESC, t.id DESC
LIMIT sqlc.arg('page_limit');

-- Every balance in Uruni is this shape: sum the ledger, never read a stored
-- total (CLAUDE.md rule 2). ledger_entry.signed_amount carries the sign, so the
-- view is the only place a minus appears. The CAST is not decoration - without it sqlc
-- emits interface{} for the aggregate and the trust core loses its compiler
-- exactly where it is needed most (ADR-024).
-- name: FundBalance :one
SELECT CAST(COALESCE(SUM(signed_amount), 0) AS INTEGER) AS balance_amount
FROM ledger_entry
WHERE fund_id = ?;

-- name: AccountBalance :one
SELECT CAST(COALESCE(SUM(signed_amount), 0) AS INTEGER) AS balance_amount
FROM ledger_entry
WHERE fund_id = ? AND account_id = ?;

-- name: PurposeBalance :one
SELECT CAST(COALESCE(SUM(signed_amount), 0) AS INTEGER) AS balance_amount
FROM ledger_entry
WHERE fund_id = ? AND purpose_id = ?;

-- The frozen half of a reconciliation: the same sum as AccountBalance, cut off
-- at the snapshot's through_transaction_id. Ordered by id, not occurred_on, so
-- a correction backdated after the count keeps its higher id and stays outside
-- the old snapshot's arithmetic while counting toward today's balance.
-- name: AccountBalanceThrough :one
SELECT CAST(COALESCE(SUM(signed_amount), 0) AS INTEGER) AS balance_amount
FROM ledger_entry
WHERE fund_id = ? AND account_id = ? AND id <= ?;

-- name: ListDuesPaymentsByMember :many
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, note, created_at
FROM "transaction"
WHERE member_id = ? AND kind = 'dues'
ORDER BY dues_period, id;

-- GET /api/dues-payments (#228, ADR-032): newest-first, keyset-paged on
-- (occurred_on DESC, id DESC) like ListTransactionsPage, which documents the
-- row-value keyset, the cursor_id CAST, and why INSTR/LOWER replaces
-- "COLLATE NOCASE LIKE ... ESCAPE" (the sqlc 1.31.1 repeated-ESCAPE bug).
--
-- Both halves of a reversal share this list (PRD section 7.3, ADR-029): every
-- kind='dues' payment and every kind='adjustment' row that reverses one -
-- never a bare "every transaction".
--
-- reversed_by_transaction_id is the reversal that undoes THIS row when it is a
-- payment (ledger_entry; null otherwise, as a reversal is never itself
-- reversed). orig is the payment THIS row reverses, carried forward as
-- reverses_occurred_on so a reversal states the original's date even when that
-- payment sits on a later page.
--
-- ?q= searches member name only.
--
-- page_limit is page size + 1: peek at one extra row.
-- name: ListDuesPaymentsPage :many
SELECT
  t.id, t.kind, t.member_id, m.name AS member_name, t.dues_period, t.amount,
  t.occurred_on, a.name AS account_name, t.note, t.reverses_transaction_id,
  t.reversed_by_transaction_id,
  orig.occurred_on AS reverses_occurred_on
FROM ledger_entry t
JOIN member m ON m.id = t.member_id
JOIN account a ON a.id = t.account_id
LEFT JOIN "transaction" orig ON orig.id = t.reverses_transaction_id
WHERE t.fund_id = sqlc.arg('fund_id')
  AND (t.kind = 'dues' OR (t.kind = 'adjustment' AND t.reverses_transaction_id IS NOT NULL))
  AND (
    sqlc.narg('cursor_occurred_on') IS NULL
    OR (t.occurred_on, t.id) < (sqlc.narg('cursor_occurred_on'), CAST(sqlc.narg('cursor_id') AS INTEGER))
  )
  AND (
    sqlc.narg('q') IS NULL
    OR INSTR(LOWER(m.name), LOWER(sqlc.narg('q'))) > 0
  )
ORDER BY t.occurred_on DESC, t.id DESC
LIMIT sqlc.arg('page_limit');

-- The envelope's net: everything ever posted against its purpose_id, rolls
-- included. CloseIncidentalAndRoll leans on it (ADR-031): collected minus
-- disbursed is exactly PurposeBalance, so a prior roll's leg already nets the
-- envelope to zero, and reopening, posting a late entry and closing again
-- computes the *net delta* through this same unfiltered sum. Leftover is
-- collected minus disbursed, computed in Go via money.Amount.Sub.
--
-- Not what GET /api/incidentals/{purposeID} shows: that wants what the
-- occasion itself collected and spent, rolls excluded - see
-- IncidentalActivityTotals. Conflating the two was #215.
-- name: IncidentalTotals :one
SELECT
  CAST(COALESCE(SUM(CASE WHEN signed_amount > 0 THEN signed_amount ELSE 0 END), 0) AS INTEGER) AS collected_amount,
  CAST(COALESCE(SUM(CASE WHEN signed_amount < 0 THEN -signed_amount ELSE 0 END), 0) AS INTEGER) AS disbursed_amount
FROM ledger_entry
WHERE fund_id = ? AND purpose_id = ?;

-- What the occasion itself collected and disbursed (PRD section 7.5's detail
-- screen), as opposed to IncidentalTotals' net-including-rolls. The LEFT JOIN
-- excludes only a reclass_purpose leg that is a roll (CloseIncidentalAndRoll,
-- in either direction, ADR-031), by transfer.kind and not transaction.kind:
-- every transfer leg is posted as transaction.kind='transfer', so that alone
-- cannot tell a roll's leg from an ordinary between_accounts transfer, which
-- this screen has no reason to exclude. tr.id IS NULL keeps every row with no
-- reclass_purpose transfer; a correction's legs come back in below.
--
-- A cancelled contribution (ADR-034's widened ADR-029 reversal) is dropped
-- whole: the reversal row and the row it reverses, by the same ledger_entry
-- reversed_on test as ContributedByIncidentalMember. Otherwise a cancellation
-- reads as money spent ("Terpakai") and the cancelled row as money collected. Both halves go,
-- so collected minus disbursed still equals the envelope's balance;
-- IncidentalTotals stays unfiltered because the pair nets to zero there.
--
-- A peruntukan correction (ADR-033) is re-attributed, not dropped (#342). Its
-- legs carry tr.corrects_transaction_id, which a roll's never do, so the join
-- keeps them and reads each against the bucket of the row it corrects
-- (o.direction): a leg moving the same way as that row adds to the bucket, the
-- other way takes it back out. So an unnamed contribution corrected out of
-- this envelope nets to zero collected here; one corrected in reads as
-- collected; an expense corrected away un-spends. Each leg still moves
-- collected minus disbursed by exactly its effect on the balance. The
-- corrected row is never a reversal (ADR-033 refuses those), so the
-- cancellation filters never meet it.
--
-- A purpose move (ADR-036, tr.reason = 'allocation') is kept and only ever
-- counts as collected: money allocated in is money the envelope has to work
-- with, and money allocated out was never spent on the occasion, so it takes
-- back what was collected rather than reading as disbursed. A roll - 'roll',
-- or NULL on a row written before the column existed - stays excluded.
--
-- through bounds it at a past month's last day for the public report (#408):
-- only rows dated by then, and a reversal dated after it has not happened yet.
-- NULL (every other caller) is unbounded.
-- name: IncidentalActivityTotals :one
SELECT
  CAST(COALESCE(SUM(CASE
    WHEN tr.reason = 'allocation' THEN t.signed_amount
    WHEN COALESCE(o.direction, t.direction) = 'in' THEN t.signed_amount
    ELSE 0 END), 0) AS INTEGER) AS collected_amount,
  CAST(COALESCE(SUM(CASE
    WHEN tr.reason = 'allocation' THEN 0
    WHEN COALESCE(o.direction, t.direction) = 'out' THEN -t.signed_amount
    ELSE 0 END), 0) AS INTEGER) AS disbursed_amount
FROM ledger_entry t
LEFT JOIN transfer tr ON tr.id = t.transfer_id AND tr.kind = 'reclass_purpose'
LEFT JOIN "transaction" o ON o.fund_id = tr.fund_id AND o.id = tr.corrects_transaction_id
WHERE t.fund_id = sqlc.arg('fund_id') AND t.purpose_id = sqlc.arg('purpose_id')
  AND (tr.id IS NULL OR tr.corrects_transaction_id IS NOT NULL OR tr.reason = 'allocation')
  AND t.reverses_transaction_id IS NULL
  AND (t.reversed_on IS NULL
       OR (CAST(sqlc.narg('through') AS TEXT) IS NOT NULL AND t.reversed_on > CAST(sqlc.narg('through') AS TEXT)))
  AND (CAST(sqlc.narg('through') AS TEXT) IS NULL OR t.occurred_on <= CAST(sqlc.narg('through') AS TEXT));

-- The roster query behind "who has paid / partially / not yet" for one
-- dues_period, every member in one pass.
-- name: DuesPaidByPeriod :many
-- The reversed = 0 clause is ADR-029's half of the reversal design: a
-- reversal is kind='adjustment' so never matches kind = 'dues'; this drops the
-- ORIGINAL row once something reverses it, so a reversed payment disappears
-- from "paid" rather than counting twice.
SELECT member_id, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS paid_amount
FROM ledger_entry t
WHERE t.fund_id = ? AND t.kind = 'dues' AND t.dues_period = ?
  AND t.reversed = 0
GROUP BY member_id;

-- DuesPaidByPeriod's transpose: every period one member has paid toward, in one
-- pass over their history (OutstandingDuesForMember's range walk). Same
-- reversed-row exclusion (ADR-029) and the same CAST(... AS TEXT) as
-- LatestDuesPeriodPaidByMember - kind = 'dues' rows always carry a dues_period
-- (schema CHECK), so it is never NULL, only untyped without the cast.
-- name: DuesPaidByMemberGroupedByPeriod :many
SELECT CAST(dues_period AS TEXT) AS dues_period, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS paid_amount
FROM ledger_entry t
WHERE t.fund_id = ? AND t.member_id = ? AND t.kind = 'dues'
  AND t.reversed = 0
GROUP BY dues_period;

-- The "paid in advance" signal. dues_period is 'YYYY-MM', so a lexicographic
-- MAX is also the chronological one. The CAST(... AS TEXT) is load-bearing:
-- uncast, MAX(dues_period) generates interface{} (the ADR-024 trap for SUM,
-- reached through MAX), and a .(string) assertion on it can panic since drivers
-- commonly hand back []byte.
-- name: LatestDuesPeriodPaidByMember :many
-- The reversed = 0 clause is ADR-029's fix: without it a reversed row would
-- still be the MAX(dues_period) for its member, reading as "paid in advance"
-- through a period that is no longer paid at all.
SELECT member_id, CAST(MAX(dues_period) AS TEXT) AS latest_period
FROM ledger_entry t
WHERE t.fund_id = ? AND t.kind = 'dues'
  AND t.reversed = 0
GROUP BY member_id;

-- The reconciliation cutoff. Deliberately not an aggregate: CAST(MAX(id) AS
-- INTEGER) generates a non-nullable (int64, error), yet a bare aggregate still
-- returns one NULL row on an empty table, so a fund's first-ever
-- reconciliation would fail with "converting NULL to int64 is unsupported".
-- This form returns zero rows instead - a clean sql.ErrNoRows the domain reads
-- as "no ledger yet".
-- name: MaxTransactionIDByFund :one
SELECT id
FROM "transaction"
WHERE fund_id = ?
ORDER BY id DESC
LIMIT 1;

-- The named half of an envelope's participation (ADR-034): every named
-- contribution against one purpose_id, summed per member, excluding a row some
-- reversal points at (ADR-029) so a reversed contribution disappears from
-- "gave" rather than counting twice. Unlike DuesPaidByPeriod (ledger_entry.reversed)
-- it drops a reversal only if it is dated on or before through, via reversed_on.
-- member_id IS NOT NULL is redundant with the schema's CHECK but kept explicit.
--
-- Says nothing about who was EXPECTED to give: that is ListMembersByFund plus
-- the envelope's opened_on and recipients, combined in Go
-- (Ledger.GetIncidentalParticipation).
--
-- through bounds it as in IncidentalActivityTotals (#408).
-- name: ContributedByIncidentalMember :many
SELECT member_id, CAST(COALESCE(SUM(amount), 0) AS INTEGER) AS contributed_amount
FROM ledger_entry t
WHERE t.fund_id = sqlc.arg('fund_id') AND t.purpose_id = sqlc.arg('purpose_id') AND t.kind = 'normal' AND t.direction = 'in'
  AND t.member_id IS NOT NULL
  AND (t.reversed_on IS NULL
       OR (CAST(sqlc.narg('through') AS TEXT) IS NOT NULL AND t.reversed_on > CAST(sqlc.narg('through') AS TEXT)))
  AND (CAST(sqlc.narg('through') AS TEXT) IS NULL OR t.occurred_on <= CAST(sqlc.narg('through') AS TEXT))
GROUP BY member_id;

-- The settle-once pre-check: sql.ErrNoRows when the claim is not yet settled,
-- the settling row when it is.
-- name: GetReimbursementSettlement :one
SELECT id, fund_id, account_id, purpose_id, direction, amount, occurred_on, kind,
       member_id, dues_period, reimbursement_id, transfer_id, note, created_at
FROM "transaction"
WHERE fund_id = ? AND reimbursement_id = ? AND kind = 'reimbursement';

