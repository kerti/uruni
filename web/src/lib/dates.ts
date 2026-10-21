/**
 * The app's date formatting, in one place, so a change of mind about how a
 * date reads is one edit. It is also the only module that reads the clock
 * (`new Date()`): the fund's day and month are the fund's zone's, never the
 * device's (ADR-041), and `noDeviceClock.test.ts` fails on a second reader.
 *
 * **`dateStyle: 'long'`, not `'medium'`.** In id-ID, medium abbreviates the
 * month ("3 Sep 2026") and long writes it out ("3 September 2026"), which is
 * what the design system's warm, unhurried voice asks for.
 *
 * Every function parses the date's parts by hand rather than handing a bare
 * string to the Date constructor: `new Date('2026-09-03')` is parsed as UTC
 * midnight, which renders as the previous day west of Greenwich. Same for a
 * bare 'YYYY-MM'.
 *
 * **The fund's zone.** "Today" and "this month" are read in the fund's time
 * zone (WIB, WITA or WIT), which `lib/setup.ts` hands to `setFundTimeZone`
 * inside the functions that return a fund, before their promise resolves and
 * never from an effect or a render: the first screen's `useState(todayISODate)`
 * initialisers run on the render right after the fetch, so a zone set from an
 * effect would let that screen read the default. Until a zone is set the
 * helpers read the default zone (WIB), never the device's, so a test or a stray
 * early call is deterministic.
 */

import { copy } from '@/copy/id'
import { DEFAULT_TIME_ZONE, isTimeZone, zoneForDeviceOffset, type TimeZone } from '@/lib/timeZones'

let fundZone: TimeZone = DEFAULT_TIME_ZONE

/** Holds the fund's zone for the module. Called by `lib/setup.ts` with the
 * `time_zone` of every fund payload it returns. A name outside the three is
 * ignored: the server's CHECK makes one unrepresentable, and the zone stays
 * what it was rather than becoming the device's. */
export function setFundTimeZone(zone: string): void {
  if (isTimeZone(zone)) fundZone = zone
}

/** The zone "today" is read in. */
export function getFundTimeZone(): TimeZone {
  return fundZone
}

/** Back to the default zone. For tests. */
export function resetFundTimeZone(): void {
  fundZone = DEFAULT_TIME_ZONE
}

/** The zone this device's clock suggests, by UTC offset: Setup's starting
 * value for its zone field (ADR-041), never the fund's zone. */
export function suggestedTimeZone(): TimeZone {
  return zoneForDeviceOffset(new Date().getTimezoneOffset())
}

// Formatters are built per zone and kept: an Intl.DateTimeFormat is costly to
// construct, and a screen asks for "today" on every render of a form.
const partsFormatters = new Map<TimeZone, Intl.DateTimeFormat>()
const dateFormatters = new Map<TimeZone, Intl.DateTimeFormat>()
const dateTimeFormatters = new Map<TimeZone, Intl.DateTimeFormat>()
// A business date and a period are zone-independent: they are read from a
// local-midnight Date built from their own parts, so the day cannot move. Only
// an instant (the unix-seconds formatters below) needs the fund's zone.
const dateFormatter = new Intl.DateTimeFormat('id-ID', { dateStyle: 'long' })
const monthFormatter = new Intl.DateTimeFormat('id-ID', { month: 'long', year: 'numeric' })

function cached(cache: Map<TimeZone, Intl.DateTimeFormat>, zone: TimeZone, make: () => Intl.DateTimeFormat): Intl.DateTimeFormat {
  let formatter = cache.get(zone)
  if (!formatter) {
    formatter = make()
    cache.set(zone, formatter)
  }
  return formatter
}

/** The calendar day an instant falls on in `zone`, as "YYYY-MM-DD". Built from
 * `formatToParts`, so no locale decides the shape. */
