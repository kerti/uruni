// Typed calls over apiFetch for the two GET /api/reconciliations/* routes
// the home screen reads (M6.9), same idiom as lib/accounts.ts and
// lib/balances.ts. POST /api/reconciliations itself is M6.10's reconcile
// flow, not this slice's.

import { apiFetch } from '@/lib/api'

/** One still-open counted line across every reconciliation snapshot
 * (internal/http/reconciliations.go's openReconciliationLineResponse) - a
 * gap found at a count that hasn't been resolved yet. */
export interface OpenReconciliationLine {
  id: number
  account_id: number
  recorded_amount: number
  actual_amount: number
  difference_amount: number
  resolution: string
  adjustment_transaction_id: number | null
  reconciliation_id: number
}

/**
 * GET /api/reconciliations/open-lines - every line still open. An empty
 * fund answers 200 with `[]`, never 404: the reconciliation banner's "cocok"
 * state and "nothing open yet" are the same response shape, not two states
 * to branch on here.
 */
export function listOpenReconciliationLines(): Promise<OpenReconciliationLine[]> {
  return apiFetch<OpenReconciliationLine[]>('/api/reconciliations/open-lines')
}

/** The most recent reconciliation snapshot taken
 * (internal/http/reconciliations.go's reconciliationResponse). */
export interface LatestReconciliation {
  id: number
  performed_at: number
  through_transaction_id: number | null
  note: string | null
  created_at: number
}

/**
 * GET /api/reconciliations/latest - feeds the home screen's "last checked"
 * line. Before any snapshot has ever been taken this answers 404 with code
 * `not_found` (latestReconciliation's own doc comment) - a normal first-run
 * state, not an error, and the only way to tell it apart from every other
 * `not_found` this app can hit: callers check `err.code === 'not_found'`
 * (ApiError carries no HTTP status).
 */
export function getLatestReconciliation(): Promise<LatestReconciliation> {
  return apiFetch<LatestReconciliation>('/api/reconciliations/latest')
}
