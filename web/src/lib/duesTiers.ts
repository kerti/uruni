// Typed calls over apiFetch for dues tiers and their rates: /api/dues-tiers
// and /api/dues-rates (internal/http dues_tiers.go, dues_rates.go).

import { apiFetch } from '@/lib/api'

export interface DuesTier {
  id: number
  name: string
  created_at: number
}

export interface DuesRate {
  id: number
  tier_id: number
  amount: number
  effective_from: string
  created_at: number
}

/** A row of GET /api/dues-tiers: the tier with its rate history, oldest month
 * first. An empty `rates` is a legal state: a tier whose price is not decided
 * yet. */
export interface DuesTierWithRates extends DuesTier {
  rates: DuesRate[]
}

export function createDuesTier(name: string): Promise<DuesTier> {
  return apiFetch<DuesTier>('/api/dues-tiers', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

export function createDuesRate(tierId: number, amount: number, effectiveFrom: string): Promise<DuesRate> {
  return apiFetch<DuesRate>(`/api/dues-tiers/${tierId}/rates`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ amount, effective_from: effectiveFrom }),
  })
}

/** GET /api/dues-tiers - the fund's tiers, each with its rate history, in
 * one request however many tiers there are. */
export function listDuesTiers(): Promise<DuesTierWithRates[]> {
  return apiFetch<DuesTierWithRates[]>('/api/dues-tiers')
}

/** PATCH /api/dues-tiers/{id} - renames a tier. The name is a label; a
 * member references the tier by id. A duplicate name for the fund hits the
 * schema's UNIQUE (fund_id, name) and comes back 409. */
export function renameDuesTier(id: number, name: string): Promise<DuesTier> {
  return apiFetch<DuesTier>(`/api/dues-tiers/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

/** DELETE /api/dues-tiers/{id} - for a tier the fund never put anyone in (a
 * setup typo). Its rates go with it in the same transaction (they priced
 * nobody); a tier a member references is refused 409
 * referenced_by_other_records and nothing is deleted. */
export function deleteDuesTier(id: number): Promise<void> {
  return apiFetch<void>(`/api/dues-tiers/${id}`, { method: 'DELETE' })
}

/** PATCH /api/dues-rates/{id} - corrects a mistyped amount on a rate just
 * entered. Never a price change: a new price is a new row for a new month
 * (createDuesRate above), because the old one still explains old periods. */
export function updateDuesRate(id: number, amount: number): Promise<DuesRate> {
  return apiFetch<DuesRate>(`/api/dues-rates/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ amount }),
  })
}

/** DELETE /api/dues-rates/{id} - for a rate filed against the wrong month,
 * which is what makes that correctable at all: UNIQUE (tier_id,
 * effective_from) refuses the corrected row while the wrong one stands. */
export function deleteDuesRate(id: number): Promise<void> {
  return apiFetch<void>(`/api/dues-rates/${id}`, { method: 'DELETE' })
}
