// Typed call over apiFetch for POST /api/transactions (M6.8), same idiom as
// lib/setup.ts's postOpeningBalance. The Transaction response shape is
// already defined in lib/setup.ts (the wizard's opening-balance call answers
// with the same row shape) - re-exported here rather than redefined.

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
