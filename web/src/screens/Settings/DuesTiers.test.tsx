import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DuesTiers from '@/screens/Settings/DuesTiers'
import { copy } from '@/copy/id'

const text = copy.settings.tiers

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function tier(id: number, name: string) {
  return { id, name, created_at: 1 }
}

function rate(id: number, tierId: number, amount: number, effectiveFrom: string) {
  return { id, tier_id: tierId, amount, effective_from: effectiveFrom, created_at: 1 }
}

function stubTiers(tiers: ReturnType<typeof tier>[], rates: ReturnType<typeof rate>[], writeResponse?: () => Response) {
  const calls: { method: string; url: string; body: Record<string, unknown> | undefined }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    const write = () => {
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(writeResponse ? writeResponse() : jsonResponse(rate(9, 1, 50_000, '2026-09')))
    }
    if (url.includes('/rates')) return method === 'GET' ? Promise.resolve(jsonResponse(rates)) : write()
    if (url.includes('/api/dues-rates')) return write()
    if (url.includes('/api/dues-tiers')) return method === 'GET' ? Promise.resolve(jsonResponse(tiers)) : write()
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

/** This section's dialogs ARE the URL (ADR-032), so `?edit=` is state worth
 * asserting on directly. */
function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

function renderAt(entry = '/settings') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/settings"
          element={
            <>
              <DuesTiers />
              <LocationProbe />
            </>
          }
        />
        {/* The golongan's own screen (#285) stands in for App.tsx's route
            here: this file's subject is the section, and what it has to
            prove is that the card leads there with the right id. */}
        <Route
          path="/dues-tiers"
          element={
            <output data-testid="tier-screen">
              <LocationProbe />
            </output>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

function currentSearch() {
  return screen.getByTestId('location-search').textContent
}

describe('Settings dues tiers', () => {
  // The card answers the one question she opens this section for, without
  // opening anything: what does this golongan cost now.
  it('shows each tier with the rate in force this month', async () => {
    const thisYear = new Date().getFullYear()
    const { fetchMock } = stubTiers(
      [tier(1, 'Pelaksana')],
      [rate(1, 1, 50_000, `${thisYear - 1}-01`), rate(2, 1, 60_000, `${thisYear + 1}-01`)],
    )
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByText('Pelaksana')).toBeInTheDocument()
    // The rate already in force, never the one that starts next year - the
    // same "latest row at or before this month" the server reads.
    await waitFor(() => expect(screen.getByText(/50\.000/)).toBeInTheDocument())
    expect(screen.queryByText(/60\.000/)).not.toBeInTheDocument()
  })

  it('renders a tier with no rates plainly, not as an error', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByText('Pelaksana')).toBeInTheDocument()
    // A tier whose price is not decided yet is a legal state (PRD section 6).
    expect(await screen.findByText(text.noRates)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('has no inline form on the section itself', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByText('Pelaksana')

    expect(screen.queryByLabelText(text.nameLabel)).not.toBeInTheDocument()
    expect(screen.queryByRole('form', { name: text.addRate })).not.toBeInTheDocument()
  })

  it('adds a tier from its own dialog', async () => {
    const { fetchMock, calls } = stubTiers([], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByText(text.empty)

    await userEvent.click(screen.getByRole('button', { name: text.add }))
    expect(currentSearch()).toBe('?edit=tier%3Anew')

    const dialog = within(await screen.findByRole('dialog'))
    await userEvent.type(dialog.getByLabelText(text.nameLabel), 'Fungsional')
    await userEvent.click(dialog.getByRole('button', { name: text.add }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', url: expect.stringContaining('/api/dues-tiers'), body: { name: 'Fungsional' } })
  })










  it('renders a duplicate tier name as the 409 it is', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [], () =>
      jsonResponse({ error: { code: 'unique_violation', message: 'conflict' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:new')

    const dialog = within(await screen.findByRole('dialog'))
    await userEvent.type(dialog.getByLabelText(text.nameLabel), 'Pelaksana')
    await userEvent.click(dialog.getByRole('button', { name: text.add }))

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.unique_violation)
  })


  // #285: the card is the way into the golongan's own screen now, not into
  // a dialog holding its whole life.
  it("navigates to a golongan's own screen", async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [rate(1, 1, 50_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await userEvent.click(await screen.findByRole('button', { name: text.cardAria('Pelaksana') }))

    expect(await screen.findByTestId('tier-screen')).toHaveTextContent('tier=1')
  })

  // A stale link to the edit dialog this section no longer has.
  it('clears a tier parameter that no longer names a dialog here', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')

    await screen.findByText('Pelaksana')
    await waitFor(() => expect(currentSearch()).toBe(''))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it("leaves another section's ?edit= alone", async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=location:3')

    await screen.findByText('Pelaksana')
    expect(currentSearch()).toBe('?edit=location:3')
  })
})
