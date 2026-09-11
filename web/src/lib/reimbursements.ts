// Typed calls over apiFetch for the reimbursement routes (M6.18), same idiom
// as lib/accounts.ts. The backend is already complete (internal/http/
// reimbursements.go); this module is the frontend's typed surface over it.

import { apiFetch } from '@/lib/api'
import type { Transaction } from '@/lib/setup'

/** A reimbursement claim row (internal/http/reimbursements.go). off-ledger
 * until settled: the balance still matches the wallet while a claim is
 * outstanding (ADR-024). `settled` says a kind='reimbursement' payout has
 * posted for it - a fact the list queries compute, so an "all" list can
 * render the same claim honestly that the outstanding list answers directly. */
export interface Reimbursement {
  id: number
  member_id: number
  purpose_id: number
  amount: number
  incurred_on: string
  waived_on: string | null
  settled: boolean
  note: string | null
  created_at: number
}

/** GET /api/reimbursements's optional query parameters (#226, ADR-032
 * "Lists: paging and search"). cursor is the opaque string a previous
 * page's nextCursor returned - omit it for the first page. q searches
 * member name and note only, narrower than GET /api/transactions's search
 * surface because a claim has no purpose-name column worth searching. */
export interface ListReimbursementsInput {
  outstanding?: boolean
  q?: string
  cursor?: string
}

/** One page of GET /api/reimbursements, camelCase on this side of the wire
 * boundary (the server's own envelope is {reimbursements, next_cursor}).
 * nextCursor is null once there is no further page. */
export interface ReimbursementsPage {
  reimbursements: Reimbursement[]
  nextCursor: string | null
}

/**
 * GET /api/reimbursements (#226, keyset-paged and searchable): newest-first,
 * 25 rows a page. An unparseable ?outstanding= value is a 400 on the
 * server; this module never sends one.
 */
export function listReimbursements(input: ListReimbursementsInput = {}): Promise<ReimbursementsPage> {
  const params = new URLSearchParams()
  if (input.outstanding) params.set('outstanding', 'true')
  if (input.q) params.set('q', input.q)
  if (input.cursor) params.set('cursor', input.cursor)
  const query = params.toString()

  return apiFetch<{ reimbursements: Reimbursement[]; next_cursor: string | null }>(
    `/api/reimbursements${query ? `?${query}` : ''}`,
  ).then((page) => ({ reimbursements: page.reimbursements, nextCursor: page.next_cursor }))
}

/** POST /api/reimbursements - a direct-CRUD write that moves no money.
 * Recording a claim posts nothing to the ledger; the outstanding list shows
 * it immediately. */
export function createReimbursement(input: {
  member_id: number
  purpose_id: number
  amount: number
  incurred_on: string
  note: string | null
}): Promise<Reimbursement> {
  return apiFetch<Reimbursement>('/api/reimbursements', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

/**
 * PATCH /api/reimbursements/{id} - correcting a claim or waiving/un-waiving
 * it. An absent key means "leave alone"; an explicit null on note or
 * waived_on means "clear it". The body is built key by key so the wire
 * never sees undefined values.
 */
export function updateReimbursement(
  id: number,
  patch: {
    member_id?: number
    purpose_id?: number
    amount?: number
    incurred_on?: string
    note?: string | null
    waived_on?: string | null
  },
): Promise<Reimbursement> {
  const body: Record<string, unknown> = {}
  if (patch.member_id !== undefined) body.member_id = patch.member_id
  if (patch.purpose_id !== undefined) body.purpose_id = patch.purpose_id
  if (patch.amount !== undefined) body.amount = patch.amount
  if (patch.incurred_on !== undefined) body.incurred_on = patch.incurred_on
  if (patch.note !== undefined) body.note = patch.note
  if (patch.waived_on !== undefined) body.waived_on = patch.waived_on
  return apiFetch<Reimbursement>(`/api/reimbursements/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

/**
 * DELETE /api/reimbursements/{id} - for a claim that should never have
 * existed. A settled claim is refused by the server as 409.
 */
export function deleteReimbursement(id: number): Promise<void> {
  return apiFetch<void>(`/api/reimbursements/${id}`, { method: 'DELETE' })
}

/**
 * POST /api/reimbursements/{id}/settle - pays out a claim. Amount and
 * purpose come from the claim itself; the caller provides only which
 * account pays and when. Returns the posted transaction row.
 */
export function settleReimbursement(
  id: number,
  input: { account_id: number; occurred_on: string },
): Promise<Transaction> {
  return apiFetch<Transaction>(`/api/reimbursements/${id}/settle`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}
