import { render as rtlRender, screen, waitFor, within } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import userEvent from '@testing-library/user-event'
import { vi } from 'vitest'
import { afterEach, describe, expect, it } from 'vitest'

import TransactionList from '@/components/TransactionList'
import { copy } from '@/copy/id'
import { formatPeriod } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import type { Transaction } from '@/lib/transactions'

// TransactionList keeps its entry-detail dialog in the URL (ADR-032), so
// every render needs a router.
function render(ui: ReactElement) {
  return rtlRender(ui, { wrapper: MemoryRouter })
}

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
    ...overrides,
  }
}

function renderRows(rows: Transaction[]) {
  return render(<TransactionList transactions={rows} purposeNames={purposeNames} emptyMessage="Belum ada." />)
}

/** The same list Riwayat renders: with a correction handler, so an eligible
 * row's pos is a control. Home passes none, which renderRows covers. */
function renderCorrectableRows(rows: Transaction[], onCorrectPurpose = vi.fn()) {
  const result = render(
    <TransactionList transactions={rows} purposeNames={purposeNames} emptyMessage="Belum ada." onCorrectPurpose={onCorrectPurpose} />,
  )
  return { ...result, onCorrectPurpose }
}

describe('TransactionList row labels (#257)', () => {
  it('labels a dues payment: CalendarCheck, "{period} - {anggota}", and Iuran for screen readers', () => {
    const { container } = renderRows([transaction({ kind: 'dues', dues_period: '2026-08', member_id: 5, member_name: 'Budi' })])

    expect(container.querySelector('.lucide-calendar-check')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.dues.text(formatPeriod('2026-08'), 'Budi'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.dues.kind)).toBeInTheDocument()
  })

  it('labels a dues reversal: Undo2, "Pembatalan - {period} - {anggota}", with no duplicate screen-reader word', () => {
    const { container } = renderRows([
      transaction({
        kind: 'adjustment',
        direction: 'out',
        dues_period: '2026-08',
        member_id: 5,
        member_name: 'Budi',
        reverses_transaction_id: 10,
      }),
    ])

    expect(container.querySelector('.lucide-undo-2')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.duesReversal.text(formatPeriod('2026-08'), 'Budi'))).toBeInTheDocument()
    // The visible text already starts with the kind word: no sr-only copy.
    expect(document.querySelector('.sr-only')).toBeNull()
  })

  it("labels a named contribution with HandCoins and the member's name, and Sumbangan for screen readers", () => {
    const { container } = renderRows([transaction({ purpose_id: 2, member_id: 5, member_name: 'Budi' })])

    expect(container.querySelector('.lucide-hand-coins')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.contribution.text('Budi'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.contribution.kind)).toHaveClass('sr-only')
  })

  it('labels a contribution reversal "Pembatalan - {anggota}", with no empty period', () => {
    const { container } = renderRows([
      transaction({ kind: 'adjustment', direction: 'out', purpose_id: 2, member_id: 5, member_name: 'Budi', reverses_transaction_id: 10 }),
    ])

    expect(container.querySelector('.lucide-undo-2')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.contributionReversal.text('Budi'))).toBeInTheDocument()
    expect(document.querySelector('.sr-only')).toBeNull()
  })

  it('labels an opening balance: Flag, "Saldo awal - {lokasi}", with no duplicate screen-reader word', () => {
    const { container } = renderRows([transaction({ kind: 'opening', account_name: 'Bank Jago' })])

    expect(container.querySelector('.lucide-flag')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.opening.text('Bank Jago'))).toBeInTheDocument()
    // The visible text already starts with the kind word: no sr-only copy.
    expect(document.querySelector('.sr-only')).toBeNull()
  })

  it("labels a Talangan settlement with HandHelping and the member's own name, and renders the claim's note on the note line", () => {
    const { container } = renderRows([
      transaction({
        kind: 'reimbursement',
        direction: 'out',
        reimbursement_id: 3,
        member_name: 'Jane',
        claim_note: 'Beli galon',
        note: null,
      }),
    ])

    expect(container.querySelector('.lucide-hand-helping')).toBeInTheDocument()
    // The visible label text is just the member's name - the claim note is
    // not repeated inside it.
    expect(screen.getByText(copy.rowLabels.settlement.text('Jane'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.settlement.kind)).toBeInTheDocument()
    // The settlement's Note is nil - the note line shows the settled claim's.
    expect(screen.getByText('Beli galon')).toBeInTheDocument()
  })

  it('labels a location transfer leg with ArrowLeftRight and "{dari} -> {ke}", and Pindah lokasi for screen readers', () => {
    const { container } = renderRows([
      transaction({
        kind: 'transfer',
        transfer_id: 7,
        transfer_kind: 'between_accounts',
        transfer_from_name: 'Tunai',
        transfer_to_name: 'Bank',
      }),
    ])

    expect(container.querySelector('.lucide-arrow-left-right')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferLocation.text('Tunai', 'Bank'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferLocation.kind)).toBeInTheDocument()
  })

  it('labels an incidental roll leg with Mail and "{amplop} -> Kas Utama", and Tutup amplop for screen readers', () => {
    const { container } = renderRows([
      transaction({
        kind: 'transfer',
        transfer_id: 8,
        transfer_kind: 'reclass_purpose',
        transfer_from_name: "Jane's wedding",
        transfer_to_name: 'Kas Utama',
      }),
    ])

    expect(container.querySelector('.lucide-mail')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferPurpose.text("Jane's wedding", 'Kas Utama'))).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferPurpose.kind)).toBeInTheDocument()
  })

  it('labels a reconciliation adjusted fix with Scale and "Penyesuaian - {lokasi}", with no duplicate screen-reader word', () => {
    const { container } = renderRows([transaction({ kind: 'adjustment', direction: 'out', account_name: 'Tunai' })])

    expect(container.querySelector('.lucide-scale')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.adjustment.text('Tunai'))).toBeInTheDocument()
    // The visible text already starts with the kind word: no sr-only copy.
    expect(document.querySelector('.sr-only')).toBeNull()
  })

  it('renders her typed note alongside the label, on its own line', () => {
    renderRows([
      transaction({
        kind: 'adjustment',
        direction: 'out',
        dues_period: '2026-08',
        member_id: 5,
        member_name: 'Budi',
        reverses_transaction_id: 10,
        note: 'Salah input jumlahnya',
      }),
    ])

    expect(screen.getByText(copy.rowLabels.duesReversal.text(formatPeriod('2026-08'), 'Budi'))).toBeInTheDocument()
    expect(screen.getByText('Salah input jumlahnya')).toBeInTheDocument()
  })

  it('renders a normal row exactly as before: no icon, no sr-only kind word, just her note', () => {
    const { container } = renderRows([transaction({ kind: 'normal', note: 'Beli konsumsi rapat' })])

    // None of the label icons this row could not possibly have rendered.
    for (const cls of [
      '.lucide-calendar-check',
      '.lucide-undo-2',
      '.lucide-flag',
      '.lucide-hand-helping',
      '.lucide-arrow-left-right',
      '.lucide-mail',
      '.lucide-scale',
    ]) {
      expect(container.querySelector(cls)).not.toBeInTheDocument()
    }
    expect(screen.getByText('Beli konsumsi rapat')).toBeInTheDocument()
    expect(screen.getByText('Kas Utama')).toBeInTheDocument()
  })

  it("labels a standalone adjustment Penyesuaian too, the report's word for it (ADR-038), and keeps her note", () => {
    const { container } = renderRows([
      transaction({ kind: 'adjustment', direction: 'out', account_name: 'Tunai', note: 'Koreksi salah catat' }),
    ])

    expect(container.querySelector('.lucide-undo-2')).not.toBeInTheDocument()
    expect(container.querySelector('.lucide-scale')).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.adjustment.text('Tunai'))).toBeInTheDocument()
    expect(screen.getByText('Koreksi salah catat')).toBeInTheDocument()
  })
})

