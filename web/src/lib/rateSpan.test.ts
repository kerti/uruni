import { describe, expect, it } from 'vitest'

import { rateInForce, rateSpan } from '@/lib/rateSpan'
import type { DuesRate } from '@/lib/setup'

function rate(id: number, effectiveFrom: string): DuesRate {
  return { id, tier_id: 1, amount: 50_000, effective_from: effectiveFrom, created_at: 1 }
}

describe('rateSpan', () => {
  it('runs on without end when no later rate stands', () => {
    const rates = [rate(1, '2026-01'), rate(2, '2026-07')]
    expect(rateSpan(rates, rates[1])).toEqual({ from: '2026-07', until: null })
  })

  it('stops the month before the next rate', () => {
    const rates = [rate(1, '2026-01'), rate(2, '2026-07')]
    expect(rateSpan(rates, rates[0])).toEqual({ from: '2026-01', until: '2026-06' })
  })

  it('is a single month when the next rate starts the month after', () => {
    const rates = [rate(1, '2026-03'), rate(2, '2026-04')]
    expect(rateSpan(rates, rates[0])).toEqual({ from: '2026-03', until: '2026-03' })
  })

  it('crosses a year boundary', () => {
    const rates = [rate(1, '2026-06'), rate(2, '2027-01')]
    expect(rateSpan(rates, rates[0])).toEqual({ from: '2026-06', until: '2026-12' })
  })

  it('takes the nearest later rate even from an unordered list', () => {
    const rates = [rate(3, '2027-01'), rate(1, '2026-01'), rate(2, '2026-09')]
    expect(rateSpan(rates, rates[1])).toEqual({ from: '2026-01', until: '2026-08' })
  })

  it('reaches onward for a lone rate', () => {
    const rates = [rate(1, '2026-01')]
    expect(rateSpan(rates, rates[0])).toEqual({ from: '2026-01', until: null })
  })
})

describe('rateInForce (#748)', () => {
  it('is the latest rate already started, whatever the order, ignoring one still to come', () => {
    const rates = [rate(3, '2027-01'), rate(1, '2025-01'), rate(2, '2026-06')]
    expect(rateInForce(rates, '2026-10')?.id).toBe(2)
    expect(rateInForce(rates, '2026-06')?.id).toBe(2)
    expect(rateInForce(rates, '2027-01')?.id).toBe(3)
  })

  it('is null when every rate starts later, or there is none', () => {
    expect(rateInForce([rate(1, '2027-01')], '2026-10')).toBeNull()
    expect(rateInForce([], '2026-10')).toBeNull()
  })
})
