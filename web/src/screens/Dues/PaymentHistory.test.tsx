import { render as rtlRender, screen, waitFor, within } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/components/toast/ToastProvider'
import PaymentHistory from '@/screens/Dues/PaymentHistory'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { DuesPaymentHistoryRow } from '@/lib/dues'
import type { Transaction } from '@/lib/transactions'

const text = copy.history.dues
const rowText = copy.dues.history

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function payment(id: number, memberName: string, overrides: Partial<DuesPaymentHistoryRow> = {}): DuesPaymentHistoryRow {
  return {
    id,
    is_reversal: false,
    member_id: 1,
    member_name: memberName,
    dues_period: '2026-08',
    amount: 25_000,
    occurred_on: '2026-08-01',
    account_name: 'Tunai',
    note: null,
    reverses_transaction_id: null,
    reversed_by_transaction_id: null,
    reverses_occurred_on: null,
    receipt_ids: [],
    ...overrides,
  }
}

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location">{location.search}</output>
}

/** The list reads and writes ?q=, so every render sits in a router. */
function render(ui: ReactElement, entry = '/history/dues') {
  const wrap = (el: ReactElement) => (
    <MemoryRouter initialEntries={[entry]}>
      <ToastProvider>{el}</ToastProvider>
      <LocationProbe />
    </MemoryRouter>
  )
  const result = rtlRender(wrap(ui))
  return { ...result, rerender: (next: ReactElement) => result.rerender(wrap(next)) }
}

type Page = { dues_payments: DuesPaymentHistoryRow[]; next_cursor: string | null }

/** Stubs fetch: GET /api/dues-payments answers whatever pageFor returns for the
 * request's URL. Returns every requested URL, in order, so a test can assert
 * on what reached the server (as History/Transactions.test.tsx's stub does). */
function stubApi(pageFor: (url: URL) => Page) {
  const requests: URL[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/dues-payments') {
        requests.push(url)
        return jsonResponse(pageFor(url))
      }
      return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
    }),
  )
  return requests
}

