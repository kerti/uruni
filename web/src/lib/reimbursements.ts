// Typed calls over apiFetch for the reimbursement routes
// (internal/http/reimbursements.go).

import { apiFetch } from '@/lib/api'
import type { Transaction } from '@/lib/setup'

/** A reimbursement claim row. Off-ledger until settled: the balance still
 * matches the wallet while a claim is outstanding (ADR-024). `settled` says a
 * kind='reimbursement' payout has posted for it - computed by the list
 * queries, so an "all" list renders the same claim honestly. */
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
  /** Every receipt attached to this claim, oldest first. Optional for the
   * same reason as lib/setup.ts's Transaction.receipt_ids. Reimbursements.tsx
   * reads it as `claim.receipt_ids ?? []`. */
  receipt_ids?: number[]
}

/** GET /api/reimbursements's optional query parameters (ADR-032 "Lists:
 * paging and search"). cursor is a previous page's nextCursor - omit it for
 * the first page. q searches member name and note only: a claim has no
 * purpose-name column worth searching. */
export interface ListReimbursementsInput {
  outstanding?: boolean
  q?: string
  cursor?: string
}

/** One page of GET /api/reimbursements, camelCase on this side of the wire
 * boundary. nextCursor is null once there is no further page. */
export interface ReimbursementsPage {
  reimbursements: Reimbursement[]
  nextCursor: string | null
}

/**
 * GET /api/reimbursements (keyset-paged, searchable): newest-first, 25 rows a
 * page.
 */
export function listReimbursements(input: ListReimbursementsInput = {}): Promise<ReimbursementsPage> {
  const params = new URLSearchParams()
  if (input.outstanding) params.set('outstanding', 'true')
  if (input.q) params.set('q', input.q)
  if (input.cursor) params.set('cursor', input.cursor)
  const query = params.toString()

  return apiFetch<{ reimbursements: Reimbursement[]; next_cursor: string | null }>(`/api/reimbursements${query ? `?${query}` : ''}`).then(
    (page) => ({ reimbursements: page.reimbursements, nextCursor: page.next_cursor }),
  )
}

/**
 * GET /api/reimbursements/{id}: one claim, with the same `settled` flag and
 * receipt_ids a list row carries. The settle and correct screens open on a
 * claim by address (`?id=`), so a reload or stale link needs a lookup that
 * does not depend on which list page the claim sits in. An id naming nothing
 * is a 404 (ApiError code not_found).
 */
export function getReimbursement(id: number): Promise<Reimbursement> {
  return apiFetch<Reimbursement>(`/api/reimbursements/${id}`)
}

/** POST /api/reimbursements - a direct-CRUD write that moves no money:
 * recording a claim posts nothing to the ledger. */
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
 * waived_on means "clear it". The body is built key by key so the wire never
 * sees undefined values.
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
export function settleReimbursement(id: number, input: { account_id: number; occurred_on: string }): Promise<Transaction> {
  return apiFetch<Transaction>(`/api/reimbursements/${id}/settle`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}
