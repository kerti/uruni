import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { copy } from '@/copy/id'
import Transactions from '@/screens/History/Transactions'

afterEach(() => {
  vi.unstubAllGlobals()
})

const text = copy.history.transactions

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const balances = {
  fund_total: 0,
  accounts: [{ id: 1, kind: 'cash', name: 'Tunai', balance: 0 }],
  purposes: [{ id: 11, kind: 'general', name: 'Kas Utama', balance: 0 }],
}

function row(id: number, note: string) {
  return {
    id,
    account_id: 1,
    purpose_id: 11,
    direction: 'in',
    amount: 10_000,
    occurred_on: '2026-09-01',
    kind: 'normal',
    member_id: null,
    dues_period: null,
    reimbursement_id: null,
    transfer_id: null,
    reverses_transaction_id: null,
    note,
    created_at: id,
  }
}

type Page = { transactions: unknown[]; next_cursor: string | null }

/** Stubs fetch: balances always answer, GET /api/transactions answers
 * whatever pageFor returns for the request's own URL (throwing from pageFor
 * simulates fetch itself failing). Returns every transactions URL requested,
 * in order, so a test can assert on what reached the server. */
function stubApi(pageFor: (url: URL) => Page) {
  const requests: URL[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/balances') return jsonResponse(balances)
      if (url.pathname === '/api/transactions') {
        requests.push(url)
        return jsonResponse(pageFor(url))
      }
      return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
    }),
  )
  return requests
}

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location">{location.search}</output>
}

function renderAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/history/transactions"
          element={
            <>
              <Transactions />
              <LocationProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('Transactions tab', () => {
  it('loads the next page from the cursor and drops the button on the last page', async () => {
    const requests = stubApi((url) =>
      url.searchParams.get('cursor') === 'c1'
        ? { transactions: [row(1, 'Beli sapu')], next_cursor: null }
        : { transactions: [row(2, 'Galon')], next_cursor: 'c1' },
    )
    const user = userEvent.setup()
    renderAt('/history/transactions')

    expect(await screen.findByText('Galon')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: text.loadMore }))

    expect(await screen.findByText('Beli sapu')).toBeInTheDocument()
    expect(screen.getByText('Galon')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.loadMore })).not.toBeInTheDocument()
    expect(requests.at(-1)?.searchParams.get('cursor')).toBe('c1')
  })

  it('reads ?q= from the URL into the field and sends it to the server', async () => {
    const requests = stubApi(() => ({ transactions: [row(3, 'Iuran Budi')], next_cursor: null }))
    renderAt('/history/transactions?q=Budi')

    expect(await screen.findByText('Iuran Budi')).toBeInTheDocument()
    expect(screen.getByLabelText(text.searchLabel)).toHaveValue('Budi')
    expect(requests[0].searchParams.get('q')).toBe('Budi')
  })

  it('writes a typed search to the URL once, after the pause, and refetches with it', async () => {
    const requests = stubApi((url) =>
      url.searchParams.get('q') === 'galon'
        ? { transactions: [row(4, 'Galon Aqua')], next_cursor: null }
        : { transactions: [row(5, 'Beli sapu')], next_cursor: null },
    )
    const user = userEvent.setup()
    renderAt('/history/transactions')

    expect(await screen.findByText('Beli sapu')).toBeInTheDocument()
    await user.type(screen.getByLabelText(text.searchLabel), 'galon')

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('?q=galon'))
    expect(await screen.findByText('Galon Aqua')).toBeInTheDocument()
    // Debounced: no request for "g", "ga", ... - only the finished word.
    expect(requests.filter((url) => url.searchParams.has('q')).map((url) => url.searchParams.get('q'))).toEqual(['galon'])
  })

  it('says nothing matched when a search comes back empty', async () => {
    stubApi(() => ({ transactions: [], next_cursor: null }))
    renderAt('/history/transactions?q=xyz')

    expect(await screen.findByText(text.noResults('xyz'))).toBeInTheDocument()
  })

  it('shows the connection state, never no-results, when a search cannot reach the server', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    renderAt('/history/transactions?q=xyz')

    expect(await screen.findByText(copy.common.errors.network_error)).toBeInTheDocument()
    expect(screen.queryByText(text.noResults('xyz'))).not.toBeInTheDocument()
    expect(screen.getByLabelText(text.searchLabel)).toHaveValue('xyz')
  })
})
