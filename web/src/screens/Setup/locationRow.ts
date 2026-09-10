/** One location row as the wizard collects it, before the server has ever
 * seen it - so it carries a client-only id, never a server account id
 * (there is none yet, and #230 means there never will be one before the
 * single POST /api/setup that creates everything), and never its array
 * index (removing an earlier row must not shift which row a later amount
 * belongs to). The opening balance lives right on the row, as the display
 * string OpeningBalances.tsx already renders (see lib/money.ts's
 * formatRupiahDigits/parseRupiah pair). Shared by Setup.tsx (owns the
 * array), Locations.tsx (step 2: kind/name) and OpeningBalances.tsx (step 3:
 * openingBalance).
 */
export interface LocationRow {
  clientId: string
  kind: 'cash' | 'bank'
  name: string
  openingBalance: string
}

let nextClientId = 0

function newClientId(): string {
  nextClientId += 1
  return `row-${nextClientId}`
}

/** A fresh, empty row for the wizard's seed rows and its "add a location"
 * button alike. */
export function newLocationRow(kind: 'cash' | 'bank' = 'cash', name = ''): LocationRow {
  return { clientId: newClientId(), kind, name, openingBalance: '' }
}