describe('TransactionList purpose correction (#276, ADR-033)', () => {
  it('labels a correction leg with Tags and Perbaikan pos, never Tutup amplop', () => {
    // A roll and a correction are the same shape on the wire - kind='transfer',
    // transfer_kind='reclass_purpose' - and only corrects_transaction_id tells
    // them apart.
    const { container } = renderRows([
      transaction({
        kind: 'transfer',
        transfer_id: 9,
        transfer_kind: 'reclass_purpose',
        transfer_corrects_transaction_id: 4,
        transfer_from_name: 'Titipan',
        transfer_to_name: 'Kas Utama',
      }),
    ])

    expect(container.querySelector('.lucide-tags')).toBeInTheDocument()
    expect(container.querySelector('.lucide-mail')).not.toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferPurposeCorrection.kind)).toBeInTheDocument()
    expect(screen.queryByText(copy.rowLabels.transferPurpose.kind)).not.toBeInTheDocument()
  })

  it("makes an eligible row's pos the control, and hands the row back on tap", async () => {
    const { onCorrectPurpose } = renderCorrectableRows([transaction({ id: 12, purpose_id: 1 })])

    const control = screen.getByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') })
    await userEvent.click(control)

    expect(onCorrectPurpose).toHaveBeenCalledTimes(1)
    expect(onCorrectPurpose.mock.calls[0][0].id).toBe(12)
  })

  it("leaves an ineligible row's pos as plain text - a dead tap is worse than none", () => {
    // Eligibility varies row by row (ADR-033), which is why the whole row is
    // not tappable: a dues row and the expense beneath it would look identical
    // and behave differently.
    renderCorrectableRows([transaction({ id: 13, kind: 'dues', member_id: 1, dues_period: '2026-08', member_name: 'Budi' })])

    expect(screen.queryByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') })).not.toBeInTheDocument()
    expect(screen.getByText('Kas Utama')).toBeInTheDocument()
  })

  it('leaves a dues reversal ineligible, though it is an adjustment', () => {
    renderCorrectableRows([
      transaction({
        id: 14,
        kind: 'adjustment',
        direction: 'out',
        reverses_transaction_id: 13,
        member_id: 1,
        dues_period: '2026-08',
        member_name: 'Budi',
      }),
    ])

    expect(screen.queryByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') })).not.toBeInTheDocument()
  })

  it('offers no control at all where no handler is passed - Beranda stays inert', () => {
    renderRows([transaction({ id: 15 })])

    // The only button left is the card's read-only detail tap.
    expect(screen.getAllByRole('button').map((b) => b.getAttribute('aria-label'))).toEqual([
      copy.transactionDetail.openAria('Kas Utama', formatIDR(25_000), '12 Agustus 2026'),
    ])
    expect(screen.getByText('Kas Utama')).toBeInTheDocument()
  })

  it('marks a corrected row with the glyph and an sr-only sentence, still showing its STORED tag', () => {
    // The stored tag renders even though the money is elsewhere: the ledger
    // sums stored tags, so showing the effective one would put the screen out
    // of step with the balances (ADR-033).
    const { container } = renderCorrectableRows([transaction({ id: 16, purpose_id: 2, effective_purpose_id: 1 })])

    expect(screen.getByText("Jane's wedding")).toBeInTheDocument()
    expect(container.querySelector('.lucide-tags')).toBeInTheDocument()
    expect(screen.getByText(copy.purposeCorrection.corrected)).toBeInTheDocument()
  })

  it('marks nothing on a row whose effective tag is its own', () => {
    const { container } = renderCorrectableRows([transaction({ id: 17, purpose_id: 1, effective_purpose_id: 1 })])

    expect(container.querySelector('.lucide-tags')).not.toBeInTheDocument()
    expect(screen.queryByText(copy.purposeCorrection.corrected)).not.toBeInTheDocument()
  })

  it('leaves a named contribution ineligible (ADR-034): moving it would strand participation', () => {
    renderCorrectableRows([transaction({ id: 18, purpose_id: 2, member_id: 9, member_name: 'Budi' })])

    expect(screen.queryByRole('button', { name: copy.purposeCorrection.controlAria("Jane's wedding") })).not.toBeInTheDocument()
    expect(screen.getByText("Jane's wedding")).toBeInTheDocument()
  })
})

