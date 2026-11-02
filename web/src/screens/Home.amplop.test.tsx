import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ToastProvider } from '@/components/toast/ToastProvider'
import Home from '@/screens/Home'
import { copy } from '@/copy/id'
import type { Balances } from '@/lib/balances'
import type { IncidentalListItem, ParticipationProgress } from '@/lib/incidentals'

// ADR-047: Beranda's Saldo per pos is where an envelope is opened, where the
// open ones show who has given, and where the closed ones are found.

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const balances = {
  fund_total: 1_000_000,
  owed_to_members: 0,
  month: { in: 0, out: 0 },
  accounts: [{ id: 1, kind: 'cash', name: 'Tunai', balance: 1_000_000 }],
  purposes: [
    { id: 11, kind: 'main', name: 'Kas Utama', balance: 700_000 },
    { id: 13, kind: 'incidental', name: 'Halal bihalal RT', balance: 300_000 },
    { id: 14, kind: 'incidental', name: 'Takziah', balance: 0 },
    { id: 15, kind: 'incidental', name: '17 Agustus', balance: 0 },
  ],
} satisfies Balances

function envelope(
  purposeId: number,
  occasion: string,
  closedOn: string | null = null,
  participation: ParticipationProgress | null = null,
): IncidentalListItem {
  return {
    purpose_id: purposeId,
    occasion,
    target_amount: null,
    opened_on: '2026-09-01',
    closed_on: closedOn,
    created_at: purposeId,
    minimum_per_member: null,
    participation,
  }
}

/** GET /api/incidentals answers every envelope, open and closed; POST is
 * recorded so a test can assert what was sent. */
function stub(incidentals: IncidentalListItem[]) {
  const posts: unknown[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/balances')) return Promise.resolve(jsonResponse(balances))
    if (url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
    if (url.includes('/api/incidentals')) {
      if (method === 'GET') return Promise.resolve(jsonResponse(incidentals))
      posts.push(init?.body ? JSON.parse(String(init.body)) : undefined)
      return Promise.resolve(jsonResponse(envelope(9, 'Kerja bakti'), 201))
    }
    if (url.includes('/api/reconciliations/open-lines')) return Promise.resolve(jsonResponse([]))
    if (url.includes('/api/reconciliations/latest')) {
      return Promise.resolve(jsonResponse({ error: { code: 'not_found', message: 'none' } }, 404))
    }
    if (url.includes('/api/transactions')) return Promise.resolve(jsonResponse({ transactions: [], next_cursor: null }))
    return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
  })
  return { fetchMock, posts }
}

function LocationProbe() {
  const location = useLocation()
  return (
    <output data-testid="location-probe">
      {location.pathname}
      {location.search}
    </output>
  )
}

function renderHome(entry = '/') {
  const callbacks = { onOpenIncidental: vi.fn(), onIncidentalOpened: vi.fn(), onViewClosedIncidentals: vi.fn() }
  render(
    <MemoryRouter initialEntries={[entry]}>
      <ToastProvider>
        <Routes>
          <Route
            path="/"
            element={
              <>
                <Home
                  refetchKey="1"
                  onReconcile={vi.fn()}
                  onViewTransactionsFor={vi.fn()}
                  onViewTransactionsForAccount={vi.fn()}
                  onViewHistory={vi.fn()}
                  onViewOwed={vi.fn()}
                  {...callbacks}
                />
                <LocationProbe />
              </>
            }
          />
        </Routes>
      </ToastProvider>
    </MemoryRouter>,
  )
  return callbacks
}

function currentLocation() {
  return screen.getByTestId('location-probe').textContent
}

function breakdown() {
  return screen.getByText(copy.home.purposeBreakdownHeading).closest('section') as HTMLElement
}

describe('Beranda amplop (ADR-047)', () => {
  it('lists open envelopes with who has given, and gathers closed ones behind a row with their count', async () => {
    const { fetchMock } = stub([
      envelope(13, 'Halal bihalal RT', null, { given: 9, expected: 10 }),
      envelope(14, 'Takziah'),
      envelope(15, '17 Agustus', '2026-08-20'),
    ])
    vi.stubGlobal('fetch', fetchMock)
    const { onViewClosedIncidentals, onOpenIncidental } = renderHome()

    const row = await screen.findByRole('button', { name: /^Halal bihalal RT/ })
    expect(within(row).getByText(copy.incidentals.participation.progress(9, 10))).toBeInTheDocument()
    // Nobody expected: no line at all.
    const quiet = screen.getByRole('button', { name: /^Takziah/ })
    expect(within(quiet).queryByText(/sudah menyumbang/)).not.toBeInTheDocument()
    // Closed: off the breakdown, counted on the closed row.
    expect(within(breakdown()).queryByText('17 Agustus')).not.toBeInTheDocument()
    const closedRow = within(breakdown()).getByRole('button', { name: new RegExp(copy.incidentals.closedRow) })
    expect(within(closedRow).getByText('1')).toBeInTheDocument()

    await userEvent.click(closedRow)
    expect(onViewClosedIncidentals).toHaveBeenCalledTimes(1)
    await userEvent.click(row)
    expect(onOpenIncidental).toHaveBeenCalledWith(13)
  })

  it('shows no closed row while nothing has been closed, and Buka amplop regardless', async () => {
    const { fetchMock } = stub([])
    vi.stubGlobal('fetch', fetchMock)
    renderHome()

    await screen.findByText(copy.home.purposeBreakdownHeading)
    expect(within(breakdown()).queryByRole('button', { name: new RegExp(copy.incidentals.closedRow) })).not.toBeInTheDocument()
    expect(within(breakdown()).getByRole('button', { name: copy.incidentals.open.heading })).toBeInTheDocument()
  })

  it('opens the dialog with ?edit=incidental:new, saying what an amplop is', async () => {
    const { fetchMock } = stub([])
    vi.stubGlobal('fetch', fetchMock)
    renderHome()

    await userEvent.click(await screen.findByRole('button', { name: copy.incidentals.open.heading }))

    expect(currentLocation()).toBe('/?edit=incidental%3Anew')
    const dialog = screen.getByRole('dialog', { name: copy.incidentals.open.heading })
    expect(dialog).toHaveAccessibleDescription(copy.incidentals.open.description)
  })

  it('posts the new envelope and hands its id to the caller', async () => {
    const { fetchMock, posts } = stub([])
    vi.stubGlobal('fetch', fetchMock)
    const { onIncidentalOpened } = renderHome('/?edit=incidental:new')

    const dialog = await screen.findByRole('dialog', { name: copy.incidentals.open.heading })
    await userEvent.type(within(dialog).getByLabelText(copy.incidentals.open.occasionLabel), 'Kerja bakti')
    await userEvent.click(within(dialog).getByRole('button', { name: copy.incidentals.open.submit }))

    await waitFor(() => expect(onIncidentalOpened).toHaveBeenCalledWith(9))
    expect(posts[0]).toMatchObject({ occasion: 'Kerja bakti', target_amount: null, minimum_per_member: null, recipient_member_ids: [] })
  })

  it('strips an incidental:<id> value once loaded, without flashing a dialog', async () => {
    const { fetchMock } = stub([])
    vi.stubGlobal('fetch', fetchMock)
    renderHome('/?edit=incidental:1')

    await screen.findByText(copy.home.purposeBreakdownHeading)
    await waitFor(() => expect(currentLocation()).toBe('/'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
