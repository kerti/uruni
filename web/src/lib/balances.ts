// Typed call over apiFetch for GET /api/balances.

import { apiFetch } from '@/lib/api'

/** One account's (location's) share of the fund total within GET
 * /api/balances. No `inactive_on` here, so this row can't say whether the
 * location is retired; a retired location can hold a nonzero balance from
 * before retirement, so the home screen renders every row returned, in the
 * order returned. */
export interface AccountBalance {
  id: number
  kind: string
  name: string
  balance: number
}

/** One purpose tag's running total within GET /api/balances - not rendered
 * by the home screen, but part of the same response body. */
export interface PurposeBalance {
  id: number
  kind: string
  name: string
  balance: number
}

/** GET /api/balances's body: the fund's one pooled total plus its
 * account/purpose breakdown. */
export interface Balances {
  fund_total: number
  accounts: AccountBalance[]
  purposes: PurposeBalance[]
  /** Unsettled, unwaived talangan claims summed on the server - already
   * inside fund_total, never subtracted from it. */
  owed_to_members: number
  /** The running month's money in and out, read in Asia/Jakarta by the same
   * ledger function the public report uses (ADR-038). In is net of reversals
   * and can be negative; out is a magnitude. */
  month: { in: number; out: number }
}

/** GET /api/balances - the home screen's balance hero and per-location rows
 * in one round trip. */
export function getBalances(): Promise<Balances> {
  return apiFetch<Balances>('/api/balances')
}
