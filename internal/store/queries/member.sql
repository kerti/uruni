-- name: CreateMember :one
INSERT INTO member (fund_id, name, tier_id, joined_on, inactive_on, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id, fund_id, name, tier_id, joined_on, inactive_on, created_at;

-- GetMemberForFund is the only single-member lookup: WHERE id = ? AND
-- fund_id = ?, the same shape GetTransactionForFund and GetReimbursement
-- already use, so a member id that is real but belongs to another fund
-- answers sql.ErrNoRows here rather than being found and only then rejected
-- for ownership. Its unscoped predecessor GetMember is gone (#188) - keeping
-- one around is how resolveMember came to be unscoped in the first place.
-- name: GetMemberForFund :one
SELECT id, fund_id, name, tier_id, joined_on, inactive_on, created_at
FROM member
WHERE id = ? AND fund_id = ?;

-- name: ListMembersByFund :many
SELECT id, fund_id, name, tier_id, joined_on, inactive_on, created_at
FROM member
WHERE fund_id = ?
ORDER BY id;

-- UpdateMember is a correction to reference data, not a ledger event. name
-- is NOT NULL, so COALESCE covers it: a nil argument can only mean "leave
-- alone". The three nullable columns need the set_* flags, because there a
-- null argument is ambiguous between "leave alone" and "clear it" - the CASE
-- substitutes the new value, NULL included, only when the caller sent it.
-- name: UpdateMember :one
UPDATE member
SET name        = COALESCE(sqlc.narg('name'), name),
    tier_id     = CASE WHEN CAST(sqlc.arg('set_tier_id')     AS INTEGER) = 1 THEN sqlc.narg('tier_id')     ELSE tier_id     END,
    joined_on   = CASE WHEN CAST(sqlc.arg('set_joined_on')   AS INTEGER) = 1 THEN sqlc.narg('joined_on')   ELSE joined_on   END,
    inactive_on = CASE WHEN CAST(sqlc.arg('set_inactive_on') AS INTEGER) = 1 THEN sqlc.narg('inactive_on') ELSE inactive_on END
WHERE id = sqlc.arg('id')
RETURNING id, fund_id, name, tier_id, joined_on, inactive_on, created_at;

-- DeleteMember leans on the composite foreign keys from "transaction" and
-- reimbursement to refuse it once a real row references the member.
-- name: DeleteMember :exec
DELETE FROM member
WHERE id = ?;

-- GET /api/members's real listing (#233, ADR-032 "Lists: paging and
-- search"): the roster read alphabetically, not a newest-first feed, so
-- this keyset-pages ascending on (name, id) rather than the (occurred_on
-- DESC, id DESC)/(performed_at DESC, id DESC) shape every other paged list
-- in this package uses - a roster has no date to sort by, and ADR-032 is
-- explicit this list is "by member name, then id, as the tiebreak". The
-- comparison is still row-value keyset, same reasoning as
-- ListTransactionsPage's own comment: an OFFSET would silently skip or
-- duplicate a member whenever two names compare equal at the page boundary
-- and a third with the same name is inserted between two fetches.
--
-- tier_name is dues_tier.name, NULL for a member with no tier_id - a plain
-- LEFT JOIN, never invented.
--
-- current_rate is the rate effective for sqlc.arg('current_period')
-- ("YYYY-MM", the caller's one snapshot of "now" - see
-- Ledger.ArrearsMonthsForMember's doc comment for why the same value drives
-- both this column and the arrears figure computed in Go), found the exact
-- row GetEffectiveDuesRate would find: the latest dues_rate row for the
-- member's tier at or before that period. A plain LEFT JOIN rather than a
-- correlated scalar subquery, on purpose - a scalar subquery's result needs
-- a CAST to read as a concrete Go type at all, and this codebase's own CAST
-- idiom (ListTransactionsPage's comment has it) is documented to mean "this
-- value is never NULL", the opposite of what current_rate must be able to
-- say: a tier with no rate yet effective (the "madya TBD" case, PRD section
-- 6) reads NULL here, never an invented 0 or a stale figure. The dr2
-- NOT EXISTS half of the join condition is what picks exactly the one row
-- with the latest effective_from <= current_period per tier - an ordinary
-- LEFT JOIN column, so sqlc types dr.amount as *int64 the same way it
-- already types tier_name as *string from the dt join below. A member with
-- no tier_id never matches dr.tier_id = m.tier_id (NULL = NULL is never
-- true in SQL) and also reads NULL, which is what "no tier" should show
-- either way.
--
-- ?q= is a case-insensitive substring match on name alone (ADR-032 line
-- 138: INSTR/LOWER, never "COLLATE NOCASE LIKE ... ESCAPE" - sqlc 1.31.1
-- silently drops a repeated sqlc.narg inside that clause).
--
-- page_limit is page size + 1, the same "peek at one extra row" trick every
-- other paged list in this package uses.
-- name: ListMembersPage :many
SELECT
  m.id, m.fund_id, m.name, m.tier_id, m.joined_on, m.inactive_on, m.created_at,
  dt.name AS tier_name,
  dr.amount AS current_rate
FROM member m
LEFT JOIN dues_tier dt ON dt.id = m.tier_id
LEFT JOIN dues_rate dr
  ON dr.tier_id = m.tier_id
 AND dr.effective_from <= sqlc.arg('current_period')
 AND NOT EXISTS (
       SELECT 1 FROM dues_rate dr2
       WHERE dr2.tier_id = dr.tier_id
         AND dr2.effective_from <= sqlc.arg('current_period')
         AND dr2.effective_from > dr.effective_from
     )
WHERE m.fund_id = sqlc.arg('fund_id')
  AND (
    sqlc.narg('cursor_name') IS NULL
    OR (m.name, m.id) > (sqlc.narg('cursor_name'), CAST(sqlc.narg('cursor_id') AS INTEGER))
  )
  AND (
    sqlc.narg('q') IS NULL
    OR INSTR(LOWER(m.name), LOWER(sqlc.narg('q'))) > 0
  )
ORDER BY m.name, m.id
LIMIT sqlc.arg('page_limit');
