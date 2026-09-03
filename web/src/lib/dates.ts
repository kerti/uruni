/**
 * The app's date formatting, in one place.
 *
 * It lived in four screens as four copies of the same two helpers, which is
 * how one of them came to read "3 Sep 2026" while the rate list beside it
 * spelled September out - a difference nobody chose. One module, so a change
 * of mind about how a date reads is one edit.
 *
 * **`dateStyle: 'long'`, not `'medium'`.** In id-ID, medium abbreviates the
 * month ("3 Sep 2026") and long writes it out ("3 September 2026"). Written
 * out is what the design system's warm, unhurried voice asks for, and the
 * rows here have the width for it.
 *
 * Every function parses the date's parts by hand rather than handing a bare
 * string to the Date constructor: `new Date('2026-09-03')` is parsed as UTC
 * midnight, which renders as the previous day west of Greenwich - and WIB is
 * east of it, so the bug shows up as a date a day early for anyone testing
 * from the Americas. Same reasoning for a bare 'YYYY-MM'.
 */

const dateFormatter = new Intl.DateTimeFormat('id-ID', { dateStyle: 'long' })
const monthFormatter = new Intl.DateTimeFormat('id-ID', { month: 'long', year: 'numeric' })

/** "2026-09-03" -> "3 September 2026". A transaction's `occurred_on`, a
 * member's `joined_on`, an account's `inactive_on`. */
export function formatIsoDate(isoDate: string): string {
  const [year, month, day] = isoDate.split('-').map(Number)
  if (!Number.isFinite(year) || !Number.isFinite(month) || !Number.isFinite(day)) return isoDate
  return dateFormatter.format(new Date(year, month - 1, day))
}

/** A unix-seconds timestamp -> "3 September 2026". A reconciliation's
 * `performed_at`, any row's `created_at`. */
export function formatUnixSeconds(unixSeconds: number): string {
  return dateFormatter.format(new Date(unixSeconds * 1000))
}

/** "2026-09" -> "September 2026". A dues period, a rate's `effective_from`. */
export function formatPeriod(period: string): string {
  const [year, month] = period.split('-').map(Number)
  if (!Number.isFinite(year) || !Number.isFinite(month)) return period
  return monthFormatter.format(new Date(year, month - 1, 1))
}
