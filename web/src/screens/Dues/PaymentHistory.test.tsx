import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import PaymentHistory from '@/screens/Dues/PaymentHistory'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'

const text = copy.history.dues
const rowText = copy.dues.history

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function money(amount: number): string {
  return formatIDR(amount).replace(/\u00a0/g, ' ')
}

function payment(id: number, memberName: string, overrides: Partial<Record<string, unknown>> = {}) {
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
    ...overrides,
  }
}

type Page = { dues_payments: unknown[]; next_cursor: string | null }

/** Stubs fetch: GET /api/dues-payments answers whatever pageFor returns for
 * the request's own URL. Returns every requested URL, in order, so a test
 * can assert on what reached the server (the same idiom
 * History/Transactions.test.tsx uses for its own stub). */
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
  it('renders a page of payments under its own heading', async () => {
    stubApi(() => ({ dues_payments: [payment(1, 'Warga Satu')], next_cursor: null }))
    render(<PaymentHistory />)

    expect(await screen.findByRole('heading', { name: text.heading })).toBeInTheDocument()
    expect(screen.getByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText(money(25_000))).toBeInTheDocument()
  })

  it('says nothing recorded yet when the fund has no dues payments', async () => {
    stubApi(() => ({ dues_payments: [], next_cursor: null }))
    render(<PaymentHistory />)

    expect(await screen.findByText(text.empty)).toBeInTheDocument()
  })

  it('loads the next page from the cursor and drops the button on the last page', async () => {
    const requests = stubApi((url) =>
      url.searchParams.get('cursor') === 'c1'
        ? { dues_payments: [payment(1, 'Warga Satu')], next_cursor: null }
        : { dues_payments: [payment(2, 'Warga Dua')], next_cursor: 'c1' },
    )
    const user = userEvent.setup()
    render(<PaymentHistory />)

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
    render(<PaymentHistory />)

    expect(await screen.findByText('Warga Dua')).toBeInTheDocument()
    await user.type(screen.getByLabelText(text.searchLabel), 'budi')

    expect(await screen.findByText('Budi Santoso')).toBeInTheDocument()
    // Debounced: no request for "b", "bu", ... - only the finished word.
    expect(requests.filter((url) => url.searchParams.has('q')).map((url) => url.searchParams.get('q'))).toEqual(['budi'])
  })

  it('says nothing matched when a search comes back empty', async () => {
    stubApi(() => ({ dues_payments: [], next_cursor: null }))
    const user = userEvent.setup()
    render(<PaymentHistory />)

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
    render(<PaymentHistory />)

    await screen.findAllByText('Warga Satu')
    // The reversed payment carries the badge.
    expect(screen.getByText(rowText.reversedBadge)).toBeInTheDocument()
    // The reversal names itself and states which payment it undoes, by
    // that payment's own date.
    expect(screen.getByText(new RegExp(rowText.reversalRow))).toBeInTheDocument()
    expect(screen.getByText(text.reversesLabel('1 Agustus 2026'))).toBeInTheDocument()
  })

  it('changing refetchKey reloads the list; nothing about the period drives it, because it takes no period prop', async () => {
    const requests = stubApi(() => ({ dues_payments: [payment(1, 'Warga Satu')], next_cursor: null }))
    const { rerender } = render(<PaymentHistory refetchKey="a" />)

    await screen.findByText('Warga Satu')
    const callsBefore = requests.length

    rerender(<PaymentHistory refetchKey="b" />)

    await waitFor(() => expect(requests.length).toBeGreaterThan(callsBefore))
  })

  // A reversal posted from the matrix's own panel stays on this screen, so
  // refetchKey never changes - Status.tsx bumps reversalCount instead.
  it('changing reversalCount reloads the list after a reversal posted on the matrix', async () => {
    const requests = stubApi(() => ({ dues_payments: [payment(1, 'Warga Satu')], next_cursor: null }))
    const { rerender } = render(<PaymentHistory refetchKey="a" reversalCount={0} />)

    await screen.findByText('Warga Satu')
    const callsBefore = requests.length

    rerender(<PaymentHistory refetchKey="a" reversalCount={1} />)

    await waitFor(() => expect(requests.length).toBeGreaterThan(callsBefore))
  })
})
