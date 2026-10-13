import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import ReconciliationBanner from '@/components/ReconciliationBanner'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { OpenReconciliationLine } from '@/lib/reconciliations'

function line(overrides: Partial<OpenReconciliationLine>): OpenReconciliationLine {
  return {
    id: 1,
    account_id: 1,
    recorded_amount: 100_000,
    actual_amount: 85_000,
    difference_amount: 15_000,
    resolution: 'left_open',
    adjustment_transaction_id: null,
    remainder_amount: 15_000,
    reconciliation_id: 1,
    ...overrides,
  }
}

describe('ReconciliationBanner', () => {
  it('renders "cocok" when a count has been taken and nothing is open', () => {
    render(<ReconciliationBanner openLines={[]} everReconciled />)
    expect(screen.getByText(copy.reconciliation.matched)).toBeInTheDocument()
  })

  // A fund nobody ever counted also has no open lines, and painting that green
  // would tell the treasurer her cash matches her records - which Uruni has
  // never checked until the reconcile flow asks what she counted.
  it('renders the neutral first-run state when no count has ever been taken', () => {
    render(<ReconciliationBanner openLines={[]} everReconciled={false} />)

    expect(screen.getByText(copy.reconciliation.neverChecked)).toBeInTheDocument()
    expect(screen.queryByText(copy.reconciliation.matched)).not.toBeInTheDocument()
  })

  it("sums every open line's remainder_amount and renders the selisih message", () => {
    const openLines = [
      line({ id: 1, difference_amount: 15_000, remainder_amount: 15_000 }),
      line({ id: 2, difference_amount: 5_000, remainder_amount: 5_000 }),
    ]
    render(<ReconciliationBanner openLines={openLines} everReconciled />)

    // 15_000 + 5_000 = 20_000, plain integer addition. The U+00A0 formatIDR
    // puts after "Rp" is collapsed to a space because getByText normalizes the
    // DOM text but never the string matcher.
    const expected = copy.reconciliation.discrepancy(formatIDR(20_000)).replace(/\u00a0/g, ' ')
    expect(screen.getByText(expected)).toBeInTheDocument()
    expect(screen.queryByText(copy.reconciliation.matched)).not.toBeInTheDocument()
  })

  // An entry that closed only part of the gap leaves the line open for the
  // REMAINDER. The 30_000 found at the count must not reach the sentence.
  it('reads the remainder of a partly closed line, not the difference that was found', () => {
    const partial = line({ difference_amount: 30_000, remainder_amount: 10_000, resolution: 'entry_added', adjustment_transaction_id: 7 })
    render(<ReconciliationBanner openLines={[partial]} everReconciled />)

    const expected = copy.reconciliation.discrepancy(formatIDR(10_000)).replace(/\u00a0/g, ' ')
    expect(screen.getByText(expected)).toBeInTheDocument()
    expect(screen.queryByText(copy.reconciliation.discrepancy(formatIDR(30_000)).replace(/\u00a0/g, ' '))).not.toBeInTheDocument()
  })

  it('sums by magnitude, so a short location and an over one never cancel out', () => {
    // difference_amount is signed (actual - recorded): -20_000 is a location
    // holding less than the ledger says, +20_000 one holding more. Summing the
    // raw values would render "selisih Rp 0" with two gaps still open, and
    // never "cocok"'s green - the worst of both.
    const openLines = [
      line({ id: 1, difference_amount: -20_000, remainder_amount: -20_000 }),
      line({ id: 2, difference_amount: 20_000, remainder_amount: 20_000 }),
    ]
    render(<ReconciliationBanner openLines={openLines} everReconciled />)

    const expected = copy.reconciliation.discrepancy(formatIDR(40_000)).replace(/\u00a0/g, ' ')
    expect(screen.getByText(expected)).toBeInTheDocument()
  })

  it('renders a single short location without a minus sign in the sentence', () => {
    render(<ReconciliationBanner openLines={[line({ difference_amount: -15_000, remainder_amount: -15_000 })]} everReconciled />)

    const expected = copy.reconciliation.discrepancy(formatIDR(15_000)).replace(/\u00a0/g, ' ')
    expect(screen.getByText(expected)).toBeInTheDocument()
  })

  // The colour is half the message: green is earned by a count, terracotta
  // marks a normal gap, alarm red never appears (CLAUDE.md rule 9). Pin the
  // token classes so a restyle can't swap them silently.
  describe('colour', () => {
    function panel(text: string) {
      return screen.getByText(text).closest('p')
    }

    it('paints "cocok" in the success tokens', () => {
      render(<ReconciliationBanner openLines={[]} everReconciled />)

      const el = panel(copy.reconciliation.matched)
      expect(el).toHaveClass('bg-success-soft', 'text-success')
      expect(el?.className).not.toMatch(/attention|destructive/)
    })

    it('paints selisih in the attention (terracotta) tokens, never destructive', () => {
      render(<ReconciliationBanner openLines={[line({ difference_amount: -15_000, remainder_amount: -15_000 })]} everReconciled />)

      const el = panel(copy.reconciliation.discrepancy(formatIDR(15_000)).replace(/\u00a0/g, ' '))
      expect(el).toHaveClass('bg-attention-soft', 'text-attention-text')
      expect(el?.className).not.toMatch(/success|destructive/)
    })

    it('keeps the never-checked state neutral, neither green nor terracotta', () => {
      render(<ReconciliationBanner openLines={[]} everReconciled={false} />)

      const el = panel(copy.reconciliation.neverChecked)
      expect(el).toHaveClass('bg-muted', 'text-muted-foreground')
      expect(el?.className).not.toMatch(/success|attention|destructive/)
    })

    it('keeps the same tokens when the banner is a button', () => {
      render(<ReconciliationBanner openLines={[line({})]} everReconciled onClick={() => {}} />)

      const button = screen.getByRole('button')
      expect(button).toHaveClass('bg-attention-soft', 'text-attention-text')
      expect(button.className).not.toMatch(/destructive/)
    })
  })
})
