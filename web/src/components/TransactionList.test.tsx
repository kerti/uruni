import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { afterEach, describe, expect, it } from 'vitest'

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

/** The same list Riwayat renders: with a correction handler, so an eligible
 * row's peruntukan is a control (#276). Home passes no handler, which is
 * what renderRows above still covers. */
function renderCorrectableRows(rows: Transaction[], onCorrectPurpose = vi.fn()) {
  const result = render(
    <TransactionList
      transactions={rows}
      purposeNames={purposeNames}
      emptyMessage="Belum ada."
      onCorrectPurpose={onCorrectPurpose}
    />,
  )
  return { ...result, onCorrectPurpose }
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

describe('TransactionList purpose correction (#276, ADR-033)', () => {
  it('labels a correction leg with Tags and Perbaikan peruntukan, never Tutup amplop', () => {
    // A roll and a correction are the same shape on the wire - kind='transfer',
    // transfer_kind='reclass_purpose' - and only corrects_transaction_id
    // tells them apart. Before this, every correction read as an envelope
    // closing that never happened.
    const { container } = renderRows([
      transaction({
        kind: 'transfer', transfer_id: 9, transfer_kind: 'reclass_purpose',
        transfer_corrects_transaction_id: 4,
        transfer_from_name: 'Titipan', transfer_to_name: 'Kas Utama',
      }),
    ])

    expect(container.querySelector('.lucide-tags')).toBeInTheDocument()
    expect(container.querySelector('.lucide-mail')).not.toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferPurposeCorrection.kind)).toBeInTheDocument()
    expect(screen.queryByText(copy.rowLabels.transferPurpose.kind)).not.toBeInTheDocument()
  })

  it('makes an eligible row\'s peruntukan the control, and hands the row back on tap', async () => {
    const { onCorrectPurpose } = renderCorrectableRows([transaction({ id: 12, purpose_id: 1 })])

    const control = screen.getByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') })
    await userEvent.click(control)

    expect(onCorrectPurpose).toHaveBeenCalledTimes(1)
    expect(onCorrectPurpose.mock.calls[0][0].id).toBe(12)
  })

  it('leaves an ineligible row\'s peruntukan as plain text - a dead tap is worse than none', () => {
    // Eligibility varies row by row (ADR-033), which is exactly why the
    // whole row is not tappable: a dues row and the expense beneath it
    // would look identical and behave differently.
    renderCorrectableRows([
      transaction({ id: 13, kind: 'dues', member_id: 1, dues_period: '2026-08', member_name: 'Budi' }),
    ])

    expect(screen.queryByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') })).not.toBeInTheDocument()
    expect(screen.getByText('Kas Utama')).toBeInTheDocument()
  })

  it('leaves a dues reversal ineligible, though it is an adjustment', () => {
    renderCorrectableRows([
      transaction({
        id: 14, kind: 'adjustment', direction: 'out', reverses_transaction_id: 13,
        member_id: 1, dues_period: '2026-08', member_name: 'Budi',
      }),
    ])

    expect(screen.queryByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') })).not.toBeInTheDocument()
  })

  it('offers no control at all where no handler is passed - Beranda stays inert', () => {
    renderRows([transaction({ id: 15 })])

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.getByText('Kas Utama')).toBeInTheDocument()
  })

  it('marks a corrected row with the glyph and an sr-only sentence, still showing its STORED tag', () => {
    // The stored tag is what renders even though the money is elsewhere:
    // the ledger sums stored tags, so a row showing its effective one would
    // put the screen out of step with the balances (ADR-033).
    const { container } = renderCorrectableRows([
      transaction({ id: 16, purpose_id: 2, effective_purpose_id: 1 }),
    ])

    expect(screen.getByText("Jane's wedding")).toBeInTheDocument()
    expect(container.querySelector('.lucide-tags')).toBeInTheDocument()
    expect(screen.getByText(copy.purposeCorrection.corrected)).toBeInTheDocument()
  })

  it('marks nothing on a row whose effective tag is its own', () => {
    const { container } = renderCorrectableRows([
      transaction({ id: 17, purpose_id: 1, effective_purpose_id: 1 }),
    ])

    expect(container.querySelector('.lucide-tags')).not.toBeInTheDocument()
    expect(screen.queryByText(copy.purposeCorrection.corrected)).not.toBeInTheDocument()
  })
})

