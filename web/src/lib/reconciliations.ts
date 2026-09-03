// Typed calls over apiFetch for the /api/reconciliations/* routes: the two
// GETs the home screen reads (M6.9) and POST /api/reconciliations, the one
// call M6.10's reconcile screen builds around (PRD §7.8) - same idiom as
// lib/accounts.ts and lib/balances.ts throughout.

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

/**
 * One fix's data (internal/http/reconciliations.go's fixRequest), camelCase
 * on this side of the wire boundary - same convention lib/transactions.ts's
 * CreateTransactionInput uses. Required when a count's resolution is
 * "adjusted" or "entry_added", absent for "matched" and "left_open"
 * (TakeReconciliation's own rule, internal/ledger/reconciliation.go:329-336).
 * Modelled as an optional field here (never `fix: null`) so
 * JSON.stringify drops the key entirely for those two resolutions rather
 * than sending an explicit null the server would have to also accept.
 */
export interface FixInput {
  purposeId: number
  direction: 'in' | 'out'
  amount: number
  occurredOn: string
  note: string | null
}

/**
 * One counted account within POST /api/reconciliations's body
 * (accountCountRequest). The caller decides `resolution` itself, from a
 * client-side preview against GET /api/balances - this module has no
 * opinion on whether that choice is correct; TakeReconciliation is the sole
 * judge of whether a claimed "matched" is real
 * (internal/ledger/reconciliation.go:185-188).
 */
export interface AccountCountInput {
  accountId: number
  actualAmount: number
  resolution: 'matched' | 'entry_added' | 'adjusted' | 'left_open'
  fix?: FixInput
}

/** One line of POST /api/reconciliations's response
 * (reconciliationLineResponse) - what the server actually recorded for one
 * counted account. The reconcile screen's confirmation state renders from
 * this, never from its own pre-submit preview. */
export interface ReconciliationLine {
  id: number
  account_id: number
  recorded_amount: number
  actual_amount: number
  difference_amount: number
  resolution: string
  adjustment_transaction_id: number | null
}

/** POST /api/reconciliations's 201 body (reconciliationDetailResponse) -
 * the same shape GET /api/reconciliations/{id} answers with: the snapshot
 * plus every line it froze, in one round trip. */
export interface ReconciliationDetail {
  id: number
  performed_at: number
  through_transaction_id: number | null
  note: string | null
  created_at: number
  lines: ReconciliationLine[]
}

/**
 * POST /api/reconciliations - takes one snapshot across every counted
 * account in a single request (M6.10, PRD §7.8). No performed_at param:
 * the server always stamps time.Now() (TakeReconciliationParams's own doc
 * comment - a reconciliation is something done right now, never backdated).
 * The server creates each fix's transaction itself from the raw fix data;
 * this call never posts a separate transaction or supplies a transaction id.
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
