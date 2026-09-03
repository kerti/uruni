import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Home from '@/screens/Home'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** formatIDR's output for use as a getByText/findByText matcher: testing-
 * library normalizes the DOM text it searches (collapsing whitespace,
 * including the U+00A0 non-breaking space Intl.NumberFormat inserts after
 * "Rp") but never normalizes a string matcher itself, so a raw formatIDR()
 * string with its NBSP intact never equals the normalized DOM text it is
 * compared against. */
function money(amount: number): string {
  return formatIDR(amount).replace(/ /g, ' ')
}

const balances = {
  fund_total: 1_450_000,
  accounts: [
    { id: 1, kind: 'cash', name: 'Tunai', balance: 950_000 },
    { id: 2, kind: 'bank', name: 'Bank lama (nonaktif)', balance: 500_000 },
  ],
  purposes: [
    { id: 11, kind: 'general', name: 'Kas Utama', balance: 1_450_000 },
    { id: 12, kind: 'pass_through', name: 'Kas Bidang', balance: 0 },
  ],
}

const transactions = [
  {
    id: 1,
    account_id: 1,
    purpose_id: 11,
    direction: 'out',
    amount: 50_000,
    occurred_on: '2026-09-01',
    kind: 'normal',
    member_id: null,
    dues_period: null,
    reimbursement_id: null,
    transfer_id: null,
    reverses_transaction_id: null,
    note: 'Beli galon',
    created_at: 1,
  },
  {
    id: 2,
    account_id: 1,
    purpose_id: 11,
    direction: 'in',
    amount: 200_000,
    occurred_on: '2026-09-02',
    kind: 'normal',
    member_id: null,
    dues_period: null,
    reimbursement_id: null,
    transfer_id: null,
    reverses_transaction_id: null,
    note: null,
    created_at: 2,
  },
]

const latestReconciliation = { id: 1, performed_at: 1_756_000_000, through_transaction_id: 5, note: null, created_at: 1_756_000_000 }

/** Routes a stubbed fetch by method + path substring, same idiom as
 * RecordTransaction.test.tsx's own routedFetch. */
function routedFetch(handlers: { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((h) => h.match(method, url))
    if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
    return handler.handle()
  })
}

function stubHome({ openLines = [] as unknown[], latest = 'ok' as 'ok' | 'not_found' } = {}) {
  return routedFetch([
    { match: (m, u) => m === 'GET' && u.includes('/api/balances'), handle: () => Promise.resolve(jsonResponse(balances)) },
    { match: (m, u) => m === 'GET' && u.includes('/api/reconciliations/open-lines'), handle: () => Promise.resolve(jsonResponse(openLines)) },
    {
      match: (m, u) => m === 'GET' && u.includes('/api/reconciliations/latest'),
      handle: () =>
        Promise.resolve(
          latest === 'ok' ? jsonResponse(latestReconciliation) : jsonResponse({ error: { code: 'not_found', message: 'no reconciliation' } }, 404),
        ),
    },
    { match: (m, u) => m === 'GET' && u.includes('/api/transactions'), handle: () => Promise.resolve(jsonResponse(transactions)) },
  ])
}

describe('Home', () => {
  it('renders the balance hero from fund_total, formatted with formatIDR', async () => {
    vi.stubGlobal('fetch', stubHome())
    render(<Home refetchKey="1" />)

    expect(await screen.findByText(money(1_450_000))).toBeInTheDocument()
  })

  it('renders every account balances.accounts returns, in the order returned', async () => {
    vi.stubGlobal('fetch', stubHome())
    render(<Home refetchKey="1" />)

    await screen.findByText('Tunai')
    expect(screen.getByText(money(950_000))).toBeInTheDocument()
    expect(screen.getByText('Bank lama (nonaktif)')).toBeInTheDocument()
    expect(screen.getByText(money(500_000))).toBeInTheDocument()
  })

  it('shows "last checked" from a reconciliation that has already been taken', async () => {
    vi.stubGlobal('fetch', stubHome({ latest: 'ok' }))
    render(<Home refetchKey="1" />)

    expect(await screen.findByText(copy.reconciliation.matched)).toBeInTheDocument()
    expect(screen.queryByText(copy.reconciliation.neverChecked)).not.toBeInTheDocument()
  })

  // GET /api/reconciliations/latest answering 404 not_found is a normal
  // first-run state (a fund that has never been reconciled), not an error -
  // it must render the first-run copy, never ErrorState.
  it('treats a 404 not_found from /api/reconciliations/latest as first-run, not an error', async () => {
    vi.stubGlobal('fetch', stubHome({ latest: 'not_found' }))
    render(<Home refetchKey="1" />)

    expect(await screen.findByText(copy.reconciliation.neverChecked)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    // Never counted is never green: Uruni has not compared anything yet, so
    // it must not claim the records and the cash agree.
    expect(screen.queryByText(copy.reconciliation.matched)).not.toBeInTheDocument()
  })

  it('renders the most recent transactions, newest first', async () => {
    vi.stubGlobal('fetch', stubHome())
    render(<Home refetchKey="1" />)

    const items = await screen.findAllByText(/Rp/)
    // The recent-activity list reverses the oldest-first API order - the
    // 200_000 "in" entry (occurred 2026-09-02) is the newest, so it appears
    // before the 50_000 "out" entry (occurred 2026-09-01) in document order.
    const amounts = items.map((el) => el.textContent)
    const inIndex = amounts.findIndex((t) => t === formatIDR(200_000))
    const outIndex = amounts.findIndex((t) => t === formatIDR(50_000))
    expect(inIndex).toBeGreaterThanOrEqual(0)
    expect(outIndex).toBeGreaterThan(inIndex)
  })

  it('labels each recent entry with its purpose and note, from the balances response', async () => {
    vi.stubGlobal('fetch', stubHome())
    render(<Home refetchKey="1" />)

    // purpose_id 11 is 'Kas Utama' in balances.purposes - both entries carry
    // it, so both rows are labelled without a fifth request.
    expect(await screen.findAllByText('Kas Utama')).toHaveLength(2)
    // The optional note (PRD 6) shows when there is one, and nothing stands
    // in for it when there isn't.
    expect(screen.getByText('Beli galon')).toBeInTheDocument()
  })

  it('refreshes when the app comes back to the foreground, without blanking the screen', async () => {
    const fetchMock = stubHome()
    vi.stubGlobal('fetch', fetchMock)
    render(<Home refetchKey="1" />)
    await screen.findByText(money(1_450_000))

    const callsBefore = fetchMock.mock.calls.length
    document.dispatchEvent(new Event('visibilitychange'))

    // The refresh is silent: the balance stays on screen throughout rather
    // than dropping to the loading state on every app switch (iOS may or
    // may not have evicted the app - a warm resume shows stale numbers
    // unless something re-reads them).
    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore))
    expect(screen.getByText(money(1_450_000))).toBeInTheDocument()
    expect(screen.queryByText(copy.common.loading)).not.toBeInTheDocument()
  })

  it('refetches when refetchKey changes, so a fresh record shows up without a manual refresh', async () => {
    const fetchMock = stubHome()
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = render(<Home refetchKey="1" />)
    await screen.findByText(money(1_450_000))

    const callsBefore = fetchMock.mock.calls.length
    rerender(<Home refetchKey="2" />)

    await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore))
  })
})
