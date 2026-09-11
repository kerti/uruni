import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { copy } from '@/copy/id'
import Reconciliations from '@/screens/History/Reconciliations'

afterEach(() => {
  vi.unstubAllGlobals()
})

const text = copy.reconciliation
const tabText = copy.history.reconciliations

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const accounts = [
  { id: 1, kind: 'cash', name: 'Tunai', inactive_on: null },
  { id: 2, kind: 'bank', name: 'Bank', inactive_on: null },
]

function listRow(id: number, performedAt: number, openDifferenceAmount: number) {
  return {
    id,
    performed_at: performedAt,
    through_transaction_id: null,
    note: null,
    created_at: performedAt,
    open_difference_amount: openDifferenceAmount,
  }
}

function detailFor(id: number, performedAt: number, note: string | null) {
  return {
    id,
    performed_at: performedAt,
    through_transaction_id: null,
    note,
    created_at: performedAt,
    lines: [
      {
        id: 100 + id,
        account_id: 1,
        recorded_amount: 100_000,
        actual_amount: 85_000,
        difference_amount: -15_000,
        resolution: 'left_open',
        adjustment_transaction_id: null,
      },
    ],
  }
}

type Page = { reconciliations: unknown[]; next_cursor: string | null }

/** Stubs fetch: GET /api/accounts always answers the same roster,
 * GET /api/reconciliations answers whatever pageFor returns, and
 * GET /api/reconciliations/{id} answers whatever detailFor returns - same
 * shape Transactions.test.tsx's own stubApi uses. */
function stubApi(pageFor: (url: URL) => Page, detailForId?: (id: number) => unknown) {
  const requests: URL[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/accounts') return jsonResponse(accounts)
      const detailMatch = /^\/api\/reconciliations\/(\d+)$/.exec(url.pathname)
      if (detailMatch && detailForId) {
        return jsonResponse(detailForId(Number(detailMatch[1])))
      }
      if (url.pathname === '/api/reconciliations') {
        requests.push(url)
        return jsonResponse(pageFor(url))
      }
      return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
    }),
  )
  return requests
}

function renderAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route path="/history/reconciliations" element={<Reconciliations />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('Reconciliations tab (Cek kas)', () => {
  it('lists snapshots newest-first with a cocok badge and a selisih badge', async () => {
    stubApi(() => ({
      reconciliations: [listRow(2, 2000, 0), listRow(1, 1000, 15_000)],
      next_cursor: null,
    }))
    renderAt('/history/reconciliations')

    const rows = await screen.findAllByRole('button')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent(text.resolutionOptions.matched)
    expect(rows[1]).toHaveTextContent(text.selisihBadge('Rp 15.000'))
  })

  it('loads the next page from the cursor and drops the button on the last page', async () => {
    const requests = stubApi((url) =>
      url.searchParams.get('cursor') === 'c1'
        ? { reconciliations: [listRow(1, 1000, 0)], next_cursor: null }
        : { reconciliations: [listRow(2, 2000, 0)], next_cursor: 'c1' },
    )
    const user = userEvent.setup()
    renderAt('/history/reconciliations')

    await screen.findAllByRole('button', { name: /Cocok/ })
    await user.click(screen.getByRole('button', { name: tabText.loadMore }))

    expect(await screen.findAllByRole('button', { name: /Cocok/ })).toHaveLength(2)
    expect(screen.queryByRole('button', { name: tabText.loadMore })).not.toBeInTheDocument()
    expect(requests.at(-1)?.searchParams.get('cursor')).toBe('c1')
  })

  it('shows the calm empty state for a fund that has never been reconciled', async () => {
    stubApi(() => ({ reconciliations: [], next_cursor: null }))
    renderAt('/history/reconciliations')

    expect(await screen.findByText(tabText.emptyState)).toBeInTheDocument()
  })

  it('opening a row fetches and shows the detail sheet with differences and note', async () => {
    stubApi(
      () => ({ reconciliations: [listRow(7, 1000, 15_000)], next_cursor: null }),
      (id) => detailFor(id, 1000, 'Hitungan bulan ini'),
    )
    const user = userEvent.setup()
    renderAt('/history/reconciliations')

    await user.click(await screen.findByRole('button', { name: /Selisih/ }))

    expect(await screen.findByText('Tunai')).toBeInTheDocument()
    expect(screen.getByText(`${text.differenceLabel}: -Rp 15.000`, { exact: false })).toBeInTheDocument()
    expect(screen.getByText('Hitungan bulan ini')).toBeInTheDocument()
  })

  it('never renders a control that links to /reconcile', async () => {
    stubApi(() => ({ reconciliations: [listRow(1, 1000, 0)], next_cursor: null }))
    renderAt('/history/reconciliations')

    await screen.findAllByRole('button', { name: /Cocok/ })
    expect(screen.queryAllByRole('link').filter((el) => el.getAttribute('href')?.includes('/reconcile'))).toHaveLength(0)
    expect(screen.queryByText(text.submit)).not.toBeInTheDocument()
  })
})
