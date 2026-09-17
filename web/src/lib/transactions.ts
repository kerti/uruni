// Typed call over apiFetch for POST /api/transactions (M6.8), same idiom as
// lib/setup.ts's postSetup. The Transaction response shape is already
// defined in lib/setup.ts - re-exported here rather than redefined.

import { apiFetch } from '@/lib/api'
import type { Transaction } from '@/lib/setup'

export type { Transaction }

/** POST /api/transactions's body (internal/http/transactions.go's
 * transactionRequest), camelCase on this side of the wire boundary. */
export interface CreateTransactionInput {
  accountId: number
  purposeId: number
  direction: 'in' | 'out'
  amount: number
  occurredOn: string
  note: string | null
}

/**
 * POST /api/transactions - one ordinary entry (kind='normal').
 * is_adjustment is always false here: M6.10's reconcile flow is the only
 * caller that ever posts kind='adjustment', through its own request.
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
    }),
  })
}

/** GET /api/transactions's optional query parameters (#225, ADR-032
 * "Lists: paging and search"). cursor is the opaque string a previous
 * page's nextCursor returned - omit it for the first page. memberId/
 * duesPeriod are exact-match filters for Dues/MemberPayments.tsx alone,
 * not a Riwayat UI filter (ADR-032 holds filters to M7) - there is
 * deliberately no copy or UI surface naming them.
 *
 * purposeId is the one that does have a UI surface (#262): ADR-032 makes
 * Riwayat -> Transaksi filtered to a purpose the only route to a closed
 * envelope, so History/Transactions.tsx renders it from `?purpose=<id>` as
 * a filter it can clear. Still not a chooser it offers - that is M7's
 * filter set. All of these narrow the same list `q` searches. */
export interface ListTransactionsInput {
  cursor?: string
  q?: string
  memberId?: number
  duesPeriod?: string
  purposeId?: number
}

/** One page of GET /api/transactions, camelCase on this side of the wire
 * boundary (the server's own envelope is {transactions, next_cursor}).
 * nextCursor is null once there is no further page. */
export interface TransactionsPage {
  transactions: Transaction[]
  nextCursor: string | null
}

/**
 * GET /api/transactions (M6.9, keyset-paged and searchable as of #225):
 * newest-first, 25 rows a page (internal/http/transactions.go's
 * listTransactions). Every caller in this codebase reads only the first
 * page today - Home and Reconcile take their first five rows straight off
 * it (no more `.slice(-5).reverse()`, the server's own order is already
 * what they want), History/Transactions renders the page as-is, and
 * MemberPayments passes memberId/duesPeriod instead of filtering
 * client-side. Building a "load more" loop over nextCursor is the
 * orchestrator's next slice, not this one's.
 */
export function listTransactions(input: ListTransactionsInput = {}): Promise<TransactionsPage> {
  const params = new URLSearchParams()
  if (input.cursor) params.set('cursor', input.cursor)
  if (input.q) params.set('q', input.q)
  if (input.memberId !== undefined) params.set('member_id', String(input.memberId))
  if (input.duesPeriod) params.set('dues_period', input.duesPeriod)
  if (input.purposeId !== undefined) params.set('purpose_id', String(input.purposeId))
  const query = params.toString()

  return apiFetch<{ transactions: Transaction[]; next_cursor: string | null }>(`/api/transactions${query ? `?${query}` : ''}`).then(
    (page) => ({ transactions: page.transactions, nextCursor: page.next_cursor }),
  )
}

/**
 * The text a row's note line shows, for TransactionList.tsx (#257). A
 * settlement's (kind='reimbursement') own Note is always nil server-side -
 * nothing generated is ever stored - so its "note" is the settled claim's
 * own note instead; every other row shows what she actually typed on the
 * row itself.
 */
export function noteForDisplay(transaction: Transaction): string | null {
  if (transaction.kind === 'reimbursement') {
    return transaction.claim_note ?? null
  }
  return transaction.note
}

/**
 * POST /api/transactions/{id}/purpose-correction (ADR-033, #267) - moves a
 * posted row's peruntukan to the tag it should always have had, by posting
 * a value-neutral reclass_purpose pair on the row's own account and date.
 *
 * One field, deliberately: the amount, the account and the date are read
 * off the row server-side and cannot be sent, which is what keeps this a
 * correction of that row rather than an edit of an immutable entry. The
 * server also decides the pair's legs from the row's EFFECTIVE peruntukan,
 * so correcting a row that was already corrected moves money out of the
 * tag that actually holds it.
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
 * Whether this row's peruntukan may be corrected at all (ADR-033's
 * eligibility list). The server refuses the rest with a named 409 either
 * way; this is what decides whether the row offers the control, so an
 * ineligible row renders its peruntukan as plain text rather than a dead
 * tap.
 *
 * kind='adjustment' is eligible EXCEPT a dues reversal, whose peruntukan
 * must track the dues row it reverses (ADR-029).
 */
export function canCorrectPurpose(transaction: Transaction): boolean {
  if (transaction.kind === 'normal') return true
  return transaction.kind === 'adjustment' && transaction.reverses_transaction_id === null
}
