// Typed call over apiFetch for GET /api/purposes (M6.8), same idiom as
// lib/accounts.ts.

import { apiFetch } from '@/lib/api'

/** A purpose tag row (internal/http/purposes.go). `kind` is 'main',
 * 'pass_through' or 'incidental' - the fund always has exactly one 'main'
 * row (the schema's own purpose_single_main), which is what a new
 * transaction defaults to (PRD section 7.2). */
export interface Purpose {
  id: number
  kind: string
  name: string
  created_at: number
}

/**
 * GET /api/purposes - every tag a transaction can carry.
 *
 * `selectable`, when true, asks the server to exclude a closed incidental's
 * purpose (ADR-031) - what the everyday record-transaction picker wants,
 * since PostTransaction's own guard would now refuse a posting to one.
 * Every other caller (renaming, reporting) still wants the unfiltered list:
 * a closed envelope is still history.
 */
export function listPurposes(selectable = false): Promise<Purpose[]> {
  const query = selectable ? '?selectable=true' : ''
  return apiFetch<Purpose[]>(`/api/purposes${query}`)
}

/**
 * POST /api/pass-through-purposes - money the fund holds but does not own,
 * collected for something and paid straight out (PRD section 7.6). Name only: the
 * kind is pinned server-side so no caller can ask for a second 'main'.
 *
 * There is no delete to pair with this: a posted transaction points at the
 * purpose, and money that passed through is not unsaid. Renaming is another
 * matter - see renamePurpose.
 */
export function createPassThroughPurpose(name: string): Promise<Purpose> {
  return apiFetch<Purpose>('/api/pass-through-purposes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

/**
 * PATCH /api/purposes/{id} - fixes a mistyped name. The name is a label: a
 * posted transaction references the purpose by id and nothing in the ledger
 * reads the text, so this rewrites no history, exactly like renaming a
 * location.
 *
 * Any purpose but the fund's own kas utama may be renamed (#264) - the
 * fund's own 'main' row is a system row with no treasurer-typed name to
 * have mistyped, so the server answers 409 `purpose_not_renameable` for
 * that one kind alone. For an incidental, the server moves both the
 * purpose's name and the envelope's own occasion together in one
 * transaction; this call looks identical either way.
 */
export function renamePurpose(id: number, name: string): Promise<Purpose> {
  return apiFetch<Purpose>(`/api/purposes/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}