describe('TransactionList receipt photos (#154)', () => {
  const text = copy.receipts

  function jsonResponse(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  }

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function renderWithReceipts(rows: Transaction[], onReceiptsChanged = vi.fn()) {
    const result = render(
      <TransactionList transactions={rows} purposeNames={purposeNames} emptyMessage="Belum ada." onReceiptsChanged={onReceiptsChanged} />,
    )
    return { ...result, onReceiptsChanged }
  }

  it('offers "Tambah foto nota" on a row with no photo, and uploads through the transactions route', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL) => Promise.resolve(jsonResponse({ id: 9, uploaded_at: 1 }, 201)))
    vi.stubGlobal('fetch', fetchMock)

    const { onReceiptsChanged } = renderWithReceipts([transaction({ id: 20, receipt_ids: [] })])

    await userEvent.click(screen.getByRole('button', { name: text.addFromRow }))
    const dialog = await screen.findByRole('dialog')
    // Blank state: no photo yet, so the dialog says so before the picker.
    expect(within(dialog).getByText(text.emptyTransaction)).toBeInTheDocument()
    await userEvent.upload(
      within(dialog).getByLabelText(text.addFromRow, { selector: 'input[type="file"]' }),
      new File(['fake-bytes'], 'nota.jpg', { type: 'image/jpeg' }),
    )
    await userEvent.click(within(dialog).getByRole('button', { name: text.addFromRow }))

    await waitFor(() => expect(onReceiptsChanged).toHaveBeenCalled())
    const uploadCall = fetchMock.mock.calls.find(([input]) => (input as string).toString().includes('/receipts'))
    expect(uploadCall).toBeDefined()
    expect((uploadCall![0] as string).toString()).toContain('/api/transactions/20/receipts')
  })

  it('opens the per-photo menu with both Ganti foto and Hapus foto', async () => {
    vi.stubGlobal('fetch', vi.fn())

    renderWithReceipts([transaction({ id: 24, receipt_ids: [42] })])

    await userEvent.click(screen.getByRole('button', { name: text.viewReceipt }))
    const dialog = await screen.findByRole('dialog')

    await userEvent.click(within(dialog).getByRole('button', { name: text.photoMenuAria }))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: text.change })).toBeInTheDocument()
    expect(within(menu).getByRole('menuitem', { name: text.delete })).toBeInTheDocument()
  })

  it('offers "Lihat nota" on a row with a photo, and deletes it through the menu after the confirm', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })))
    vi.stubGlobal('fetch', fetchMock)

    const { onReceiptsChanged } = renderWithReceipts([transaction({ id: 21, receipt_ids: [42] })])

    await userEvent.click(screen.getByRole('button', { name: text.viewReceipt }))
    const dialog = await screen.findByRole('dialog')

    // Hapus foto lives inside the "more" menu on the photo itself, never
    // in a footer under it - opening it is what surfaces the inline
    // confirm bar, not the delete API call directly.
    await userEvent.click(within(dialog).getByRole('button', { name: text.photoMenuAria }))
    await userEvent.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: text.delete }))
    expect(within(dialog).getByText(text.deleteConfirm)).toBeInTheDocument()
    await userEvent.click(within(dialog).getByRole('button', { name: text.delete }))

    await waitFor(() => expect(onReceiptsChanged).toHaveBeenCalled())
    expect(fetchMock).toHaveBeenCalledWith('/api/receipts/42', expect.objectContaining({ method: 'DELETE' }))
  })

  it('replaces a photo through the menu: delete-then-upload, both through the receipt routes', async () => {
    const calls: string[] = []
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push(`${(init?.method ?? 'GET').toUpperCase()} ${input.toString()}`)
      if ((init?.method ?? 'GET').toUpperCase() === 'DELETE') return Promise.resolve(new Response(null, { status: 204 }))
      return Promise.resolve(jsonResponse({ id: 43, uploaded_at: 2 }, 201))
    })
    vi.stubGlobal('fetch', fetchMock)

    const { onReceiptsChanged } = renderWithReceipts([transaction({ id: 23, receipt_ids: [42] })])

    await userEvent.click(screen.getByRole('button', { name: text.viewReceipt }))
    const dialog = await screen.findByRole('dialog')

    await userEvent.click(within(dialog).getByRole('button', { name: text.photoMenuAria }))
    await userEvent.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: text.change }))
    await userEvent.upload(
      within(dialog).getByLabelText(text.addFromRow, { selector: 'input[type="file"]' }),
      new File(['fake-bytes'], 'nota-baru.jpg', { type: 'image/jpeg' }),
    )
    await userEvent.click(within(dialog).getByRole('button', { name: text.change }))

    await waitFor(() => expect(onReceiptsChanged).toHaveBeenCalled())
    // Delete-then-upload, in that order (no combined replace route) -
    // see ReceiptDialog.tsx's own comment on why that order is the safer
    // of the two ways a replace can go half-done.
    expect(calls[0]).toBe('DELETE /api/receipts/42')
    expect(calls[1]).toBe('POST /api/transactions/23/receipts')
  })

  it('opens the full-screen viewer when the photo itself is tapped', async () => {
    vi.stubGlobal('fetch', vi.fn())

    renderWithReceipts([transaction({ id: 25, receipt_ids: [42] })])

    await userEvent.click(screen.getByRole('button', { name: text.viewReceipt }))
    const dialog = await screen.findByRole('dialog')

    await userEvent.click(within(dialog).getByRole('button', { name: text.zoomAria }))

    // Two dialogs are now open: ReceiptDialog itself and the viewer layered
    // above it. `hidden: true` because Radix marks the first inert (and so
    // hidden from the accessibility tree) while the second sits on top of
    // it, the same "hides the row underneath" behaviour Reimbursements'
    // own receipt tests already rely on.
    await waitFor(() => expect(screen.getAllByRole('dialog', { hidden: true })).toHaveLength(2))
  })

  it('renders no receipt control at all where onReceiptsChanged is not passed', () => {
    renderRows([transaction({ id: 22, receipt_ids: [] })])

    expect(screen.queryByRole('button', { name: text.addFromRow })).not.toBeInTheDocument()
  })
})
