import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import SettingsIncidentals from '@/screens/Settings/Incidentals'
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
 * recorded so a test can assert what was actually sent. */
function stubIncidentals(initial: ReturnType<typeof envelope>[], writeResponse?: () => Response) {
  const calls: { method: string; url: string; body: unknown }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/incidentals')) {
      if (method === 'GET') return Promise.resolve(jsonResponse(initial))
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(writeResponse ? writeResponse() : jsonResponse(envelope(9, 'Baru')))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
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
      </Routes>
    </MemoryRouter>,
  )
}

function currentLocation() {
  return screen.getByTestId('location-probe').textContent
}

describe('Settings incidentals', () => {
  it('lists every envelope, open and closed alike, with its status', async () => {
    const { fetchMock } = stubIncidentals([envelope(1, 'Halal bihalal RT'), envelope(2, '17 Agustus', '2026-08-20')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByRole('list')).toBeInTheDocument()
    const openCard = screen.getByRole('button', { name: text.cardAria('Halal bihalal RT') })
    expect(openCard).toBeInTheDocument()
    expect(screen.getByText(copy.incidentals.status.open)).toBeInTheDocument()

    const closedCard = screen.getByRole('button', { name: text.cardAria('17 Agustus') })
    expect(closedCard).toBeInTheDocument()
    expect(screen.getByText(copy.incidentals.status.closed)).toBeInTheDocument()
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

  it('a card navigates to the envelope\'s detail route', async () => {
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
