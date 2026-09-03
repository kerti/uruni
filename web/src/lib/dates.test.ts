import { describe, expect, it } from 'vitest'

import { formatIsoDate, formatPeriod, formatUnixSeconds } from '@/lib/dates'

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
