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
    reconciliation_id: 1,
    ...overrides,
  }
}

describe('ReconciliationBanner', () => {
  it('renders "cocok" when a count has been taken and nothing is open', () => {
    render(<ReconciliationBanner openLines={[]} everReconciled />)
    expect(screen.getByText(copy.reconciliation.matched)).toBeInTheDocument()
  })

  // The whole reason this prop exists: a fund nobody ever counted also has
  // no open lines, and painting that green would tell the treasurer her cash
  // matches her records - something Uruni has never checked and cannot know
  // until the reconcile flow (M6.10) asks her what she actually counted.
  it('renders the neutral first-run state when no count has ever been taken', () => {
    render(<ReconciliationBanner openLines={[]} everReconciled={false} />)

    expect(screen.getByText(copy.reconciliation.neverChecked)).toBeInTheDocument()
    expect(screen.queryByText(copy.reconciliation.matched)).not.toBeInTheDocument()
  })

  it('sums every open line\'s difference_amount and renders the selisih message', () => {
    const openLines = [line({ id: 1, difference_amount: 15_000 }), line({ id: 2, difference_amount: 5_000 })]
    render(<ReconciliationBanner openLines={openLines} everReconciled />)

    // 15_000 + 5_000 = 20_000, plain integer addition - no float anywhere
    // near a rupiah figure. The U+00A0 formatIDR puts after "Rp" is
    // collapsed to a regular space here because getByText normalizes the
    // DOM text it searches but never the string matcher itself.
    const expected = copy.reconciliation.discrepancy(formatIDR(20_000)).replace(/ /g, ' ')
    expect(screen.getByText(expected)).toBeInTheDocument()
    expect(screen.queryByText(copy.reconciliation.matched)).not.toBeInTheDocument()
  })

  it('sums by magnitude, so a short location and an over one never cancel out', () => {
    // difference_amount is signed (actual - recorded): -20_000 is a location
    // holding less than the ledger says, +20_000 one holding more. Summing
    // the raw values would render "selisih Rp 0" with two gaps still open,
    // and would never render "cocok"'s green - the worst of both.
    const openLines = [line({ id: 1, difference_amount: -20_000 }), line({ id: 2, difference_amount: 20_000 })]
    render(<ReconciliationBanner openLines={openLines} everReconciled />)

    const expected = copy.reconciliation.discrepancy(formatIDR(40_000)).replace(/\u00a0/g, ' ')
    expect(screen.getByText(expected)).toBeInTheDocument()
  })

  it('renders a single short location without a minus sign in the sentence', () => {
    render(<ReconciliationBanner openLines={[line({ difference_amount: -15_000 })]} everReconciled />)

    const expected = copy.reconciliation.discrepancy(formatIDR(15_000)).replace(/\u00a0/g, ' ')
    expect(screen.getByText(expected)).toBeInTheDocument()
  })
})
