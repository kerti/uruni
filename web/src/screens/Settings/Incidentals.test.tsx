import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import SettingsIncidentals, { ClosedIncidentals } from '@/screens/Settings/Incidentals'
import { copy } from '@/copy/id'

const text = copy.settings.incidentals

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function envelope(purposeId: number, occasion: string, closedOn: string | null = null) {
  return { purpose_id: purposeId, occasion, target_amount: null, opened_on: '2026-09-01', closed_on: closedOn, created_at: purposeId }
}

/** One stub for the whole section: GET /api/incidentals?open=false answers
 * the current list (open and closed together), and POST /api/incidentals is
 * recorded so a test can assert what was actually sent. `members` answers
 * the recipients picker's own roster (ADR-034, #333) - empty by default,
 * since most of this suite is not about that field. */
function stubIncidentals(initial: ReturnType<typeof envelope>[], writeResponse?: () => Response, members: unknown[] = []) {
  const calls: { method: string; url: string; body: unknown }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/members')) return Promise.resolve(jsonResponse({ members, next_cursor: null }))
    if (url.includes('/api/incidentals')) {
      if (method === 'GET') return Promise.resolve(jsonResponse(initial))
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(writeResponse ? writeResponse() : jsonResponse(envelope(9, 'Baru')))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

function member(overrides: Partial<{ id: number; name: string }> = {}) {
  return {
    id: 1,
    name: 'Budi',
    tier_id: null,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
    ...overrides,
  }
}

/** Exposes the router's current search string and pathname, the same way
 * Locations.test.tsx's own probe does - this screen's dialog and its cards'
 * navigation are both asserted against the URL. */
function LocationProbe() {
  const location = useLocation()
  return (
    <output data-testid="location-probe">
      {location.pathname}
      {location.search}
    </output>
  )
}

function renderAt(entry = '/settings') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/settings"
          element={
            <>
              <SettingsIncidentals />
              <LocationProbe />
            </>
          }
        />
        {/* A card navigates here (App.tsx's real route) - present only so
            the navigation has somewhere to land; this suite does not assert
            anything about what renders past it. */}
        <Route path="/incidentals" element={<LocationProbe />} />
        <Route path="/incidentals/closed" element={<LocationProbe />} />
      </Routes>
    </MemoryRouter>,
  )
}

function currentLocation() {
  return screen.getByTestId('location-probe').textContent
}

