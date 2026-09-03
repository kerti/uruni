// Typed call over apiFetch for GET /api/accounts (M6.8), same idiom as
// lib/auth.ts and lib/setup.ts. The Account shape is already defined in
// lib/setup.ts (the setup wizard reads the same rows POST /api/setup just
// created) - re-exported here rather than redefined, so the two modules
// can't drift apart on the same wire shape.

import { apiFetch } from '@/lib/api'
import type { Account } from '@/lib/setup'

export type { Account }

/**
 * GET /api/accounts - every location the fund has, active and retired alike
 * (internal/http/accounts.go's listAccounts: history still needs the
 * retired ones). Callers that must offer only a place to *record against*
 * - this screen's AccountPicker - filter out `inactive_on` rows themselves;
 * this module stays a plain wire call with no policy of its own.
 */
export function listAccounts(): Promise<Account[]> {
  return apiFetch<Account[]>('/api/accounts')
}

/**
 * POST /api/accounts - one more location for a fund that already exists
 * (#78: setup asks for the first batch, this adds to them afterward). Kind
 * is fixed at creation; there is no route that changes it, deliberately -
 * cash and bank reconcile differently, so a location that changed kind is a
 * different location.
 */
export function createAccount(kind: string, name: string): Promise<Account> {
  return apiFetch<Account>('/api/accounts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ kind, name }),
  })
}

/**
 * PATCH /api/accounts/{id} for the two labels a location carries. An absent
 * key means "leave alone" server-side, so an undefined field here is simply
 * left out of the body rather than sent as null - null means something else
 * on this route (see setAccountInactiveOn), which is why retiring has its
 * own function instead of a third optional field here.
 *
 * Kind is editable because nothing in the ledger branches on it: 'cash' vs
 * 'bank' is a label on the location, not a rule about the money in it.
 */
export function updateAccount(id: number, patch: { name?: string; kind?: string }): Promise<Account> {
  const body: Record<string, string> = {}
  if (patch.name !== undefined) body.name = patch.name
  if (patch.kind !== undefined) body.kind = patch.kind
  return apiFetch<Account>(`/api/accounts/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

/** PATCH /api/accounts/{id} with `inactive_on` - a `YYYY-MM-DD` date retires
 * the location, null reinstates one retired by mistake. */
export function setAccountInactiveOn(id: number, inactiveOn: string | null): Promise<Account> {
  return apiFetch<Account>(`/api/accounts/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ inactive_on: inactiveOn }),
  })
}

/**
 * DELETE /api/accounts/{id} - for a duplicate that was never used. The
 * moment anything references the location the server answers 409
 * `referenced_by_other_records` (mapSQLiteDeleteError), which the settings
 * screen renders as "nonaktifkan, bukan hapus" rather than a generic
 * failure: deactivating is what the treasurer actually wants there.
 */
export function deleteAccount(id: number): Promise<void> {
  return apiFetch<void>(`/api/accounts/${id}`, { method: 'DELETE' })
}
