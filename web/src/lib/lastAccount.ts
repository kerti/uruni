// The one rule for "where does the money leave from" pickers, shared by every
// form that has one (#737): Catat, Iuran, Bayar talangan, Tutup amplop.

import type { Account } from '@/lib/accounts'

/** localStorage key for the last location chosen - a per-viewer convenience
 * (PRD section 7.2's "location remembers last used"), never server state. */
const LAST_ACCOUNT_KEY = 'uruni:record:last-account-id'

/** Reads the remembered account id, or null if there is none, it doesn't
 * parse, or storage isn't reachable (a private window can throw on the
 * property access itself, not just the call). */
export function readLastAccountId(): number | null {
  try {
    const raw = window.localStorage.getItem(LAST_ACCOUNT_KEY)
    if (raw === null) return null
    const parsed = Number(raw)
    return Number.isFinite(parsed) ? parsed : null
  } catch {
    return null
  }
}

/** Called after any successful posting that chose a source location, so the
 * next form opens where she last paid from. */
export function rememberAccountId(accountId: number) {
  try {
    window.localStorage.setItem(LAST_ACCOUNT_KEY, String(accountId))
  } catch {
    // Private window, storage disabled, quota - the remembered default is a
    // convenience; losing it costs one extra tap.
  }
}

/** The default for a source-location picker: the last-used active location,
 * falling back to the first active one (none when there is no active
 * location). A retired location is never a candidate. */
export function defaultSourceAccountId(accounts: Account[]): number | null {
  const active = accounts.filter((a) => a.inactive_on === null)
  if (active.length === 0) return null
  const lastId = readLastAccountId()
  return (active.find((a) => a.id === lastId) ?? active[0]).id
}

/** The default for a destination picker: blank, unless exactly one candidate
 * exists, in which case that one is the only possible answer. */
export function soleCandidateId(candidates: { id: number }[]): number | null {
  return candidates.length === 1 ? candidates[0].id : null
}
