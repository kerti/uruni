import { describe, expect, it } from 'vitest'

/**
 * ADR-041: the fund's day and month are the fund's zone's, never the device's.
 * The twelve private copies of `todayISODate` and `currentISOMonth` were born
 * because the shared helper was not at hand; this is what keeps a thirteenth
 * from growing back. Outside `lib/dates.ts` (which reads the clock once, in the
 * fund's zone) and tests, nothing may call `new Date()` with no argument or
 * `Date.now()`.
 *
 * A date built from arguments - `new Date(year, month, day)`, `new Date(seconds
 * * 1000)` - names a day or an instant it was handed and reads no clock, so it
 * is not matched.
 */

// Every source file, as text. Vite resolves the glob at build time, so this
// reaches no further than web/src and needs no node types.
const sources = import.meta.glob<string>('/src/**/*.{ts,tsx}', { query: '?raw', import: 'default', eager: true })

/** What may read the device clock: the one module that owns "today", and test
 * files and helpers, which fake or freeze it. */
function mayReadTheClock(path: string): boolean {
  return path === '/src/lib/dates.ts' || /\.test\.tsx?$/.test(path) || path.startsWith('/src/test/')
}

/** Source with comments removed, so a comment that mentions `new Date()` (this
 * file's, dates.ts's) is not a call. Line comments are recognised only at the
 * start of a line or after whitespace, so a `//` inside a URL string survives. */
function withoutComments(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|\s)\/\/.*$/gm, '$1')
}

const DEVICE_CLOCK = /\bnew\s+Date\s*\(\s*\)|\bDate\s*\.\s*now\s*\(/

describe('the device clock (ADR-041)', () => {
  it('finds the source files, so the check below is not vacuously green', () => {
    const paths = Object.keys(sources)
    expect(paths).toContain('/src/lib/dates.ts')
    expect(paths).toContain('/src/screens/RecordTransaction.tsx')
    expect(paths.length).toBeGreaterThan(100)
  })

  it('is read by lib/dates.ts and nothing else', () => {
    const offenders = Object.entries(sources)
      .filter(([path]) => !mayReadTheClock(path))
      .filter(([, source]) => DEVICE_CLOCK.test(withoutComments(source)))
      .map(([path]) => path)
    expect(offenders, "use todayISODate / currentPeriod from lib/dates (the fund's zone), not the device clock").toEqual([])
  })

  it('does match what it bans', () => {
    for (const banned of ['const now = new Date()', 'new  Date( )', 'const t = Date.now()', 'Date . now ()']) {
      expect(DEVICE_CLOCK.test(banned), banned).toBe(true)
    }
    for (const fine of ['new Date(2026, 8, 3)', 'new Date(seconds * 1000)', 'new Date(iso)', 'x.getDate()', 'Date.parse(s)']) {
      expect(DEVICE_CLOCK.test(fine), fine).toBe(false)
    }
    expect(DEVICE_CLOCK.test(withoutComments('// new Date()\n/* Date.now() */'))).toBe(false)
  })
})
