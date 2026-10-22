-- name: CreateMember :one
INSERT INTO member (fund_id, name, joined_on, inactive_on, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING id, fund_id, name, joined_on, inactive_on, created_at;

-- The only single-member lookup, fund-scoped: a real id belonging to another
-- fund answers sql.ErrNoRows rather than being found and then rejected. There
-- is deliberately no unscoped GetMember (#188).
-- name: GetMemberForFund :one
SELECT id, fund_id, name, joined_on, inactive_on, created_at
FROM member
WHERE id = ? AND fund_id = ?;

-- By name, ignoring case (maintainer, 2026-10-05): the order every member list
-- reads, Iuran status included. id only breaks a tie between identical names.
-- name: ListMembersByFund :many
SELECT id, fund_id, name, joined_on, inactive_on, created_at
FROM member
WHERE fund_id = ?
ORDER BY name COLLATE NOCASE, id;

-- A correction to reference data, not a ledger event. name and joined_on are
-- NOT NULL, so COALESCE covers them: nil can only mean "leave alone". The one
-- nullable column needs the set_* flag, since there a null argument is
-- ambiguous between "leave alone" and "clear it". The tier is not a column of
-- member (ADR-040): it is member_tier rows, written by Ledger.UpdateMember.
-- name: UpdateMember :one
UPDATE member
SET name        = COALESCE(sqlc.narg('name'), name),
    joined_on   = COALESCE(sqlc.narg('joined_on'), joined_on),
    inactive_on = CASE WHEN CAST(sqlc.arg('set_inactive_on') AS INTEGER) = 1 THEN sqlc.narg('inactive_on') ELSE inactive_on END
WHERE id = sqlc.arg('id')
RETURNING id, fund_id, name, joined_on, inactive_on, created_at;

-- Leans on the composite FKs from "transaction" and reimbursement to refuse
-- once a real row references the member.
-- name: DeleteMember :exec
DELETE FROM member
WHERE id = ?;

-- GET /api/members (#233, ADR-032): the roster read alphabetically, so it
-- keyset-pages ascending on (name, id), not the date-descending shape of the
-- other paged lists. Row-value keyset like ListTransactionsPage: an OFFSET
-- would skip or duplicate a member when equal names straddle a page boundary
-- and a same-named member is inserted between fetches.
--
-- tier_id, tier_name and current_rate describe the member's CURRENT tier
-- (ADR-040): the standing member_tier row of the latest month at or before
-- max(current_period, the join month), so a member who joins next month shows the
-- tier entered for them, with current_rate (what GetEffectiveDuesRate would find)
-- read at that same month. Reads go through the *_standing views, never the
-- tables. A member with no row, or a NULL tier, reads NULL in all three.
--
-- current_period is "YYYY-MM", the caller's one snapshot of "now", shared with
-- the arrears figure computed in Go - see
-- Ledger.ArrearsMonthsForMember. A LEFT JOIN, not a scalar subquery, on
-- purpose: a scalar subquery needs a CAST to get a concrete Go type, and this
-- codebase's CAST idiom means "never NULL" - the opposite of what current_rate
-- must say. A tier with no rate yet effective (the "madya TBD" case, PRD
-- section 6) and a member with no tier (NULL = NULL is never true) both read
-- NULL, never an invented 0 or a stale figure. The dr2 NOT EXISTS picks the
-- single row with the latest effective_from <= current_period per tier
-- (dues_rate_standing leaves a deleted month out, so the month before answers).
--
-- ?q= is a case-insensitive substring match on name alone (ADR-032): INSTR/LOWER,
-- never "COLLATE NOCASE LIKE ... ESCAPE" - sqlc 1.31.1 silently drops a
-- repeated sqlc.narg inside that clause.
--
-- page_limit is page size + 1: peek at one extra row.
-- name: ListMembersPage :many
SELECT
  m.id, m.fund_id, m.name, m.joined_on, m.inactive_on, m.created_at,
  ct.tier_id AS tier_id,
  dt.name AS tier_name,
  dr.amount AS current_rate
FROM member m
LEFT JOIN member_tier_standing ct
  ON ct.member_id = m.id
 AND ct.effective_from = (
       SELECT MAX(x.effective_from) FROM member_tier_standing x
       WHERE x.member_id = m.id
         AND x.effective_from <= MAX(CAST(sqlc.arg('current_period') AS TEXT), SUBSTR(m.joined_on, 1, 7))
     )
LEFT JOIN dues_tier dt ON dt.id = ct.tier_id
LEFT JOIN dues_rate_standing dr
  ON dr.tier_id = ct.tier_id
 AND dr.effective_from <= MAX(CAST(sqlc.arg('current_period') AS TEXT), SUBSTR(m.joined_on, 1, 7))
 AND NOT EXISTS (
       SELECT 1 FROM dues_rate_standing dr2
       WHERE dr2.tier_id = dr.tier_id
         AND dr2.effective_from <= MAX(CAST(sqlc.arg('current_period') AS TEXT), SUBSTR(m.joined_on, 1, 7))
         AND dr2.effective_from > dr.effective_from
     )
WHERE m.fund_id = sqlc.arg('fund_id')
  AND (
    sqlc.narg('cursor_name') IS NULL
    OR (m.name COLLATE NOCASE, m.id) > (sqlc.narg('cursor_name'), CAST(sqlc.narg('cursor_id') AS INTEGER))
  )
  AND (
    sqlc.narg('q') IS NULL
    OR INSTR(LOWER(m.name), LOWER(sqlc.narg('q'))) > 0
  )
ORDER BY m.name COLLATE NOCASE, m.id
LIMIT sqlc.arg('page_limit');
