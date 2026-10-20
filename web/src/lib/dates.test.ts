import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  addMonthsToPeriod,
  currentPeriod,
  dateBounds,
  formatIsoDate,
  formatPeriod,
  formatUnixSeconds,
  formatUnixSecondsWithTime,
  getFundTimeZone,
  parseIsoDate,
  periodBounds,
  resetFundTimeZone,
  setFundTimeZone,
  suggestedTimeZone,
  todayISODate,
  toIsoDate,
  yearAhead,
} from '@/lib/dates'
import { zoneForDeviceOffset } from '@/lib/timeZones'

describe('formatIsoDate', () => {
  // The month is written out, never abbreviated (`medium` renders "3 Sep 2026").
  it('writes the month out in full', () => {
    expect(formatIsoDate('2026-09-03')).toBe('3 September 2026')
  })

  // The helpers parse the parts by hand because the Date constructor reads a
  // bare 'YYYY-MM-DD' as UTC midnight, the previous day west of Greenwich. This
  // only fails somewhere with a negative offset, so the assertion is the local
  // calendar day the treasurer typed.
  it('keeps the calendar day the string names, whatever the timezone', () => {
    expect(formatIsoDate('2026-01-01')).toBe('1 Januari 2026')
  })

  it('hands back anything it cannot parse rather than rendering a lie', () => {
    expect(formatIsoDate('kemarin')).toBe('kemarin')
  })
})

describe('formatUnixSeconds', () => {
  it('writes the month out in full', () => {
    // 2026-09-03 12:00 WIB - midday, so no zone near the fund's can move the date.
    const noon = new Date('2026-09-03T12:00:00+07:00').getTime() / 1000
    expect(formatUnixSeconds(noon)).toBe('3 September 2026')
  })
})

describe('formatPeriod', () => {
  it('renders a dues period as a month and year', () => {
    expect(formatPeriod('2026-09')).toBe('September 2026')
  })

  it('hands back a malformed period unchanged', () => {
    expect(formatPeriod('2026')).toBe('2026')
  })
})

describe('parseIsoDate / toIsoDate (#197)', () => {
  it('round-trips a real date at local midnight', () => {
    const date = parseIsoDate('2026-02-28')
    expect(date?.getHours()).toBe(0)
    expect(toIsoDate(date as Date)).toBe('2026-02-28')
  })

  it('refuses a date that does not exist rather than rolling it over', () => {
    expect(parseIsoDate('2026-02-30')).toBeNull()
    expect(parseIsoDate('2026-9-3')).toBeNull()
    expect(parseIsoDate('')).toBeNull()
  })
})

describe('addMonthsToPeriod', () => {
  it('crosses year ends both ways', () => {
    expect(addMonthsToPeriod('2026-11', 3)).toBe('2027-02')
    expect(addMonthsToPeriod('2026-01', -1)).toBe('2025-12')
  })
})

describe('date picker bounds (#197)', () => {
  // Only Date is faked; the bounds read the fund's clock (WIB until a zone is set).
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-01T09:00:00+07:00'))
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('caps money dates at today and two calendar years back', () => {
    expect(dateBounds.entry()).toEqual({ min: '2024-01-01', max: '2026-10-01' })
  })

  it('lets a join date reach back to 2000 and a year ahead', () => {
    expect(dateBounds.membership()).toEqual({ min: '2000-01-01', max: '2027-10-01' })
  })

  it('lets a rate start two years ahead, and the status period look a year ahead', () => {
    expect(periodBounds.rate()).toEqual({ min: '2000-01', max: '2028-10' })
    expect(periodBounds.status()).toEqual({ min: '2000-01', max: '2027-10' })
  })

  it('bounds a tier start from the join month to the current month', () => {
    expect(periodBounds.tierStart('2026-03-15')).toEqual({ min: '2026-03', max: '2026-10' })
    expect(periodBounds.tierStart('2026-10-01')).toEqual({ min: '2026-10', max: '2026-10' })
  })

  it('lets the join month lift the tier start ceiling for a member who joins later', () => {
    expect(periodBounds.tierStart('2026-12-05')).toEqual({ min: '2026-12', max: '2026-12' })
  })
})

