// Typed call over apiFetch for GET /api/purposes (M6.8), same idiom as
// lib/accounts.ts.

import { apiFetch } from '@/lib/api'

/** A purpose tag row (internal/http/purposes.go). `kind` is 'main',
 * 'pass_through' or 'incidental' - the fund always has exactly one 'main'
 * row (the schema's own purpose_single_main), which is what a new
 * transaction defaults to (PRD §7.2). */
export interface Purpose {
  id: number
  kind: string
  name: string
  created_at: number
}

/** GET /api/purposes - every tag a transaction can carry. */
export function listPurposes(): Promise<Purpose[]> {
  return apiFetch<Purpose[]>('/api/purposes')
}
