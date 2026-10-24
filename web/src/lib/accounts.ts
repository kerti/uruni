// Typed call over apiFetch for GET /api/accounts. The Account shape is
// lib/setup.ts's (the setup wizard reads the same rows), re-exported here so
// the two modules can't drift apart on one wire shape.

import { apiFetch } from '@/lib/api'
import type { Account } from '@/lib/setup'
import type { AccountKind } from '@/lib/enums'

export type { Account }

/**
 * GET /api/accounts - every location the fund has, active and retired alike
 * (history still needs the retired ones). Callers that must offer only a place
 * to *record against* - AccountPicker - filter out `inactive_on` rows
 * themselves; this stays a plain wire call with no policy.
 */
export function listAccounts(): Promise<Account[]> {
  return apiFetch<Account[]>('/api/accounts')
}

/**
 * POST /api/accounts - one more location for a fund that already exists
 * (setup asks for the first batch). Kind is fixed at creation, deliberately -
 * cash and bank reconcile differently, so a location that changed kind is a
 * different location.
 *
 * `openingBalance`, when given, is posted in the same database transaction
 * that creates the account: a location and its opening balance are born
 * together, or not at all. Omit it, or leave the amount at 0, for a location
 * that starts empty. Its `note` holds only what the treasurer typed - the
 * add-location dialog has no field for one, so it is omitted in practice; the
 * row explains itself through a display label.
 */
export function createAccount(
  kind: AccountKind,
  name: string,
  openingBalance?: { amount: number; occurredOn: string; note?: string | null },
): Promise<Account> {
  return apiFetch<Account>('/api/accounts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      kind,
      name,
      ...(openingBalance && openingBalance.amount > 0
        ? { opening_balance: { amount: openingBalance.amount, occurred_on: openingBalance.occurredOn, note: openingBalance.note ?? null } }
        : {}),
    }),
  })
}

/**
 * PATCH /api/accounts/{id} for the two labels a location carries. An absent
 * key means "leave alone" server-side, so an undefined field is left out of
 * the body rather than sent as null - null means something else on this
 * route (see setAccountInactiveOn), which is why retiring has its own
 * function.
 *
 * Kind is editable because nothing in the ledger branches on it: 'cash' vs
 * 'bank' is a label on the location, not a rule about the money in it.
 */
export function updateAccount(id: number, patch: { name?: string; kind?: AccountKind }): Promise<Account> {
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
 * DELETE /api/accounts/{id} - for a duplicate that was never used. Once
 * anything references the location the server answers 409
 * `referenced_by_other_records`, which the settings screen renders as
 * "nonaktifkan, bukan hapus" rather than a generic failure.
 */
export function deleteAccount(id: number): Promise<void> {
  return apiFetch<void>(`/api/accounts/${id}`, { method: 'DELETE' })
}
