// Typed call over apiFetch for GET /api/purposes.

import { apiFetch } from '@/lib/api'
import type { PurposeKind } from '@/lib/enums'

/** A purpose tag row (internal/http/purposes.go). `kind` is 'main',
 * 'pass_through' or 'incidental' - the fund always has exactly one 'main'
 * row (purpose_single_main), which is what a new transaction defaults to
 * (PRD section 7.2). */
export interface Purpose {
  id: number
  kind: PurposeKind
  name: string
  created_at: number
}

/**
 * GET /api/purposes - every tag a transaction can carry.
 *
 * `selectable`, when true, asks the server to exclude a closed incidental's
 * purpose (ADR-031) - what the record-transaction picker wants, since
 * PostTransaction would refuse a posting to one. Every other caller
 * (renaming, reporting) wants the unfiltered list: a closed envelope is
 * still history.
 */
export function listPurposes(selectable = false): Promise<Purpose[]> {
  const query = selectable ? '?selectable=true' : ''
  return apiFetch<Purpose[]>(`/api/purposes${query}`)
}

/**
 * POST /api/pass-through-purposes - money the fund holds but does not own,
 * collected for something and paid straight out (PRD section 7.6). Name only:
 * the kind is pinned server-side so no caller can ask for a second 'main'.
 *
 * There is no delete: a posted transaction points at the purpose, and money
 * that passed through is not unsaid. Renaming is another matter - see
 * renamePurpose.
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
 * reads the text, so this rewrites no history.
 *
 * Any purpose but the fund's kas utama may be renamed - that 'main' row is a
 * system row with no treasurer-typed name to have mistyped, so the server
 * answers 409 `purpose_not_renameable` for that kind alone. For an
 * incidental, the server moves the purpose's name and the envelope's occasion
 * together in one transaction.
 */
export function renamePurpose(id: number, name: string): Promise<Purpose> {
  return apiFetch<Purpose>(`/api/purposes/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}
