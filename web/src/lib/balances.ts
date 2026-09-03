// Typed call over apiFetch for GET /api/balances (M6.9), same idiom as
// lib/accounts.ts and lib/purposes.ts.

import { apiFetch } from '@/lib/api'

/** One account's (location's) share of the fund total within GET
 * /api/balances (internal/http/balances.go's accountBalanceResponse). No
 * `inactive_on` here - unlike lib/accounts.ts's Account, this row can't say
 * whether the location is retired; ListAccountsByFund still returns it (a
 * retired location can hold a nonzero balance from before retirement), and
 * the home screen renders every row this endpoint returns, in the order
 * returned. */
export interface AccountBalance {
  id: number
  kind: string
  name: string
  balance: number
}

/** One purpose tag's running total within GET /api/balances
 * (purposeBalanceResponse) - not rendered by the home screen (M6.9), but
 * part of the same response body. */
export interface PurposeBalance {
  id: number
  kind: string
  name: string
  balance: number
}

/** GET /api/balances's body: the fund's one pooled total plus its
 * account/purpose breakdown (internal/http/balances.go's
 * balancesResponse). */
export interface Balances {
  fund_total: number
  accounts: AccountBalance[]
  purposes: PurposeBalance[]
}

/** GET /api/balances - the home screen's balance hero and per-location rows
 * in one round trip. */
export function getBalances(): Promise<Balances> {
  return apiFetch<Balances>('/api/balances')
}
