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
const dateTimeFormatter = new Intl.DateTimeFormat('id-ID', { dateStyle: 'long', timeStyle: 'short' })
const monthFormatter = new Intl.DateTimeFormat('id-ID', { month: 'long', year: 'numeric' })

/** Today, as a local YYYY-MM-DD - never `toISOString()`, which is UTC and
 * can read as yesterday's date in WIB. The default `occurred_on`/`joined_on`
 * a form seeds itself with. */
export function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

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

/** A unix-seconds timestamp with its time of day -> "1 Oktober 2026 pukul
 * 13.42". A row's `created_at` in the entry detail (#359), where when it was
 * actually recorded is the point. */
export function formatUnixSecondsWithTime(unixSeconds: number): string {
  return dateTimeFormatter.format(new Date(unixSeconds * 1000))
}

/** "2026-09" -> "September 2026". A dues period, a rate's `effective_from`. */
export function formatPeriod(period: string): string {
  const [year, month] = period.split('-').map(Number)
  if (!Number.isFinite(year) || !Number.isFinite(month)) return period
  return monthFormatter.format(new Date(year, month - 1, 1))
}

/** "YYYY-MM-DD" -> a local-midnight Date, or null for anything else. The
 * date pickers' own parse (#197) - never `new Date(iso)`, see above. */
export function parseIsoDate(iso: string): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  if (!match) return null
  const [, y, m, d] = match.map(Number)
  const date = new Date(y, m - 1, d)
  return date.getFullYear() === y && date.getMonth() === m - 1 && date.getDate() === d ? date : null
}

/** A local Date -> "YYYY-MM-DD". */
export function toIsoDate(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

/** "YYYY-MM" `months` later (negative for earlier) - integer arithmetic,
 * no Date, so no timezone can shift it. */
export function addMonthsToPeriod(period: string, months: number): string {
  const [year, month] = period.split('-').map(Number)
  const index = year * 12 + (month - 1) + months
  return `${Math.floor(index / 12)}-${String((index % 12) + 1).padStart(2, '0')}`
}

/** The current local month, "YYYY-MM". */
export function currentPeriod(): string {
  return todayISODate().slice(0, 7)
}

/** The inclusive range a date picker offers (#197). The server accepts any
 * real calendar date; these exist so the treasurer cannot land on a date
 * that makes no sense for the field by a slip of the thumb - a payment
 * dated next year, or a year of 20026 - and so the calendar's year list
 * stays short. */
export interface DateBounds {
  min: string
  max: string
}

/** The earliest year any picker offers. A fund adopting Uruni carries its
 * history in the opening balance (PRD: history starts at adoption); only a
 * member's join date or a rate's start month ever reaches back further, for
 * live arrears, and not past this. */
const EARLIEST_YEAR = 2000

export const dateBounds = {
  /** Money that has moved - a transaction, a dues payment, a claim, a
   * settlement, a reversal, a reconciliation fix, an envelope opening or
   * closing. Never in the future (it already happened), and at most two
   * calendar years back: older than that belongs in the opening balance. */
  entry(): DateBounds {
    const today = todayISODate()
    return { min: `${Number(today.slice(0, 4)) - 2}-01-01`, max: today }
  },
  /** A member's join date: back to EARLIEST_YEAR for live arrears, and up to
   * a year ahead for someone who has said they are joining. */
  membership(): DateBounds {
    const today = todayISODate()
    return { min: `${EARLIEST_YEAR}-01-01`, max: `${Number(today.slice(0, 4)) + 1}${today.slice(4)}` }
  },
}

export const periodBounds = {
  /** A rate's start month: back to EARLIEST_YEAR (a rate that already
   * applied), up to two years ahead (a rise agreed in advance). */
  rate(): DateBounds {
    return { min: `${EARLIEST_YEAR}-01`, max: addMonthsToPeriod(currentPeriod(), 24) }
  },
  /** The dues status period: back to EARLIEST_YEAR, and a year ahead, far
   * enough to read a member paid in advance (#357). */
  status(): DateBounds {
    return { min: `${EARLIEST_YEAR}-01`, max: addMonthsToPeriod(currentPeriod(), 12) }
  },
}
