import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DuesTiers from '@/screens/Settings/DuesTiers'
import { copy } from '@/copy/id'
import type { DuesRate, DuesTier, DuesTierWithRates } from '@/lib/setup'
import { cardName } from '@/test/card'

const text = copy.settings.tiers

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function tier(id: number, name: string): DuesTier {
  return { id, name, created_at: 1 }
}

function rate(id: number, tierId: number, amount: number, effectiveFrom: string): DuesRate {
  return { id, tier_id: tierId, amount, effective_from: effectiveFrom, created_at: 1 }
}

function withRates(tiers: DuesTier[], rates: DuesRate[]): DuesTierWithRates[] {
  return tiers.map((t) => ({ ...t, rates: rates.filter((r) => r.tier_id === t.id) }))
}

function stubTiers(tiers: DuesTier[], rates: DuesRate[], writeResponse?: () => Response) {
  const calls: { method: string; url: string; body: Record<string, unknown> | undefined }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    const write = () => {
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(writeResponse ? writeResponse() : jsonResponse(rate(9, 1, 50_000, '2026-09')))
    }
    if (url.includes('/api/dues-rates')) return write()
    // GET /api/dues-tiers carries each tier's rates (#672); nothing reads a
    // tier's rates on their own any more.
    if (url.includes('/api/dues-tiers') && method === 'GET') return Promise.resolve(jsonResponse(withRates(tiers, rates)))
    if (url.includes('/api/dues-tiers')) return write()
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

/** This section's dialogs ARE the URL (ADR-032), so `?edit=` is state worth
 * asserting on. */
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
        {/* The tier's own screen (#285) stands in for App.tsx's route
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
  // opening anything: what does this tier cost now.
  it('shows each tier with the rate in force this month', async () => {
    const thisYear = new Date().getFullYear()
    const { fetchMock } = stubTiers(
      [tier(1, 'Reguler')],
      [rate(1, 1, 50_000, `${thisYear - 1}-01`), rate(2, 1, 60_000, `${thisYear + 1}-01`)],
    )
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByText('Reguler')).toBeInTheDocument()
    // The rate already in force, never the one that starts next year - the
    // same "latest row at or before this month" the server reads.
    await waitFor(() => expect(screen.getByText(/50\.000/)).toBeInTheDocument())
    expect(screen.queryByText(/60\.000/)).not.toBeInTheDocument()
  })

  // #672: one request for every tier and its rates, never one per tier after
  // the list.
  it('prices every tier from one request', async () => {
    const thisYear = new Date().getFullYear()
    const { fetchMock } = stubTiers(
      [tier(1, 'Reguler'), tier(2, 'Senior'), tier(3, 'Utama')],
      [rate(1, 1, 50_000, `${thisYear - 1}-01`), rate(2, 2, 75_000, `${thisYear - 1}-01`), rate(3, 3, 120_000, `${thisYear - 1}-01`)],
    )
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await waitFor(() => expect(screen.getByText(/120\.000/)).toBeInTheDocument())
    expect(screen.getByText(/50\.000/)).toBeInTheDocument()
    expect(screen.getByText(/75\.000/)).toBeInTheDocument()
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual(['/api/dues-tiers'])
  })

  it('renders a tier with no rates plainly, not as an error', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Reguler')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByText('Reguler')).toBeInTheDocument()
    // A tier whose price is not decided yet is a legal state (PRD section 6).
    expect(await screen.findByText(text.noRates)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('has no inline form on the section itself', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Reguler')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByText('Reguler')

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
    const { fetchMock } = stubTiers([tier(1, 'Reguler')], [], () =>
      jsonResponse({ error: { code: 'unique_violation', message: 'conflict' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:new')

    const dialog = within(await screen.findByRole('dialog'))
    await userEvent.type(dialog.getByLabelText(text.nameLabel), 'Reguler')
    await userEvent.click(dialog.getByRole('button', { name: text.add }))

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.unique_violation)
  })

  // The card is the way into the tier's screen, not into a dialog holding
  // its whole life.
  it("navigates to a tier's own screen", async () => {
    const { fetchMock } = stubTiers([tier(1, 'Reguler')], [rate(1, 1, 50_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await userEvent.click(await screen.findByRole('button', { name: cardName(text.cardVerb, 'Reguler') }))

    expect(await screen.findByTestId('tier-screen')).toHaveTextContent('tier=1')
  })

  // A stale link to the edit dialog this section no longer has.
  it('clears a tier parameter that no longer names a dialog here', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Reguler')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')

    await screen.findByText('Reguler')
    await waitFor(() => expect(currentSearch()).toBe(''))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it("leaves another section's ?edit= alone", async () => {
    const { fetchMock } = stubTiers([tier(1, 'Reguler')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=location:3')

    await screen.findByText('Reguler')
    expect(currentSearch()).toBe('?edit=location:3')
  })
})
