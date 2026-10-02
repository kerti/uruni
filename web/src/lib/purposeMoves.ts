// Typed call over apiFetch for POST /api/purpose-moves, same idiom as
// lib/transfers.ts.

import { apiFetch } from '@/lib/api'
import type { Transfer } from '@/lib/transfers'

export interface PostPurposeMoveInput {
  fromPurposeId: number
  toPurposeId: number
  accountId: number
  amount: number
  occurredOn: string
  note?: string | null
}

/**
 * POST /api/purpose-moves (ADR-036, #383) - money that stays where it is but
 * changes what it is for: from Kas Utama to an envelope, back again, or from
 * one envelope to another. Nothing leaves the fund or any location, so no
 * balance and no reconciliation figure moves; only the two purposes' balances
 * do, by exactly the amount.
 *
 * `accountId` is the one location both legs post on. It is asked for rather
 * than chosen silently because the row names it (the close-envelope form's
 * precedent).
 *
 * The note reaches both legs or neither, the same contract as a transfer's.
 */
export function postPurposeMove(input: PostPurposeMoveInput): Promise<Transfer> {
  return apiFetch<Transfer>('/api/purpose-moves', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      from_purpose_id: input.fromPurposeId,
      to_purpose_id: input.toPurposeId,
      account_id: input.accountId,
      amount: input.amount,
      occurred_on: input.occurredOn,
      note: input.note ?? null,
    }),
  })
}