describe('TransactionList reversal (ADR-042)', () => {
  const text = copy.history.transactions

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function jsonResponse(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
  }

  function renderReversible(rows: Transaction[], onReversed = vi.fn()) {
    const result = render(
      <TransactionList transactions={rows} purposeNames={purposeNames} emptyMessage="Belum ada." onReversed={onReversed} />,
    )
    return { ...result, onReversed }
  }

  /** Taps the card, then the door in the entry detail. */
  async function openForm() {
    await userEvent.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))
    const dialog = await screen.findByRole('dialog', { name: copy.transactionDetail.heading })
    await userEvent.click(within(dialog).getByRole('button', { name: text.reverse }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  }

  it('offers Batalkan transaksi in the entry detail of a reversible row, and the button posts nothing', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    renderReversible([transaction({ id: 30, reversible: true })])

    expect(screen.queryByRole('button', { name: text.reverse })).not.toBeInTheDocument()

    await openForm()

    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: text.reverseConfirm })).toBeInTheDocument()
    expect(screen.getByText(text.reverseExplainer)).toBeInTheDocument()
  })

  it('offers it on a dues row too: the door follows the server, not the kind', async () => {
    renderReversible([transaction({ id: 31, kind: 'dues', member_id: 3, member_name: 'Ani', dues_period: '2026-08', reversible: true })])

    await userEvent.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('button', { name: text.reverse })).toBeInTheDocument()
  })

  it.each([
    ['a row the ledger would refuse', { reversible: false }],
    ['a row with no flag at all', {}],
    ['a row something already reverses', { reversed: true, reversible: false }],
  ])('shows no door on %s', async (_name, flags) => {
    renderReversible([transaction({ id: 32, ...flags })])

    await userEvent.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('button', { name: text.reverse })).not.toBeInTheDocument()
  })

  it('shows no door where no handler is passed (Beranda)', async () => {
    renderRows([transaction({ id: 35, reversible: true })])

    await userEvent.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('button', { name: text.reverse })).not.toBeInTheDocument()
  })

  it("states the original's date when no cek kas has counted the row", async () => {
    renderReversible([transaction({ id: 33, reversible: true, counted: false, occurred_on: '2026-08-12' })])

    await openForm()

    expect(screen.getByText(text.reverseDateOriginal('12 Agustus 2026'))).toBeInTheDocument()
    expect(screen.getByText('Dicatat pada 12 Agustus 2026.')).toBeInTheDocument()
    expect(screen.queryByText(text.reverseDateToday)).not.toBeInTheDocument()
    expect(screen.queryByText(copy.record.dateLabel)).not.toBeInTheDocument()
  })

  it('says it is recorded today once a cek kas has counted the row', async () => {
    renderReversible([transaction({ id: 34, reversible: true, counted: true, occurred_on: '2026-08-12' })])

    await openForm()

    expect(screen.getByText('Dicatat hari ini, karena kas sudah dicek sejak transaksi ini.')).toBeInTheDocument()
    expect(screen.queryByText(/^Dicatat pada/)).not.toBeInTheDocument()
  })

  it('posts only the reason on confirm, then hands the refetch to the caller', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse(transaction({ id: 99, kind: 'adjustment', direction: 'out' }), 201)))
    vi.stubGlobal('fetch', fetchMock)

    const { onReversed } = renderReversible([transaction({ id: 36, reversible: true })])
    await openForm()
    await userEvent.type(screen.getByLabelText(text.reverseNoteLabel), '  Salah arah  ')
    await userEvent.click(screen.getByRole('button', { name: text.reverseConfirm }))

    await waitFor(() => expect(onReversed).toHaveBeenCalledTimes(1))
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/transactions/36/reversal',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ note: 'Salah arah' }) }),
    )
    expect(screen.queryByRole('button', { name: text.reverseConfirm })).not.toBeInTheDocument()
  })

  it('closes the form on Jangan jadi, posting nothing', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    renderReversible([transaction({ id: 37, reversible: true })])

    await openForm()
    await userEvent.click(screen.getByRole('button', { name: text.reverseCancel }))

    expect(screen.queryByRole('button', { name: text.reverseConfirm })).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('shows the transaksi wording, not the iuran one, for a repeat reversal', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ error: { code: 'transaction_already_reversed', message: 'x' } }, 409))),
    )

    renderReversible([transaction({ id: 38, reversible: true })])
    await openForm()
    await userEvent.click(screen.getByRole('button', { name: text.reverseConfirm }))

    expect(await screen.findByText('Transaksi ini sudah dibatalkan.')).toBeInTheDocument()
    expect(screen.queryByText(copy.common.errors.transaction_already_reversed)).not.toBeInTheDocument()
  })

  it('reads a refusal the page could not foresee from the shared error map', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(jsonResponse({ error: { code: 'reversal_reconciliation_fix', message: 'x' } }, 409))),
    )

    renderReversible([transaction({ id: 39, reversible: true })])
    await openForm()
    await userEvent.click(screen.getByRole('button', { name: text.reverseConfirm }))

    expect(await screen.findByText(copy.common.errors.reversal_reconciliation_fix)).toBeInTheDocument()
  })
})