// ADR-041: "today" and "this month" are the fund's zone's, never the device's.
// Every instant below is named with an explicit offset, so the result does not
// depend on the zone of the machine running the tests (CI is UTC).
describe("the fund's zone (ADR-041)", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
  })
  afterEach(() => {
    vi.useRealTimers()
    resetFundTimeZone()
  })

  // 22:30 WIB on 30 September is 23:30 WITA and 00:30 WIT on 1 October.
  const lateEvening = '2026-09-30T22:30:00+07:00'
  const lastHour = '2026-09-30T23:30:00+07:00'

  it.each([
    ['Asia/Jakarta', lateEvening, '2026-09-30', '2026-09'],
    ['Asia/Makassar', lateEvening, '2026-09-30', '2026-09'],
    ['Asia/Jayapura', lateEvening, '2026-10-01', '2026-10'],
    ['Asia/Jakarta', lastHour, '2026-09-30', '2026-09'],
    ['Asia/Makassar', lastHour, '2026-10-01', '2026-10'],
    ['Asia/Jayapura', lastHour, '2026-10-01', '2026-10'],
  ])('%s at %s: today %s, month %s', (zone, instant, today, month) => {
    vi.setSystemTime(new Date(instant))
    setFundTimeZone(zone)
    expect(todayISODate()).toBe(today)
    expect(currentPeriod()).toBe(month)
  })

  it("reads the default zone, WIB, until a zone is set - never the device's", () => {
    resetFundTimeZone()
    vi.setSystemTime(new Date('2026-09-30T23:30:00+07:00'))
    expect(getFundTimeZone()).toBe('Asia/Jakarta')
    expect(todayISODate()).toBe('2026-09-30')
    // The same instant on the UTC calendar is still 30 September at 16:30Z, and
    // on a phone in WIT already 1 October: only the fund's zone decides.
    vi.setSystemTime(new Date('2026-09-30T16:30:00Z'))
    expect(todayISODate()).toBe('2026-09-30')
  })

  it('ignores a zone it does not know, rather than falling back to the device', () => {
    setFundTimeZone('Asia/Makassar')
    setFundTimeZone('Asia/Tokyo')
    setFundTimeZone('')
    expect(getFundTimeZone()).toBe('Asia/Makassar')
  })

  it('flips the picker bounds with the zone', () => {
    vi.setSystemTime(new Date(lateEvening))
    setFundTimeZone('Asia/Jakarta')
    expect(dateBounds.entry().max).toBe('2026-09-30')
    expect(periodBounds.history().max).toBe('2026-09')
    expect(periodBounds.tierStart('2026-01-01').max).toBe('2026-09')
    setFundTimeZone('Asia/Jayapura')
    expect(dateBounds.entry().max).toBe('2026-10-01')
    expect(periodBounds.history().max).toBe('2026-10')
    expect(periodBounds.status().max).toBe('2027-10')
    expect(periodBounds.tierStart('2026-01-01').max).toBe('2026-10')
  })

  it("shows an instant as the day it falls on in the fund's zone", () => {
    const seconds = new Date('2026-09-30T23:30:00+07:00').getTime() / 1000 // 16:30Z
    setFundTimeZone('Asia/Jakarta')
    expect(formatUnixSeconds(seconds)).toBe('30 September 2026')
    setFundTimeZone('Asia/Jayapura')
    expect(formatUnixSeconds(seconds)).toBe('1 Oktober 2026')
  })

  it("writes a time of day with the zone's abbreviation from copy", () => {
    const seconds = new Date('2026-10-01T13:42:00+07:00').getTime() / 1000
    setFundTimeZone('Asia/Jakarta')
    expect(formatUnixSecondsWithTime(seconds)).toBe('1 Oktober 2026 pukul 13.42 WIB')
    setFundTimeZone('Asia/Makassar')
    expect(formatUnixSecondsWithTime(seconds)).toBe('1 Oktober 2026 pukul 14.42 WITA')
    setFundTimeZone('Asia/Jayapura')
    expect(formatUnixSecondsWithTime(seconds)).toBe('1 Oktober 2026 pukul 15.42 WIT')
  })

  it('leaves a business date and a period alone: they name a day, not an instant', () => {
    setFundTimeZone('Asia/Jayapura')
    expect(formatIsoDate('2026-09-30')).toBe('30 September 2026')
    expect(formatPeriod('2026-09')).toBe('September 2026')
  })
})

