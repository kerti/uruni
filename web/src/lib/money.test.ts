import { describe, expect, it } from 'vitest'

import { exceedsAmountCeiling, formatIDR, formatRupiahDigits, MAX_AMOUNT, MAX_AMOUNT_DIGITS, parseRupiah } from '@/lib/money'

// Intl's id-ID currency output uses a non-breaking space after "Rp" - asserted
// against a normalized string so these tests describe the digits and grouping,
// which is what the rule is about, rather than one ICU version's spacing.
function normalize(value: string): string {
  return value.replace(/\u00a0/g, ' ')
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
    // 15 rupiah; both are wrong for whole-rupiah fields. 15 is the honest
    // reading of the digits typed.
    expect(parseRupiah('1.5')).toBe(15)
    expect(Number.isInteger(parseRupiah('1.5'))).toBe(true)
  })
})

describe('the amount ceiling', () => {
  it('is twelve nines, and exact in a JS number', () => {
    expect(MAX_AMOUNT).toBe(999_999_999_999)
    expect(MAX_AMOUNT_DIGITS).toBe(12)
    expect(Number.isSafeInteger(MAX_AMOUNT)).toBe(true)
  })

  it('accepts twelve digits and refuses thirteen', () => {
    expect(exceedsAmountCeiling('999999999999')).toBe(false)
    expect(exceedsAmountCeiling('1000000000000')).toBe(true)
    expect(exceedsAmountCeiling('9999999999999')).toBe(true)
  })

  it('counts digits however they were written, and ignores leading zeros', () => {
    expect(exceedsAmountCeiling('Rp 999.999.999.999')).toBe(false)
    expect(exceedsAmountCeiling('1.000.000.000.000')).toBe(true)
    expect(exceedsAmountCeiling('0000000000001')).toBe(false)
    expect(exceedsAmountCeiling('')).toBe(false)
  })

  it('parseRupiah reads the ceiling exactly and never pulls an oversized figure down under it', () => {
    expect(parseRupiah('999999999999')).toBe(999_999_999_999)
    // Above the ceiling the figure stays above it, so the server refuses it
    // (amount_too_large) instead of a clamp quietly saving a different amount.
    expect(parseRupiah('1000000000000')).toBe(1_000_000_000_000)
    expect(parseRupiah('99999999999999999999')).toBeGreaterThan(MAX_AMOUNT)
  })
})