describe('TransactionList the pair read as linked (ADR-042)', () => {
  it('marks the original Sudah dibatalkan, in the list and in the detail', async () => {
    renderRows([transaction({ id: 40, reversed: true })])

    expect(screen.getByText('Sudah dibatalkan')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Sudah dibatalkan')).toBeInTheDocument()
  })

  it('marks no row that nothing reverses', () => {
    renderRows([transaction({ id: 41, reversed: false }), transaction({ id: 42 })])

    expect(screen.queryByText('Sudah dibatalkan')).not.toBeInTheDocument()
  })

  it("labels a reversal that names no member with the original's date", () => {
    const { container } = renderRows([
      transaction({ id: 43, kind: 'adjustment', direction: 'out', reverses_transaction_id: 40, reverses_occurred_on: '2026-08-12' }),
    ])

    expect(container.querySelector('.lucide-undo-2')).toBeInTheDocument()
    expect(screen.getByText('Pembatalan \u00b7 transaksi 12 Agustus 2026')).toBeInTheDocument()
  })

  it('leaves a dues and a named-contribution reversal label as it was', () => {
    renderRows([
      transaction({
        id: 44,
        kind: 'adjustment',
        direction: 'out',
        member_id: 5,
        member_name: 'Budi',
        reverses_transaction_id: 10,
        reverses_occurred_on: '2026-08-12',
      }),
      transaction({
        id: 45,
        kind: 'adjustment',
        direction: 'out',
        member_id: 6,
        member_name: 'Ani',
        dues_period: '2026-08',
        reverses_transaction_id: 11,
        reverses_occurred_on: '2026-08-12',
      }),
    ])

    expect(screen.getByText('Pembatalan \u00b7 Budi')).toBeInTheDocument()
    expect(screen.getByText(`Pembatalan \u00b7 ${formatPeriod('2026-08')} \u00b7 Ani`)).toBeInTheDocument()
    expect(screen.queryByText(/transaksi 12 Agustus 2026/)).not.toBeInTheDocument()
  })

  it('hides the pos correction control on a reversed row, though the row would otherwise be eligible', () => {
    renderCorrectableRows([transaction({ id: 46, purpose_id: 1, reversed: true }), transaction({ id: 47, purpose_id: 1 })])

    expect(screen.getAllByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') })).toHaveLength(1)
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

    // Hapus foto lives inside the "more" menu on the photo itself - opening
    // it surfaces the inline confirm bar, not the delete API call.
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
    // Delete-then-upload, in that order (no combined replace route) - see
    // ReceiptDialog.tsx for why that order is the safer half-done.
    expect(calls[0]).toBe('DELETE /api/receipts/42')
    expect(calls[1]).toBe('POST /api/transactions/23/receipts')
  })

  it('opens the full-screen viewer when the photo itself is tapped', async () => {
    vi.stubGlobal('fetch', vi.fn())

    renderWithReceipts([transaction({ id: 25, receipt_ids: [42] })])

    await userEvent.click(screen.getByRole('button', { name: text.viewReceipt }))
    const dialog = await screen.findByRole('dialog')

    await userEvent.click(within(dialog).getByRole('button', { name: text.zoomAria }))

    // Two dialogs are open: ReceiptDialog and the viewer above it. `hidden:
    // true` because Radix marks the first inert (hidden from the accessibility
    // tree) while the second sits on top.
    await waitFor(() => expect(screen.getAllByRole('dialog', { hidden: true })).toHaveLength(2))
  })

  it('renders no receipt control at all where onReceiptsChanged is not passed', () => {
    renderRows([transaction({ id: 22, receipt_ids: [] })])

    expect(screen.queryByRole('button', { name: text.addFromRow })).not.toBeInTheDocument()
  })
})

describe('TransactionList entry detail (#359)', () => {
  const longNote = 'Beli semen 3 sak, pasir 1 pikap, dan upah tukang dua hari untuk perbaikan saluran air di gang belakang pos ronda'

  it('opens the entry in full on a tap of the card, with the whole note and when it was recorded', async () => {
    // An explicit instant: the dialog shows the time in the fund's zone (WIB
    // until one is set, ADR-041), whatever zone the runner is in (CI is UTC).
    renderRows([transaction({ direction: 'out', note: longNote, created_at: new Date('2026-10-01T13:42:00+07:00').getTime() / 1000 })])

    await userEvent.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))

    const dialog = await screen.findByRole('dialog', { name: copy.transactionDetail.heading })
    expect(within(dialog).getByText(longNote)).toBeInTheDocument()
    expect(within(dialog).getByText(copy.transactionDetail.directionOut)).toBeInTheDocument()
    expect(within(dialog).getByText('Tunai')).toBeInTheDocument()
    expect(within(dialog).getByText(copy.transactionDetail.recordedAtLabel)).toBeInTheDocument()
    expect(within(dialog).getByText('1 Oktober 2026 pukul 13.42 WIB')).toBeInTheDocument()
  })

  it('shows the pos the money is under now on a corrected row', async () => {
    renderRows([transaction({ purpose_id: 1, effective_purpose_id: 2 })])

    await userEvent.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Kas Utama')).toBeInTheDocument()
    expect(within(dialog).getByText(copy.purposeCorrection.currentLabel("Jane's wedding"))).toBeInTheDocument()
  })

  it('carries the app-built label in full, e.g. a reconciliation adjustment', async () => {
    renderRows([transaction({ kind: 'adjustment', direction: 'out' })])

    await userEvent.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(copy.rowLabels.adjustment.text('Tunai'))).toBeInTheDocument()
  })

  it('leaves a row control to do its own job, without opening the detail', async () => {
    const { onCorrectPurpose } = renderCorrectableRows([transaction({ direction: 'out' })])

    await userEvent.click(screen.getByRole('button', { name: copy.purposeCorrection.controlAria('Kas Utama') }))

    expect(onCorrectPurpose).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('dialog', { name: copy.transactionDetail.heading })).not.toBeInTheDocument()
  })
})

