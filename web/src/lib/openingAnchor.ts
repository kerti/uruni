// Where "history starts" for a dated entry (PRD section 7.1): the opening
// entry's date. An entry dated before it is probably a slip, or money already
// inside the opening balance, so the forms warn (never block). #637.

import type { Account } from '@/lib/accounts'

/** The date an entry is measured against, and the location it belongs to -
 * null when it is the fund's earliest opening rather than one location's. */
export interface OpeningAnchor {
  date: string
  locationName: string | null
}

/**
 * The anchor for a form posting against `accountIds`: each location's own
 * opening date, falling back to the fund's earliest opening entry when it has
 * none; with several locations (Pindah lokasi) the later one, since the entry
 * is before the opening of whichever started last. A form with no location
 * picker passes none and gets the fund's earliest. A fund with no opening
 * entry at all has no anchor, so no warning.
 *
 * Retired locations count towards the fund's earliest: their opening entry is
 * still part of the ledger.
 */
export function openingAnchor(accounts: Account[], accountIds: (number | null)[] = []): OpeningAnchor | null {
  let earliest: string | null = null
  for (const account of accounts) {
    const on = account.opening_on
    if (on && (earliest === null || on < earliest)) earliest = on
  }
  if (earliest === null) return null
  const fund: OpeningAnchor = { date: earliest, locationName: null }

  let anchor: OpeningAnchor | null = null
  for (const id of accountIds) {
    if (id === null) continue
    const account = accounts.find((a) => a.id === id)
    const own: OpeningAnchor = account?.opening_on ? { date: account.opening_on, locationName: account.name } : fund
    if (anchor === null || own.date > anchor.date) anchor = own
  }
  return anchor ?? fund
}

/** ISO dates compare as strings; the anchor day itself is not "before". */
export function isBeforeAnchor(date: string, anchor: OpeningAnchor | null): boolean {
  return anchor !== null && date !== '' && date < anchor.date
}
