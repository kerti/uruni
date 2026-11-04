import { render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import ReversalDialog from '@/components/ReversalDialog'
import { PERIOD_HYPHEN } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import type { Transaction } from '@/lib/transactions'

function payment(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: 1,
    account_id: 1,
    purpose_id: 1,
    direction: 'in',
    amount: 150_000,
    occurred_on: '2026-10-12',
    kind: 'dues',
    member_id: 5,
    dues_period: '2026-05',
    reimbursement_id: null,
    transfer_id: null,
    reverses_transaction_id: null,
    note: null,
    created_at: 1,
    account_name: 'Tunai',
    member_name: 'Rizki',
    dues_payment_id: 7,
    reversal_state: 'none',
    periods: [
      { dues_period: '2026-05', amount: 50_000, reversed: false },
      { dues_period: '2026-06', amount: 40_000, reversed: true },
      { dues_period: '2026-10', amount: 60_000, reversed: false },
    ],
    ...overrides,
  }
}

function open(transaction: Transaction) {
  render(<ReversalDialog transaction={transaction} open onClose={vi.fn()} onReversed={vi.fn()} />)
  return screen.getByRole('dialog')
}

describe('ReversalDialog shows the payment group (ADR-048)', () => {
  it('names all its months and the whole total', () => {
    const dialog = open(payment())

    expect(within(dialog).getByText(`Mei${PERIOD_HYPHEN}Jun, Okt 2026`)).toBeInTheDocument()
    expect(within(dialog).getByText(formatIDR(150_000))).toBeInTheDocument()
  })

  it('states only the months still standing for a part-reversed payment', () => {
    const dialog = open(payment({ reversal_state: 'partial' }))

    expect(within(dialog).getByText(formatIDR(110_000))).toBeInTheDocument()
    expect(within(dialog).queryByText(formatIDR(150_000))).not.toBeInTheDocument()
  })
})