describe('PaymentHistory', () => {
  it('shows the camera and count under the amount once a payment has notas, the count in the card name (#768)', async () => {
    stubApi(() => ({ dues_payments: [payment(1, 'Warga Satu', { receipt_ids: [4, 9] }), payment(2, 'Warga Dua')], next_cursor: null }))
    render(<PaymentHistory onOpenStatus={vi.fn()} />)

    const withNota = (await screen.findByText('Warga Satu')).closest('li')!
    expect(within(withNota).getByRole('button')).toHaveAccessibleName(
      copy.transactionDetail.openAria('Warga Satu, Agustus 2026', formatIDR(25_000), '1 Agustus 2026') +
        `, ${copy.transactionDetail.receiptCountAria(2)}`,
    )
    const amount = within(withNota).getByText(formatIDR(25_000))
    expect(amount.parentElement).toHaveTextContent('2')
    const without = screen.getByText('Warga Dua').closest('li')!
    expect(within(without).getByText(formatIDR(25_000)).parentElement?.textContent).toBe(formatIDR(25_000))
  })

  it('renders a page of payments under its own heading', async () => {
    stubApi(() => ({ dues_payments: [payment(1, 'Warga Satu')], next_cursor: null }))
    render(<PaymentHistory onOpenStatus={vi.fn()} />)

    expect(await screen.findByRole('heading', { name: text.heading })).toBeInTheDocument()
    expect(screen.getByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText(formatIDR(25_000))).toBeInTheDocument()
  })

  it('says nothing recorded yet when the fund has no dues payments', async () => {
    stubApi(() => ({ dues_payments: [], next_cursor: null }))
    render(<PaymentHistory onOpenStatus={vi.fn()} />)

    expect(await screen.findByText(text.empty)).toBeInTheDocument()
  })

  it('loads the next page from the cursor and drops the button on the last page', async () => {
    const requests = stubApi((url) =>
      url.searchParams.get('cursor') === 'c1'
        ? { dues_payments: [payment(1, 'Warga Satu')], next_cursor: null }
        : { dues_payments: [payment(2, 'Warga Dua')], next_cursor: 'c1' },
    )
    const user = userEvent.setup()
    render(<PaymentHistory onOpenStatus={vi.fn()} />)

    expect(await screen.findByText('Warga Dua')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: text.loadMore }))

    expect(await screen.findByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText('Warga Dua')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.loadMore })).not.toBeInTheDocument()
    expect(requests.at(-1)?.searchParams.get('cursor')).toBe('c1')
  })

  it('debounces a typed search and sends it to the server', async () => {
    const requests = stubApi((url) =>
      url.searchParams.get('q') === 'budi'
        ? { dues_payments: [payment(1, 'Budi Santoso')], next_cursor: null }
        : { dues_payments: [payment(2, 'Warga Dua')], next_cursor: null },
    )
    const user = userEvent.setup()
    render(<PaymentHistory onOpenStatus={vi.fn()} />)

    expect(await screen.findByText('Warga Dua')).toBeInTheDocument()
    await user.type(screen.getByLabelText(text.searchLabel), 'budi')

    expect(await screen.findByText('Budi Santoso')).toBeInTheDocument()
    // Debounced: no request for "b", "bu", ... - only the finished word.
    expect(requests.filter((url) => url.searchParams.has('q')).map((url) => url.searchParams.get('q'))).toEqual(['budi'])
    expect(screen.getByTestId('location')).toHaveTextContent('?q=budi')
  })

  it('reads ?q= from the URL into the field and sends it to the server', async () => {
    const requests = stubApi(() => ({ dues_payments: [payment(1, 'Budi Santoso')], next_cursor: null }))
    render(<PaymentHistory onOpenStatus={vi.fn()} />, '/history/dues?q=budi')

    expect(await screen.findByText('Budi Santoso')).toBeInTheDocument()
    expect(screen.getByLabelText(text.searchLabel)).toHaveValue('budi')
    expect(requests.map((url) => url.searchParams.get('q'))).toEqual(['budi'])
  })

  it('says nothing matched when a search comes back empty', async () => {
    stubApi(() => ({ dues_payments: [], next_cursor: null }))
    const user = userEvent.setup()
    render(<PaymentHistory onOpenStatus={vi.fn()} />)

    await waitFor(() => expect(screen.getByText(text.empty)).toBeInTheDocument())
    await user.type(screen.getByLabelText(text.searchLabel), 'xyz')

    expect(await screen.findByText(text.noResults('xyz'))).toBeInTheDocument()
  })

  it('shows a reversed payment with its badge and the reversal beside it, linked by date', async () => {
    stubApi(() => ({
      dues_payments: [
        payment(2, 'Warga Satu', {
          is_reversal: true,
          amount: 25_000,
          occurred_on: '2026-08-10',
          reverses_transaction_id: 1,
          reverses_occurred_on: '2026-08-01',
        }),
        payment(1, 'Warga Satu', { reversed_by_transaction_id: 2 }),
      ],
      next_cursor: null,
    }))
    render(<PaymentHistory onOpenStatus={vi.fn()} />)

    await screen.findAllByText('Warga Satu')
    // The reversed payment carries the badge.
    expect(screen.getByText(rowText.reversedBadge)).toBeInTheDocument()
    // The reversal names itself and states which payment it undoes, by that
    // payment's date.
    expect(screen.getByText(new RegExp(rowText.reversalRow))).toBeInTheDocument()
    expect(screen.getByText(text.reversesLabel('1 Agustus 2026'))).toBeInTheDocument()
  })

  it('changing refetchKey reloads the list; nothing about the period drives it, because it takes no period prop', async () => {
    const requests = stubApi(() => ({ dues_payments: [payment(1, 'Warga Satu')], next_cursor: null }))
    const { rerender } = render(<PaymentHistory onOpenStatus={vi.fn()} refetchKey="a" />)

    await screen.findByText('Warga Satu')
    const callsBefore = requests.length

    rerender(<PaymentHistory onOpenStatus={vi.fn()} refetchKey="b" />)

    await waitFor(() => expect(requests.length).toBeGreaterThan(callsBefore))
  })

  // The status matrix is its own screen, reached from the top of this tab
  // rather than stacked above the list.
  it('opens the status matrix from the row at the top of the tab', async () => {
    stubApi(() => ({ dues_payments: [], next_cursor: null }))
    const onOpenStatus = vi.fn()
    const user = userEvent.setup()
    render(<PaymentHistory onOpenStatus={onOpenStatus} />)

    await user.click(screen.getByRole('button', { name: copy.dues.entryLink }))
    expect(onOpenStatus).toHaveBeenCalledTimes(1)
  })

  describe('the card is one tap target (#768)', () => {
    function duesTransaction(id: number, overrides: Partial<Transaction> = {}): Transaction {
      return {
        id,
        account_id: 7,
        purpose_id: 3,
        direction: 'in',
        amount: 25_000,
        occurred_on: '2026-08-01',
        kind: 'dues',
        member_id: 1,
        dues_period: '2026-08',
        reimbursement_id: null,
        transfer_id: null,
        reverses_transaction_id: null,
        note: null,
        created_at: 1,
        reversible: true,
        ...overrides,
      }
    }

    /** The list, the transaction behind it (filtered by member and period, as
     * the server does), the purposes and the reversal write. */
    function stubDetailApi() {
      const calls: { method: string; url: URL }[] = []
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
          const url = new URL(String(input), 'http://localhost')
          const method = (init?.method ?? 'GET').toUpperCase()
          calls.push({ method, url })
          if (url.pathname === '/api/dues-payments') return jsonResponse({ dues_payments: [payment(7, 'Warga Satu')], next_cursor: null })
          if (url.pathname === '/api/transactions' && method === 'GET') {
            return jsonResponse({ transactions: [duesTransaction(7)], next_cursor: null })
          }
          if (url.pathname === '/api/transactions/7/reversal' && method === 'POST') {
            return jsonResponse(duesTransaction(8, { direction: 'out', reverses_transaction_id: 7 }), 201)
          }
          if (url.pathname === '/api/purposes') return jsonResponse([{ id: 3, kind: 'main', name: 'Kas Utama', created_at: 1 }])
          return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
        }),
      )
      return calls
    }

    it('has one button, the whole card, named by its facts, and no button of its own', async () => {
      stubDetailApi()
      render(<PaymentHistory onOpenStatus={vi.fn()} />)

      const card = (await screen.findByText('Warga Satu')).closest('li')!
      const buttons = within(card).getAllByRole('button')
      expect(buttons).toHaveLength(1)
      expect(buttons[0]).toHaveAccessibleName(
        copy.transactionDetail.openAria('Warga Satu, Agustus 2026', formatIDR(25_000), '1 Agustus 2026'),
      )
      expect(card).toHaveClass('rounded-lg', 'px-4', 'py-3')
    })

    it('opens the entry detail at ?entry=<id>, and Batalkan hands off to the reversal dialog, worded as on Transaksi, and posts', async () => {
      const calls = stubDetailApi()
      const user = userEvent.setup()
      render(<PaymentHistory onOpenStatus={vi.fn()} />)

      await user.click(await screen.findByRole('button', { name: /^Lihat rincian/ }))
      const detail = await screen.findByRole('dialog', { name: copy.transactionDetail.heading })
      expect(screen.getByTestId('location')).toHaveTextContent('?entry=7')
      // A dues row is not eligible for Perbaiki pos.
      expect(within(detail).queryByRole('button', { name: copy.purposeCorrection.heading })).not.toBeInTheDocument()
      expect(calls.some((c) => c.url.pathname === '/api/transactions' && c.url.searchParams.get('member_id') === '1')).toBe(true)
      expect(calls.some((c) => c.url.pathname === '/api/transactions' && c.url.searchParams.get('dues_period') === '2026-08')).toBe(true)

      await user.click(within(detail).getByRole('button', { name: copy.history.transactions.reverse }))

      // The detail is gone before the reversal dialog opens: never two at once.
      const dialog = await screen.findByRole('dialog', { name: copy.history.transactions.reverse })
      expect(screen.getAllByRole('dialog')).toHaveLength(1)
      expect(screen.getByTestId('location').textContent).toContain('edit=reverse')
      expect(screen.getByTestId('location').textContent).not.toContain('entry=')

      await user.click(within(dialog).getByRole('button', { name: copy.history.transactions.reverseConfirm }))

      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
      expect(screen.getByText(rowText.success)).toBeInTheDocument()
      expect(calls.some((c) => c.method === 'POST' && c.url.pathname === '/api/transactions/7/reversal')).toBe(true)
    })

    it('strips an ?entry= naming a payment this list does not hold', async () => {
      stubDetailApi()
      render(<PaymentHistory onOpenStatus={vi.fn()} />, '/history/dues?entry=99')

      await screen.findByText('Warga Satu')
      await waitFor(() => expect(screen.getByTestId('location').textContent).not.toContain('entry='))
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
  })
})
