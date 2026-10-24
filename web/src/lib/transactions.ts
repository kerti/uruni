// Typed call over apiFetch for POST /api/transactions. The Transaction
// response shape is defined in lib/setup.ts - re-exported here.

import { apiFetch } from '@/lib/api'
import type { Transaction } from '@/lib/setup'
import type { Direction } from '@/lib/enums'

export type { Transaction }

/** POST /api/transactions's body (transactionRequest), camelCase on this side
 * of the wire boundary. */
export interface CreateTransactionInput {
  accountId: number
  purposeId: number
  direction: Direction
  amount: number
  occurredOn: string
  note: string | null
  /** Who a contribution into an open envelope is from (ADR-034) - Catat's
   * "Dari siapa? (opsional)". Omitted or null for every other posting. */
  memberId?: number | null
}

/**
 * POST /api/transactions - one ordinary entry (kind='normal').
 * is_adjustment is always false here: the reconcile flow is the only caller
 * that posts kind='adjustment', through its own request.
 */
export function createTransaction(input: CreateTransactionInput): Promise<Transaction> {
  return apiFetch<Transaction>('/api/transactions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      account_id: input.accountId,
      purpose_id: input.purposeId,
      direction: input.direction,
      amount: input.amount,
      occurred_on: input.occurredOn,
      note: input.note,
      is_adjustment: false,
      member_id: input.memberId ?? null,
    }),
  })
}

/** GET /api/transactions's optional query parameters (ADR-032 "Lists: paging
 * and search"). cursor is a previous page's nextCursor - omit it for the
 * first page. memberId/duesPeriod are exact-match filters for
 * Dues/MemberPayments.tsx alone, not a Riwayat UI filter (ADR-032 holds
 * filters to M7) - there is deliberately no copy or UI surface naming them.
 *
 * purposeId does have a UI surface: ADR-032 makes Riwayat -> Transaksi
 * filtered to a purpose the only route to a closed envelope, so
 * History/Transactions.tsx renders it from `?purpose=<id>` as a filter it
 * can clear, but offers no chooser. All of these narrow the same list `q`
 * searches. */
export interface ListTransactionsInput {
  cursor?: string
  q?: string
  memberId?: number
  duesPeriod?: string
  purposeId?: number
  /** "YYYY-MM" - the rows dated in that month. */
  month?: string
  direction?: Direction
}

/** One page of GET /api/transactions, camelCase on this side of the wire
 * boundary. nextCursor is null once there is no further page. */
export interface TransactionsPage {
  transactions: Transaction[]
  nextCursor: string | null
}

/**
 * GET /api/transactions (keyset-paged, searchable): newest-first, 25 rows a
 * page. Home and Reconcile take their first five rows off the first page,
 * MemberPayments passes memberId/duesPeriod instead of filtering
 * client-side, and History/Transactions follows nextCursor for "load more".
 */
export function listTransactions(input: ListTransactionsInput = {}): Promise<TransactionsPage> {
  const params = new URLSearchParams()
  if (input.cursor) params.set('cursor', input.cursor)
  if (input.q) params.set('q', input.q)
  if (input.memberId !== undefined) params.set('member_id', String(input.memberId))
  if (input.duesPeriod) params.set('dues_period', input.duesPeriod)
  if (input.purposeId !== undefined) params.set('purpose_id', String(input.purposeId))
  if (input.month) params.set('month', input.month)
  if (input.direction) params.set('direction', input.direction)
  const query = params.toString()

  return apiFetch<{ transactions: Transaction[]; next_cursor: string | null }>(`/api/transactions${query ? `?${query}` : ''}`).then(
    (page) => ({ transactions: page.transactions, nextCursor: page.next_cursor }),
  )
}

/**
 * The text a row's note line shows. A settlement's (kind='reimbursement')
 * Note is always nil server-side - nothing generated is ever stored - so its
 * "note" is the settled claim's note; every other row shows what she typed.
 */
export function noteForDisplay(transaction: Transaction): string | null {
  if (transaction.kind === 'reimbursement') {
    return transaction.claim_note ?? null
  }
  return transaction.note
}

/**
 * POST /api/transactions/{id}/reversal - undo a dues payment or a normal
 * transaction recorded in error (PRD section 7.3, ADR-042). {id} is the row
 * being reversed.
 *
 * Deliberately narrow: only a note is sent. The ledger copies amount, location,
 * purpose (the effective one), member and period from the original and picks the date
 * itself: the original's own day, or today once a cek kas has counted it. A new
 * linked entry is posted (CLAUDE.md rule 3).
 *
 * `note` holds only what the treasurer typed; blank sends null, never a generated default.
 */
export function reverseTransaction(transactionId: number, note: string | null): Promise<Transaction> {
  return apiFetch<Transaction>(`/api/transactions/${transactionId}/reversal`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ note }),
  })
}

/**
 * POST /api/transactions/{id}/purpose-correction (ADR-033) - moves a posted
 * row's pos to the tag it should always have had, by posting a value-neutral
 * reclass_purpose pair on the row's account and date.
 *
 * One field, deliberately: the amount, account and date are read off the row
 * server-side and cannot be sent, which keeps this a correction of that row
 * rather than an edit of an immutable entry. The server decides the pair's
 * legs from the row's EFFECTIVE pos, so correcting an already-corrected row
 * moves money out of the tag that actually holds it.
 *
 * Answers the transfer row. Every refusal is a named 409 the caller maps
 * through copy.common.errors (purpose_correction_*).
 */
export function correctPurpose(transactionId: number, purposeId: number): Promise<{ id: number; kind: string; created_at: number }> {
  return apiFetch(`/api/transactions/${transactionId}/purpose-correction`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ purpose_id: purposeId }),
  })
}

/**
 * Whether this row's pos may be corrected at all (ADR-033's eligibility
 * list). The server refuses the rest with a named 409; this decides whether
 * the row offers the control, so an ineligible row renders its pos as plain
 * text rather than a dead tap.
 *
 * kind='adjustment' is eligible EXCEPT a reversal, which copies its original's
 * effective pos (ADR-029, ADR-042).
 *
 * A named contribution (kind='normal' with member_id set) is excluded too
 * (ADR-034): moving it would leave participation counting the member against
 * an envelope the money has left. So is a row something already reverses
 * (ADR-042), on the server's `reversed` flag, even on an unfetched page: the
 * ledger refuses reverse-then-correct.
 */
export function canCorrectPurpose(transaction: Transaction): boolean {
  if (transaction.reversed) return false
  if (transaction.kind === 'normal') return transaction.member_id === null
  return transaction.kind === 'adjustment' && transaction.reverses_transaction_id === null
}
