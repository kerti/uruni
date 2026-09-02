import { describe, expect, it } from 'vitest'

import { formatIDR, formatRupiahDigits, parseRupiah } from '@/lib/money'

// Intl's id-ID currency output uses a non-breaking space after "Rp" - asserted
// against a normalized string so these tests describe the digits and grouping,
// which is what the rule is about, rather than one ICU version's spacing.
function normalize(value: string): string {
  return value.replace(/ /g, ' ')
}

describe('formatIDR', () => {
  it('renders integer rupiah with Indonesian grouping and no decimals', () => {
    expect(normalize(formatIDR(1_000_000))).toBe('Rp 1.000.000')
    expect(normalize(formatIDR(50_000))).toBe('Rp 50.000')
    expect(normalize(formatIDR(0))).toBe('Rp 0')
  })
})

describe('formatRupiahDigits', () => {
  it('renders the grouped digits an amount input echoes back, without the currency symbol', () => {
    expect(formatRupiahDigits(1_000_000)).toBe('1.000.000')
    expect(formatRupiahDigits(0)).toBe('0')
  })
})

describe('parseRupiah', () => {
  it('reads digits out of whatever the treasurer typed', () => {
    expect(parseRupiah('1000000')).toBe(1_000_000)
    expect(parseRupiah('1.000.000')).toBe(1_000_000)
    expect(parseRupiah('Rp 1.000.000')).toBe(1_000_000)
    expect(parseRupiah('1 000 000')).toBe(1_000_000)
  })

  it('reads an empty or digit-free field as zero, the "post nothing" path', () => {
    expect(parseRupiah('')).toBe(0)
    expect(parseRupiah('   ')).toBe(0)
    expect(parseRupiah('Rp')).toBe(0)
  })

  it('cannot express a negative amount - a minus sign is not a digit', () => {
    expect(parseRupiah('-50000')).toBe(50_000)
  })

  it('never goes through a float: a typed decimal keeps every digit', () => {
    // parseFloat('1.5') would be 1.5, and a naive strip-then-parse would be
    // 15 rupiah; both are wrong for a field that only ever means whole
    // rupiah. 15 is the honest reading of the digits typed.
    expect(parseRupiah('1.5')).toBe(15)
    expect(Number.isInteger(parseRupiah('1.5'))).toBe(true)
  })
})