describe('TransactionList purpose-pair labels (ADR-036, #383)', () => {
  const pair: Partial<Transaction> = {
    kind: 'transfer',
    transfer_id: 9,
    transfer_kind: 'reclass_purpose',
    transfer_from_name: 'Kas Utama',
    transfer_to_name: 'Amplop Duka',
  }

  it('labels an allocation "Pindah pos" with both purposes, and Shuffle, never Tutup amplop', () => {
    const { container } = renderRows([transaction({ ...pair, transfer_reason: 'allocation' })])

    expect(screen.getByText(copy.rowLabels.transferPurposeMove.text('Kas Utama', 'Amplop Duka'))).toBeInTheDocument()
    expect(container.querySelector('.lucide-shuffle')).toBeInTheDocument()
    expect(container.querySelector('.lucide-mail')).not.toBeInTheDocument()
    expect(screen.queryByText(copy.rowLabels.transferPurpose.kind)).not.toBeInTheDocument()
    expect(screen.queryByText(copy.rowLabels.transferPurposeCorrection.kind)).not.toBeInTheDocument()
  })

  it('keeps a roll as Tutup amplop', () => {
    const { container } = renderRows([transaction({ ...pair, transfer_reason: 'roll' })])

    expect(screen.getByText(copy.rowLabels.transferPurpose.kind)).toBeInTheDocument()
    expect(screen.getByText(copy.rowLabels.transferPurpose.text('Kas Utama', 'Amplop Duka'))).toBeInTheDocument()
    expect(container.querySelector('.lucide-mail')).toBeInTheDocument()
    expect(container.querySelector('.lucide-shuffle')).not.toBeInTheDocument()
  })

  it('reads a null reason as a roll: the row predates the column', () => {
    const { container } = renderRows([transaction({ ...pair, transfer_reason: null })])
    expect(screen.getByText(copy.rowLabels.transferPurpose.kind)).toBeInTheDocument()
    expect(container.querySelector('.lucide-mail')).toBeInTheDocument()

    // And a row from an older server, with no field at all, reads the same.
    const { container: older } = renderRows([transaction({ ...pair })])
    expect(older.querySelector('.lucide-mail')).toBeInTheDocument()
  })

  it('keeps a correction as Perbaikan pos', () => {
    const { container } = renderRows([transaction({ ...pair, transfer_corrects_transaction_id: 4, transfer_reason: null })])

    expect(screen.getByText(copy.rowLabels.transferPurposeCorrection.kind)).toBeInTheDocument()
    expect(container.querySelector('.lucide-tags')).toBeInTheDocument()
    expect(container.querySelector('.lucide-shuffle')).not.toBeInTheDocument()
  })
})