describe('Settings incidentals', () => {
  // #319: closed envelopes pile up over the years, so Pengaturan lists the
  // open ones and gathers the closed behind one row that says how many.
  it('lists open envelopes, and gathers closed ones behind a row with their count', async () => {
    const { fetchMock } = stubIncidentals([
      envelope(1, 'Halal bihalal RT'),
      envelope(2, '17 Agustus', '2026-08-20'),
      envelope(3, 'Kerja bakti', '2026-07-05'),
    ])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByRole('button', { name: text.cardAria('Halal bihalal RT') })).toBeInTheDocument()
    expect(screen.getByText(copy.incidentals.status.open)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.cardAria('17 Agustus') })).not.toBeInTheDocument()

    const closedRow = screen.getByRole('button', { name: new RegExp(text.closedRow) })
    expect(within(closedRow).getByText('2')).toBeInTheDocument()
    await userEvent.click(closedRow)
    expect(currentLocation()).toBe('/incidentals/closed')
  })

  it('shows no closed row while every envelope is still open', async () => {
    const { fetchMock } = stubIncidentals([envelope(1, 'Halal bihalal RT')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByRole('button', { name: text.cardAria('Halal bihalal RT') })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: new RegExp(text.closedRow) })).not.toBeInTheDocument()
  })

  it('shows the empty state with no envelopes yet', async () => {
    const { fetchMock } = stubIncidentals([])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByText(text.empty)).toBeInTheDocument()
  })

  it('opens the dialog from the add button, with ?edit=incidental:new', async () => {
    const { fetchMock } = stubIncidentals([])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByText(text.empty)

    await userEvent.click(screen.getByRole('button', { name: text.add }))

    expect(currentLocation()).toBe('/settings?edit=incidental%3Anew')
    expect(screen.getByRole('dialog', { name: copy.incidentals.open.heading })).toBeInTheDocument()
  })

  it('opens an envelope, posting occasion/target/date, then closes the dialog and reloads the list', async () => {
    const { fetchMock, calls } = stubIncidentals([], () => jsonResponse(envelope(3, 'Kerja bakti'), 201))
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByText(text.empty)

    await userEvent.click(screen.getByRole('button', { name: text.add }))
    const dialog = screen.getByRole('dialog', { name: copy.incidentals.open.heading })
    await userEvent.type(within(dialog).getByLabelText(copy.incidentals.open.occasionLabel), 'Kerja bakti')
    await userEvent.click(within(dialog).getByRole('button', { name: copy.incidentals.open.submit }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', body: { occasion: 'Kerja bakti', target_amount: null } })
    // A successful open closes the dialog and drops the param.
    await waitFor(() => expect(currentLocation()).toBe('/settings'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  // ADR-034 (#211): the open dialog also carries the envelope's minimum and
  // its recipients - both optional, and posted only what she actually fills.
  it('opens an envelope with a minimum and a recipient, both optional fields', async () => {
    const { fetchMock, calls } = stubIncidentals([], () => jsonResponse(envelope(3, 'Kerja bakti'), 201), [member({ id: 7, name: 'Budi' })])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByText(text.empty)

    await userEvent.click(screen.getByRole('button', { name: text.add }))
    const dialog = screen.getByRole('dialog', { name: copy.incidentals.open.heading })
    await userEvent.type(within(dialog).getByLabelText(copy.incidentals.open.occasionLabel), 'Kerja bakti')
    await userEvent.type(within(dialog).getByLabelText(copy.incidentals.open.minimumLabel), '25000')
    await userEvent.click(await within(dialog).findByRole('checkbox', { name: 'Budi' }))
    await userEvent.click(within(dialog).getByRole('button', { name: copy.incidentals.open.submit }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({
      method: 'POST',
      body: { occasion: 'Kerja bakti', minimum_per_member: 25_000, recipient_member_ids: [7] },
    })
  })

  it('opens an envelope with no minimum and no recipients when neither is touched', async () => {
    const { fetchMock, calls } = stubIncidentals([], () => jsonResponse(envelope(3, 'Kerja bakti'), 201), [member({ id: 7, name: 'Budi' })])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByText(text.empty)

    await userEvent.click(screen.getByRole('button', { name: text.add }))
    const dialog = screen.getByRole('dialog', { name: copy.incidentals.open.heading })
    await userEvent.type(within(dialog).getByLabelText(copy.incidentals.open.occasionLabel), 'Kerja bakti')
    await userEvent.click(within(dialog).getByRole('button', { name: copy.incidentals.open.submit }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ body: { minimum_per_member: null, recipient_member_ids: [] } })
  })

  it("a card navigates to the envelope's detail route", async () => {
    const { fetchMock } = stubIncidentals([envelope(1, 'Halal bihalal RT')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await userEvent.click(await screen.findByRole('button', { name: text.cardAria('Halal bihalal RT') }))

    expect(currentLocation()).toBe('/incidentals?purpose=1')
  })

  it('strips a malformed ?edit= value once the list has loaded, without flashing a dialog', async () => {
    const { fetchMock } = stubIncidentals([envelope(1, 'Halal bihalal RT')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=incidental:1')
    await screen.findByRole('list')

    await waitFor(() => expect(currentLocation()).toBe('/settings'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})

// #319: the screen behind Pengaturan's closed row - closed envelopes only,
// most recently closed first, each opening its detail.
describe('Closed incidentals', () => {
  it('lists only closed envelopes, most recently closed first, and opens one', async () => {
    const { fetchMock } = stubIncidentals([
      envelope(1, 'Halal bihalal RT'),
      envelope(2, 'Kerja bakti', '2026-07-05'),
      envelope(3, '17 Agustus', '2026-08-20'),
    ])
    vi.stubGlobal('fetch', fetchMock)
    const onOpen = vi.fn()
    render(<ClosedIncidentals onBack={vi.fn()} onOpen={onOpen} />)

    const cards = await screen.findAllByRole('button', { name: /^Lihat / })
    expect(cards.map((card) => card.getAttribute('aria-label'))).toEqual([text.cardAria('17 Agustus'), text.cardAria('Kerja bakti')])
    await userEvent.click(cards[0])
    expect(onOpen).toHaveBeenCalledWith(3)
  })

  it('says so when nothing has been closed yet', async () => {
    const { fetchMock } = stubIncidentals([envelope(1, 'Halal bihalal RT')])
    vi.stubGlobal('fetch', fetchMock)
    render(<ClosedIncidentals onBack={vi.fn()} onOpen={vi.fn()} />)

    expect(await screen.findByText(text.closedEmpty)).toBeInTheDocument()
  })
})
