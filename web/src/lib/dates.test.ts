import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  addMonthsToPeriod,
  dateBounds,
  formatIsoDate,
  formatPeriod,
  formatUnixSeconds,
  parseIsoDate,
  periodBounds,
  toIsoDate,
} from '@/lib/dates'

describe('formatIsoDate', () => {
  // The month is written out, never abbreviated: id-ID's `medium` style
  // renders "3 Sep 2026", which is what these screens used to show.
  it('writes the month out in full', () => {
    expect(formatIsoDate('2026-09-03')).toBe('3 September 2026')
  })

  // The whole reason these helpers parse the parts by hand: the Date
  // constructor reads a bare 'YYYY-MM-DD' as UTC midnight, which is the
  // previous day west of Greenwich. This test only fails somewhere with a
  // negative offset - so the assertion is the local calendar day, which is
  // what the treasurer typed.
  it('keeps the calendar day the string names, whatever the timezone', () => {
    expect(formatIsoDate('2026-01-01')).toBe('1 Januari 2026')
  })

  it('hands back anything it cannot parse rather than rendering a lie', () => {
    expect(formatIsoDate('kemarin')).toBe('kemarin')
  })
})

describe('formatUnixSeconds', () => {
  it('writes the month out in full', () => {
    // 2026-09-03 12:00 local - midday, so no timezone can move the date.
    const noon = new Date(2026, 8, 3, 12, 0, 0).getTime() / 1000
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
  // Only Date is faked; the bounds read the local clock.
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date(2026, 9, 1))
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
})
