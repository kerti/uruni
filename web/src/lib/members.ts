// Typed calls over apiFetch for the roster: /api/members (internal/http
// members.go).

import { apiFetch } from '@/lib/api'

/** A member row, as every route in this module returns it.
 *
 * tier_name and current_rate are period-independent: dues_tier.name for
 * tier_id, and the dues_rate in force this month for it. Both are nil for a
 * member with no tier, and current_rate alone for a tier whose rate has not
 * started yet (the undecided-rate case, PRD section 6). arrears_months is the
 * Tunggakan badge's figure: whole months of periods strictly before the
 * current one still owed, 0 meaning no badge. All three are zero-valued on a
 * POST/PATCH reply - read them only from listMembersPage. */
export interface Member {
  id: number
  name: string
  tier_id: number | null
  joined_on: string
  inactive_on: string | null
  created_at: number
  tier_name: string | null
  current_rate: number | null
  arrears_months: number
}

/** One page of GET /api/members (ADR-032 "Lists: paging and search"): 25
 * rows, ascending by name with id as the tiebreak. next_cursor is null once
 * there is no further page; camelCase on this side of the wire boundary. */
export interface MembersPage {
  members: Member[]
  nextCursor: string | null
}

/**
 * GET /api/members (ADR-032 "Lists: paging and search") - one page of the
 * roster, keyset-paged and optionally searched by name. Paging and search are
 * server-side because ADR-032 prohibits filtering a paged list client-side;
 * a caller that needs the whole roster uses listAllMembers.
 *
 * `q` is a case-insensitive substring match on name. `cursor` is a previous
 * page's `nextCursor` - omit it for the first page.
 */
export function listMembersPage(q?: string, cursor?: string): Promise<MembersPage> {
  const params = new URLSearchParams()
  if (q) params.set('q', q)
  if (cursor) params.set('cursor', cursor)
  const query = params.toString()

  return apiFetch<{ members: Member[]; next_cursor: string | null }>(`/api/members${query ? `?${query}` : ''}`).then((page) => ({
    members: page.members,
    nextCursor: page.next_cursor,
  }))
}

/**
 * The fund's whole roster, retired members included (`inactive_on` set), for
 * a picker (RecordPayment's and Reimbursements' member `<Select>`,
 * MemberPicker).
 *
 * A `<Select>` needs every member to resolve whatever it is showing, so it
 * cannot page: this walks listMembersPage's `nextCursor` until null. A caller
 * recording new money filters retired rows out itself, as AccountPicker does
 * for a retired location; a caller reading history needs them.
 */
export async function listAllMembers(): Promise<Member[]> {
  const all: Member[] = []
  let cursor: string | undefined
  for (;;) {
    const page = await listMembersPage(undefined, cursor)
    all.push(...page.members)
    if (page.nextCursor === null) return all
    cursor = page.nextCursor
  }
}

export function createMember(name: string, tierId: number | null, joinedOn: string): Promise<Member> {
  return apiFetch<Member>('/api/members', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, tier_id: tierId, joined_on: joinedOn }),
  })
}

/**
 * PATCH /api/members/{id}. The server distinguishes three cases per field: an
 * **absent key** means "leave alone", an explicit **null** means "clear it"
 * (no tier, or reinstated), and a value sets it. A partial object cannot
 * express that - `{ tierId: null }` and `{}` are indistinguishable once
 * spread - so each field is passed as `undefined` for "leave alone" and the
 * body is built key by key below.
 *
 * Clearing `tier_id` drops the member's dues obligation; clearing
 * `inactive_on` reinstates them. `joined_on` moves but never clears: a fund's
 * history starts at adoption, so the server refuses a null.
 */
export interface MemberPatch {
  name?: string
  tierId?: number | null
  /** "YYYY-MM", from which month `tierId` holds (ADR-040's "Mulai bulan"). Only with `tierId`. */
  tierEffectiveFrom?: string
  joinedOn?: string
  inactiveOn?: string | null
}

export function updateMember(id: number, patch: MemberPatch): Promise<Member> {
  const body: Record<string, unknown> = {}
  if (patch.name !== undefined) body.name = patch.name
  if (patch.tierId !== undefined) body.tier_id = patch.tierId
  if (patch.tierEffectiveFrom !== undefined) body.tier_effective_from = patch.tierEffectiveFrom
  if (patch.joinedOn !== undefined) body.joined_on = patch.joinedOn
  if (patch.inactiveOn !== undefined) body.inactive_on = patch.inactiveOn
  return apiFetch<Member>(`/api/members/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

/**
 * DELETE /api/members/{id} - only for a duplicate with no posted history. A
 * member any transaction references is refused by the foreign key as 409
 * `referenced_by_other_records`, which the roster renders as "sudah punya
 * riwayat - nonaktifkan, bukan hapus".
 */
export function deleteMember(id: number): Promise<void> {
  return apiFetch<void>(`/api/members/${id}`, { method: 'DELETE' })
}