function isoDateIn(zone: TimeZone, instant: Date): string {
  const formatter = cached(
    partsFormatters,
    zone,
    () => new Intl.DateTimeFormat('en-US', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit' }),
  )
  const parts: Record<string, string> = {}
  for (const part of formatter.formatToParts(instant)) parts[part.type] = part.value
  return `${parts.year}-${parts.month}-${parts.day}`
}

/** The device clock as an instant, for measuring elapsed wall time only (the
 * fetch wrapper's 15-minute key window, ADR-043) - never a day, which is
 * todayISODate's, in the fund's zone. Wall time, not performance.now(), which
 * can stand still while a locked phone sleeps. */
export function wallClockMs(): number {
  return Date.now()
}

/** Today in the fund's zone, as YYYY-MM-DD - never `toISOString()` (UTC, which
 * reads as yesterday's date in WIB before 07:00) and never the device's own
 * calendar (a phone in another zone). The default `occurred_on`/`joined_on` a
 * form seeds itself with. */
export function todayISODate(): string {
  return isoDateIn(fundZone, new Date())
}

/** "2026-09-03" -> "3 September 2026". An `occurred_on`, `joined_on`,
 * `inactive_on`. Zone-independent: a business date names a calendar day. */
export function formatIsoDate(isoDate: string): string {
  const [year, month, day] = isoDate.split('-').map(Number)
  if (!Number.isFinite(year) || !Number.isFinite(month) || !Number.isFinite(day)) return isoDate
  return dateFormatter.format(new Date(year, month - 1, day))
}

/** A unix-seconds timestamp -> "3 September 2026", the day it falls on in the
 * fund's zone. A reconciliation's `performed_at`, any row's `created_at`. */
export function formatUnixSeconds(unixSeconds: number): string {
  const zone = fundZone
  return cached(dateFormatters, zone, () => new Intl.DateTimeFormat('id-ID', { dateStyle: 'long', timeZone: zone })).format(
    new Date(unixSeconds * 1000),
  )
}

/** A unix-seconds timestamp with its time of day and the zone's abbreviation,
 * from copy and not from `Intl`'s engine-dependent `timeZoneName` -> "1 Oktober
 * 2026 pukul 13.42 WIB". A row's `created_at` in the entry detail, where when it
 * was actually recorded is the point. */
export function formatUnixSecondsWithTime(unixSeconds: number): string {
  const zone = fundZone
  const formatted = cached(
    dateTimeFormatters,
    zone,
    () => new Intl.DateTimeFormat('id-ID', { dateStyle: 'long', timeStyle: 'short', timeZone: zone }),
  ).format(new Date(unixSeconds * 1000))
  return `${formatted} ${copy.timeZone.options[zone].abbreviation}`
}

/** "2026-09" -> "September 2026". A dues period, a rate's `effective_from`. */
export function formatPeriod(period: string): string {
  const [year, month] = period.split('-').map(Number)
  if (!Number.isFinite(year) || !Number.isFinite(month)) return period
  return monthFormatter.format(new Date(year, month - 1, 1))
}

/** "YYYY-MM-DD" -> a local-midnight Date, or null for anything else. Never
 * `new Date(iso)`, see above. */
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

/** The current month in the fund's zone, "YYYY-MM". */
export function currentPeriod(): string {
  return todayISODate().slice(0, 7)
}

/** The inclusive range a date picker offers. The server refuses a date outside
 * its plausible window (#632: a floor of 2000 in the schema, a ceiling per field
 * read in the fund's zone, internal/ledger/date_window.go) with `date_out_of_range`.
 * These bounds sit inside that window, so no date a picker offers is ever
 * refused; they also keep the treasurer from landing on a nonsensical one by a
 * slip of the thumb and keep the calendar's year list short. The window's table
 * is pinned in dates.test.ts. */
export interface DateBounds {
  min: string
  max: string
}

/** The earliest year any picker offers, and the server's floor (the schema's
 * CHECKs, #632). A fund adopting Uruni carries its history in the opening
 * balance (PRD: history starts at adoption); only a member's join date or a
 * rate's start month reaches back further, for live arrears, and not past this. */
const EARLIEST_YEAR = 2000

/** The same day next year, or the month's last day when that day does not exist:
 * 29 February reads as 28 February. The server's ceiling for a join or leaving
 * date is exactly this (`dateWindow.person` in internal/ledger/date_window.go),
 * so the picker's last day is a real date the server accepts. Plain integer
 * arithmetic on the date's parts, never a Date round trip. */
export function yearAhead(isoDate: string): string {
  const [year, month, day] = isoDate.split('-').map(Number)
  const lastDay = new Date(year + 1, month, 0).getDate()
  return `${year + 1}-${String(month).padStart(2, '0')}-${String(Math.min(day, lastDay)).padStart(2, '0')}`
}

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
   * a year ahead for someone who has said they are joining. On 29 February the
   * year ahead is 28 February (`yearAhead`), a day that exists. */
  membership(): DateBounds {
    return { min: `${EARLIEST_YEAR}-01-01`, max: yearAhead(todayISODate()) }
  },
}

export const periodBounds = {
  /** A rate's start month: back to EARLIEST_YEAR (a rate that already
   * applied), up to two years ahead (a rise agreed in advance). */
  rate(): DateBounds {
    return { min: `${EARLIEST_YEAR}-01`, max: addMonthsToPeriod(currentPeriod(), 24) }
  },
  /** The dues status period: back to EARLIEST_YEAR, and a year ahead, far
   * enough to read a member paid in advance. */
  status(): DateBounds {
    return { min: `${EARLIEST_YEAR}-01`, max: addMonthsToPeriod(currentPeriod(), 12) }
  },
  /** A member's tier start month ("Mulai bulan", ADR-040): from the join
   * month to the later of the current month and the join month. No future
   * start - a pending tier would be shown nowhere. */
  tierStart(joinedOn: string): DateBounds {
    const current = currentPeriod()
    const join = /^\d{4}-\d{2}/.test(joinedOn) ? joinedOn.slice(0, 7) : current
    return { min: join, max: join > current ? join : current }
  },
  /** Riwayat's month filter: any month money could have moved in - back to
   * EARLIEST_YEAR, never past this one. */
  history(): DateBounds {
    return { min: `${EARLIEST_YEAR}-01`, max: currentPeriod() }
  },
}
