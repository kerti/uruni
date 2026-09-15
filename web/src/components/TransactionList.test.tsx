import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import TransactionList from '@/components/TransactionList'
import { copy } from '@/copy/id'
import { formatPeriod } from '@/lib/dates'
import type { Transaction } from '@/lib/transactions'

const purposeNames = new Map([
  [1, 'Kas Utama'],
  [2, "Jane's wedding"],
])

function transaction(overrides: Partial<Transaction> = {}): Transaction {
  return {
    id: 1,
    account_id: 1,
    purpose_id: 1,
    direction: 'in',
    amount: 25_000,
    occurred_on: '2026-08-12',
    kind: 'normal',
    member_id: null,
    dues_period: null,
    reimbursement_id: null,
    transfer_id: null,
    reverses_transaction_id: null,
    note: null,
    created_at: 1,
    account_name: 'Tunai',
    member_name: null,
    claim_note: null,
    transfer_kind: null,
    transfer_from_name: null,
    transfer_to_name: null,
    is_reconciliation_fix: false,
    ...overrides,
  }
}

function renderRows(rows: Transaction[]) {
  return render(<TransactionList transactions={rows} purposeNames={purposeNames} emptyMessage="Belum ada." />)
}

describe('TransactionList row labels (#257)', () => {
  it('labels a dues payment: CalendarCheck, "{period} - {anggota}", and Iuran for screen readers', () => {
    const { container } = renderRows([
      transaction({ kind: 'dues', dues_period: '2026-08', member_id: 5, member_name: 'Budi' }),
    ])

    expect(container.querySelector('.lucide-calendar-check')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.dues.text(formatPeriod('2026-08'), 'Budi'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.dues.kind)).toBeInTheDocument()
  })

  it('labels a dues reversal: Undo2, "Pembatalan - {period} - {anggota}", with no duplicate screen-reader word', () => {
    const { container } = renderRows([
      transaction({
        kind: 'adjustment', direction: 'out', dues_period: '2026-08',
        member_id: 5, member_name: 'Budi', reverses_transaction_id: 10,
      }),
    ])

    expect(container.querySelector('.lucide-undo-2')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.duesReversal.text(formatPeriod('2026-08'), 'Budi'))).toBeInTheDocument()
    // The visible text already starts with the kind word, so there is no
    // sr-only copy of it to read twice.
    expect(document.querySelector('.sr-only')).toBeNull()
  })

  it('labels an opening balance: Flag, "Saldo awal - {lokasi}", with no duplicate screen-reader word', () => {
    const { container } = renderRows([transaction({ kind: 'opening', account_name: 'Bank Jago' })])

    expect(container.querySelector('.lucide-flag')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.opening.text('Bank Jago'))).toBeInTheDocument()
    // The visible text already starts with the kind word, so there is no
    // sr-only copy of it to read twice.
    expect(document.querySelector('.sr-only')).toBeNull()
  })

  it('labels a Talangan settlement with HandHelping and the member\'s own name, and renders the claim\'s note on the note line', () => {
    const { container } = renderRows([
      transaction({
        kind: 'reimbursement', direction: 'out', reimbursement_id: 3,
        member_name: 'Jane', claim_note: 'Beli galon', note: null,
      }),
    ])

    expect(container.querySelector('.lucide-hand-helping')).toBeInTheDocument()
    // The visible label text is just the member's name - the claim note is
    // not repeated inside it.
    expect(screen.getByText(copy.rowLabels.settlement.text('Jane'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.settlement.kind)).toBeInTheDocument()
    // The settlement's own Note is nil (#257) - what renders on the note
    // line is the settled claim's own note instead.
    expect(screen.getByText('Beli galon')).toBeInTheDocument()
  })

  it('labels a location transfer leg with ArrowLeftRight and "{dari} -> {ke}", and Pindah lokasi for screen readers', () => {
    const { container } = renderRows([
      transaction({
        kind: 'transfer', transfer_id: 7, transfer_kind: 'between_accounts',
        transfer_from_name: 'Tunai', transfer_to_name: 'Bank',
      }),
    ])

    expect(container.querySelector('.lucide-arrow-left-right')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferLocation.text('Tunai', 'Bank'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferLocation.kind)).toBeInTheDocument()
  })

  it('labels an incidental roll leg with Mail and "{amplop} -> Kas Utama", and Tutup amplop for screen readers', () => {
    const { container } = renderRows([
      transaction({
        kind: 'transfer', transfer_id: 8, transfer_kind: 'reclass_purpose',
        transfer_from_name: "Jane's wedding", transfer_to_name: 'Kas Utama',
      }),
    ])

    expect(container.querySelector('.lucide-mail')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferPurpose.text("Jane's wedding", 'Kas Utama'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferPurpose.kind)).toBeInTheDocument()
  })

  it('labels a reconciliation adjusted fix with Scale and "Penyesuaian - {lokasi}", with no duplicate screen-reader word', () => {
    const { container } = renderRows([
      transaction({ kind: 'adjustment', direction: 'out', is_reconciliation_fix: true, account_name: 'Tunai' }),
    ])

    expect(container.querySelector('.lucide-scale')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.reconciliationFix.text('Tunai'))).toBeInTheDocument()
    // The visible text already starts with the kind word, so there is no
    // sr-only copy of it to read twice.
    expect(document.querySelector('.sr-only')).toBeNull()
  })

  it('renders her typed note alongside the label, on its own line', () => {
    renderRows([
      transaction({
        kind: 'adjustment', direction: 'out', dues_period: '2026-08',
        member_id: 5, member_name: 'Budi', reverses_transaction_id: 10,
        note: 'Salah input jumlahnya',
      }),
    ])

    expect(screen.getByText(copy.rowLabels.duesReversal.text(formatPeriod('2026-08'), 'Budi'))).toBeInTheDocument()
    expect(screen.getByText('Salah input jumlahnya')).toBeInTheDocument()
  })

  it('renders a normal row exactly as before: no icon, no sr-only kind word, just her note', () => {
    const { container } = renderRows([transaction({ kind: 'normal', note: 'Beli konsumsi rapat' })])

    // None of the label icons this row could not possibly have rendered.
    for (const cls of ['.lucide-calendar-check', '.lucide-undo-2', '.lucide-flag', '.lucide-hand-helping', '.lucide-arrow-left-right', '.lucide-mail', '.lucide-scale']) {
      expect(container.querySelector(cls)).not.toBeInTheDocument()
    }
    expect(screen.getByText('Beli konsumsi rapat')).toBeInTheDocument()
    expect(screen.getByText('Kas Utama')).toBeInTheDocument()
  })

  it('renders no label for an ordinary adjustment that is neither a reversal nor a reconciliation fix', () => {
    const { container } = renderRows([
      transaction({ kind: 'adjustment', direction: 'out', note: 'Koreksi salah catat' }),
    ])

    expect(container.querySelector('.lucide-undo-2')).not.toBeInTheDocument()
    expect(container.querySelector('.lucide-scale')).not.toBeInTheDocument()
    expect(screen.getByText('Koreksi salah catat')).toBeInTheDocument()
  })
})
