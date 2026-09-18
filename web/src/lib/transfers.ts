// Typed call over apiFetch for POST /api/transfers, same idiom as
// lib/transactions.ts.

import { apiFetch } from '@/lib/api'

/** The transfer row POST /api/transfers answers with. The two legs it binds
 * are ordinary transactions, already readable through GET /api/transactions,
 * which is why there is no GET /api/transfers to pair with this. */
export interface Transfer {
  id: number
  kind: string
  created_at: number
}

export interface PostTransferInput {
  purposeId: number
  fromAccountId: number
  toAccountId: number
  amount: number
  occurredOn: string
  note?: string | null
}

/**
 * POST /api/transfers (#235) - money moving between two locations, cash
 * deposited at the bank or drawn back out (PRD section 6). The fund's total
 * is unchanged by construction: one amount, two opposite legs.
 *
 * `purposeId` is singular deliberately, and the record form does not ask for
 * it: both legs carry the same purpose, so every purpose balance nets to
 * zero whichever one it is - a location transfer never changes what the
 * money is for (ADR-024), only where it sits. The form sends the fund's own
 * Kas Utama row.
 *
 * The note reaches both legs or neither (#213): one movement is one thing
 * that happened, and a note on the "out" leg alone reads, in the transaction
 * list, as an unexplained arrival somewhere else.
 */
export function postTransfer(input: PostTransferInput): Promise<Transfer> {
  return apiFetch<Transfer>('/api/transfers', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      purpose_id: input.purposeId,
      from_account_id: input.fromAccountId,
      to_account_id: input.toAccountId,
      amount: input.amount,
      occurred_on: input.occurredOn,
      note: input.note ?? null,
    }),
  })
}
