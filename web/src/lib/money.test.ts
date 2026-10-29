import { describe, expect, it } from 'vitest'

import { exceedsAmountCeiling, formatIDR, formatRupiahDigits, MAX_AMOUNT, MAX_AMOUNT_DIGITS, parseRupiah } from '@/lib/money'

// Exact strings, no normalizing: the format is the contract (#724). Intl's
// currency style printed "Rp\u00a01.000" in Chrome and "Rp1.000" in Safari, so
// these pin every character, including the absence of a space after "Rp".
describe('formatIDR', () => {
  it('renders integer rupiah in the EYD form: "Rp" against the digits, dot grouping, no decimals', () => {
    expect(formatIDR(1_000_000)).toBe('Rp1.000.000')
    expect(formatIDR(1_625_500)).toBe('Rp1.625.500')
    expect(formatIDR(50_000)).toBe('Rp50.000')
    expect(formatIDR(999)).toBe('Rp999')
    expect(formatIDR(0)).toBe('Rp0')
    expect(formatIDR(MAX_AMOUNT)).toBe('Rp999.999.999.999')
  })

  it('joins a negative figure\'s minus to "Rp" so a line can never break between them', () => {
    expect(formatIDR(-15_000)).toBe('-\u2060Rp15.000')
    expect(formatIDR(-MAX_AMOUNT)).toBe('-\u2060Rp999.999.999.999')
    expect(parseRupiah(formatIDR(-15_000))).toBe(15_000)
  })

  it('never writes a negative zero', () => {
    expect(formatIDR(-0)).toBe('Rp0')
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
    expect(exceedsAmountCeiling('Rp999.999.999.999')).toBe(false)
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
