import { describe, expect, it } from 'vitest'

import { isBeforeAnchor, openingAnchor } from '@/lib/openingAnchor'
import type { Account } from '@/lib/accounts'

function account(id: number, name: string, opening_on: string | null): Account {
  return { id, kind: 'cash', name, inactive_on: null, opening_on, created_at: 1 }
}

const tunai = account(1, 'Tunai', '2026-03-01')
const bank = account(2, 'Bank', '2026-05-10')
const empty = account(3, 'Dompet', null)

describe('openingAnchor', () => {
  it('is the selected location own opening, named', () => {
    expect(openingAnchor([tunai, bank], [2])).toEqual({ date: '2026-05-10', locationName: 'Bank' })
  })

  it('falls back to the fund earliest opening when the location has none', () => {
    expect(openingAnchor([bank, tunai, empty], [3])).toEqual({ date: '2026-03-01', locationName: null })
  })

  it('is the fund earliest for a form with no location', () => {
    expect(openingAnchor([bank, tunai])).toEqual({ date: '2026-03-01', locationName: null })
  })

  it('is null when the fund has no opening entry at all', () => {
    expect(openingAnchor([empty], [3])).toBeNull()
    expect(openingAnchor([empty])).toBeNull()
  })

  it('takes the later of two locations, and names that one', () => {
    expect(openingAnchor([tunai, bank], [1, 2])).toEqual({ date: '2026-05-10', locationName: 'Bank' })
    expect(openingAnchor([tunai, bank], [2, 1])).toEqual({ date: '2026-05-10', locationName: 'Bank' })
  })

  it('counts a location with no opening as the fund earliest when pairing', () => {
    expect(openingAnchor([tunai, bank, empty], [3, 1])).toEqual({ date: '2026-03-01', locationName: null })
    expect(openingAnchor([tunai, bank, empty], [3, 2])).toEqual({ date: '2026-05-10', locationName: 'Bank' })
  })

  it('ignores a location not yet chosen', () => {
    expect(openingAnchor([tunai, bank], [null, 2])).toEqual({ date: '2026-05-10', locationName: 'Bank' })
  })
})

describe('isBeforeAnchor', () => {
  const anchor = { date: '2026-05-10', locationName: 'Bank' }

  it('is true before the anchor', () => {
    expect(isBeforeAnchor('2026-05-09', anchor)).toBe(true)
  })

  it('is false on the anchor day itself', () => {
    expect(isBeforeAnchor('2026-05-10', anchor)).toBe(false)
  })

  it('is false after it, with no anchor, or with no date', () => {
    expect(isBeforeAnchor('2026-05-11', anchor)).toBe(false)
    expect(isBeforeAnchor('2026-01-01', null)).toBe(false)
    expect(isBeforeAnchor('', anchor)).toBe(false)
  })
})
