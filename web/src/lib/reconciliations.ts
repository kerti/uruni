// Typed calls over apiFetch for the /api/reconciliations/* routes: the two
// GETs the home screen reads and POST /api/reconciliations, which the
// reconcile screen builds around (PRD section 7.8).

import { apiFetch } from '@/lib/api'
import type { Direction, Resolution } from '@/lib/enums'

/** One still-open counted line across every reconciliation snapshot - a gap
 * found at a count that hasn't been resolved yet. */
export interface OpenReconciliationLine {
  id: number
  account_id: number
  recorded_amount: number
  actual_amount: number
  difference_amount: number
  resolution: Resolution
  adjustment_transaction_id: number | null
  /** What the line still has open: difference_amount minus its fix, signed
   * like difference_amount. Every "selisih" reads this, never
   * difference_amount, which stays the gap found at the count. */
  remainder_amount: number
  reconciliation_id: number
}

/**
 * GET /api/reconciliations/open-lines - every line still open. An empty fund
 * answers 200 with `[]`, never 404: "cocok" and "nothing open yet" are the
 * same response shape, not two states to branch on.
 */
export function listOpenReconciliationLines(): Promise<OpenReconciliationLine[]> {
  return apiFetch<OpenReconciliationLine[]>('/api/reconciliations/open-lines')
}

/** The most recent reconciliation snapshot taken. */
export interface LatestReconciliation {
  id: number
  performed_at: number
  through_transaction_id: number | null
  note: string | null
  created_at: number
}

/**
 * GET /api/reconciliations/latest - feeds the home screen's "last checked"
 * line. Before any snapshot exists this answers 404 `not_found` - a normal
 * first-run state, not an error. Callers check `err.code === 'not_found'`
 * (ApiError carries no HTTP status).
 */
export function getLatestReconciliation(): Promise<LatestReconciliation> {
  return apiFetch<LatestReconciliation>('/api/reconciliations/latest')
}

/**
 * One fix's data (fixRequest), camelCase on this side of the wire boundary.
 * Required when a count's resolution is "adjusted" or "entry_added", absent
 * for "matched" and "left_open" (TakeReconciliation's rule,
 * internal/ledger/reconciliation.go:329-336). An optional field here (never
 * `fix: null`) so JSON.stringify drops the key for those two resolutions
 * rather than sending an explicit null.
 */
export interface FixInput {
  purposeId: number
  direction: Direction
  amount: number
  occurredOn: string
  note: string | null
}

/**
 * One counted account within POST /api/reconciliations's body. The caller
 * decides `resolution` from a client-side preview against GET /api/balances;
 * TakeReconciliation is the sole judge of whether a claimed "matched" is real
 * (internal/ledger/reconciliation.go:185-188).
 */
export interface AccountCountInput {
  accountId: number
  actualAmount: number
  resolution: Resolution
  fix?: FixInput
}

/** One line of POST /api/reconciliations's response - what the server
 * recorded for one counted account. The confirmation state renders from
 * this, never from its own pre-submit preview. */
export interface ReconciliationLine {
  id: number
  account_id: number
  recorded_amount: number
  actual_amount: number
  difference_amount: number
  resolution: Resolution
  adjustment_transaction_id: number | null
  /** See OpenReconciliationLine.remainder_amount. 0 means closed. */
  remainder_amount: number
}

/**
 * What a Cek kas line would still have open if saved with this fix: the
 * line's difference (actual - recorded) minus the fix's signed amount, "in"
 * positive and "out" negative - the same arithmetic the server does, for the
 * live "masih tersisa" line. Plain integers.
 */
export function fixRemainder(difference: number, direction: Direction, amount: number): number {
  return difference - (direction === 'in' ? amount : -amount)
}

/** POST /api/reconciliations's 201 body - the same shape as
 * GET /api/reconciliations/{id}: the snapshot plus every line it froze. */
export interface ReconciliationDetail {
  id: number
  performed_at: number
  through_transaction_id: number | null
  note: string | null
  created_at: number
  lines: ReconciliationLine[]
}

/** One row of GET /api/reconciliations's page: the snapshot plus
 * open_difference_amount, the sum of ABS(difference_amount) over its lines -
 * the remainder each still has open, not the gap found - enough for Cek
 * kas's list to show cocok (0) vs selisih without a detail fetch. */
export interface ReconciliationListItem {
  id: number
  performed_at: number
  through_transaction_id: number | null
  note: string | null
  created_at: number
  open_difference_amount: number
}

/** One page of GET /api/reconciliations, camelCase on this side of the wire
 * boundary. nextCursor is null once there is no further page. */
export interface ReconciliationsPage {
  reconciliations: ReconciliationListItem[]
  nextCursor: string | null
}

/**
 * GET /api/reconciliations (keyset-paged): newest-first, 25 rows a page. No
 * search - a handful of dated snapshots a year (ADR-032).
 */
export function listReconciliations(input: { cursor?: string } = {}): Promise<ReconciliationsPage> {
  const params = new URLSearchParams()
  if (input.cursor) params.set('cursor', input.cursor)
  const query = params.toString()

  return apiFetch<{ reconciliations: ReconciliationListItem[]; next_cursor: string | null }>(
    `/api/reconciliations${query ? `?${query}` : ''}`,
  ).then((page) => ({ reconciliations: page.reconciliations, nextCursor: page.next_cursor }))
}

/**
 * GET /api/reconciliations/{id} - one snapshot's full detail. Cek kas's
 * detail sheet only reads from this; the tab is read-only.
 */
export function getReconciliation(id: number): Promise<ReconciliationDetail> {
  return apiFetch<ReconciliationDetail>(`/api/reconciliations/${id}`)
}

/**
 * POST /api/reconciliations - takes one snapshot across every counted
 * account in a single request (PRD section 7.8). No performed_at param: the
 * server always stamps time.Now() - a reconciliation is never backdated. The
 * server creates each fix's transaction itself from the raw fix data; this
 * call never posts a separate transaction.
 */
export function takeReconciliation(note: string | null, counts: AccountCountInput[]): Promise<ReconciliationDetail> {
  return apiFetch<ReconciliationDetail>('/api/reconciliations', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      note,
      counts: counts.map((c) => ({
        account_id: c.accountId,
        actual_amount: c.actualAmount,
        resolution: c.resolution,
        fix: c.fix
          ? {
              purpose_id: c.fix.purposeId,
              direction: c.fix.direction,
              amount: c.fix.amount,
              occurred_on: c.fix.occurredOn,
              note: c.fix.note,
            }
          : undefined,
      })),
    }),
  })
}
