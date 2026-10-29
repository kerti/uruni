import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import ReconciliationLines from '@/components/ReconciliationLines'
import { copy } from '@/copy/id'
import type { ReconciliationLine } from '@/lib/reconciliations'

describe('ReconciliationLines', () => {
  it('labels both the recorded and the counted figure', () => {
    const line = {
      id: 1,
      account_id: 1,
      recorded_amount: 2_152_000,
      actual_amount: 2_152_000,
      difference_amount: 0,
      remainder_amount: 0,
      resolution: 'matched',
    } as ReconciliationLine
    render(<ReconciliationLines lines={[line]} accountNames={new Map([[1, 'Tunai']])} />)
    expect(
      screen.getByText(`${copy.reconciliation.recordedLabel} Rp2.152.000 \u00b7 ${copy.reconciliation.countedLabel} Rp2.152.000`),
    ).toBeInTheDocument()
  })
})