describe('the zone Setup pre-selects (ADR-041)', () => {
  it.each([
    [-420, 'Asia/Jakarta'],
    [-480, 'Asia/Makassar'],
    [-540, 'Asia/Jayapura'],
    // Any other offset falls back to WIB: UTC, New York, India.
    [0, 'Asia/Jakarta'],
    [300, 'Asia/Jakarta'],
    [-330, 'Asia/Jakarta'],
  ])('a device %d minutes from UTC suggests %s', (offset, zone) => {
    expect(zoneForDeviceOffset(offset)).toBe(zone)
  })

  it('reads the device clock for the offset, and nothing else', () => {
    vi.spyOn(Date.prototype, 'getTimezoneOffset').mockReturnValue(-480)
    expect(suggestedTimeZone()).toBe('Asia/Makassar')
    vi.restoreAllMocks()
  })
})

// #632: the server refuses a date outside its window (internal/ledger/date_window.go
// and the schema's CHECKs), and no date a picker offers may be refused. The
// server's limits are written out here by hand, per field, for the same fund
// clock, so the table below is the contract both sides are held to: the Go side
// pins the same numbers in date_window_test.go.
describe('the pickers sit inside the server window (#632)', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
  })
  afterEach(() => {
    vi.useRealTimers()
    resetFundTimeZone()
  })

  const FLOOR_DAY = '2000-01-01'
  const FLOOR_MONTH = '2000-01'

  // [fund zone, instant, today, join/leave ceiling, dues ceiling, rate ceiling]
  it.each([
    ['Asia/Jakarta', '2026-09-30T22:30:00+07:00', '2026-09-30', '2027-09-30', '2027-09', '2028-09'],
    ['Asia/Jayapura', '2026-09-30T22:30:00+07:00', '2026-10-01', '2027-10-01', '2027-10', '2028-10'],
    ['Asia/Makassar', '2026-12-31T23:30:00+07:00', '2027-01-01', '2028-01-01', '2028-01', '2029-01'],
    // The leap day: a year ahead of 29 February 2028 is 28 February 2029.
    ['Asia/Jakarta', '2028-02-29T12:00:00+07:00', '2028-02-29', '2029-02-28', '2029-02', '2030-02'],
    ['Asia/Jakarta', '2028-02-28T12:00:00+07:00', '2028-02-28', '2029-02-28', '2029-02', '2030-02'],
  ])('%s at %s (today %s): every picker ends where the server does', (zone, instant, today, person, dues, rate) => {
    vi.setSystemTime(new Date(instant))
    setFundTimeZone(zone)

    // Money that has moved: at most today, never below the floor.
    expect(dateBounds.entry().max).toBe(today)
    expect(dateBounds.entry().min >= FLOOR_DAY).toBe(true)
    // A join or leaving date: a year ahead, and the last day is a real date.
    expect(dateBounds.membership().max).toBe(person)
    expect(parseIsoDate(dateBounds.membership().max)).not.toBeNull()
    expect(dateBounds.membership().min).toBe(FLOOR_DAY)
    // A rate's start month: 24 months ahead.
    expect(periodBounds.rate().max).toBe(rate)
    expect(periodBounds.rate().min).toBe(FLOOR_MONTH)
    // The dues form walks at most 12 months past the current month, and the
    // status screen's month picker (a read) matches it.
    expect(periodBounds.status().max).toBe(dues)
    // A tier start: the current month, or the join month if later - the server's
    // ceiling exactly - and never before the join month.
    expect(periodBounds.tierStart('2026-01-05').max).toBe(today.slice(0, 7))
  })

  it('keeps every picker end a real date on every day of a leap year and the years around it', () => {
    setFundTimeZone('Asia/Jakarta')
    const first = Date.UTC(2027, 0, 1)
    const last = Date.UTC(2029, 11, 31)
    for (let t = first; t <= last; t += 24 * 60 * 60 * 1000) {
      // Noon in WIB, so the fund's day is the UTC date of t.
      vi.setSystemTime(new Date(t + 5 * 60 * 60 * 1000))
      const today = new Date(t).toISOString().slice(0, 10)
      expect(todayISODate()).toBe(today)
      const max = dateBounds.membership().max
      expect(parseIsoDate(max), `${today} -> ${max}`).not.toBeNull()
      expect(max > today).toBe(true)
    }
  })

  it('writes a year ahead as the same day, clamped to the month end', () => {
    expect(yearAhead('2026-09-30')).toBe('2027-09-30')
    expect(yearAhead('2026-12-31')).toBe('2027-12-31')
    expect(yearAhead('2028-02-28')).toBe('2029-02-28')
    expect(yearAhead('2028-02-29')).toBe('2029-02-28')
    expect(yearAhead('2024-02-29')).toBe('2025-02-28')
    expect(yearAhead('2027-02-28')).toBe('2028-02-28')
  })
})
