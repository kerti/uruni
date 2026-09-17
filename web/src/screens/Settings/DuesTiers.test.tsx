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

  it("lists a tier's rates with the month each starts from, inside its dialog", async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [rate(1, 1, 50_000, '2026-01'), rate(2, 1, 60_000, '2026-07')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')

    // The old rate stays: it is what explains the months it covered.
    expect(await screen.findByText(text.effectiveFrom('Januari 2026'))).toBeInTheDocument()
    expect(screen.getByText(text.effectiveFrom('Juli 2026'))).toBeInTheDocument()
  })

  it('adds a rate effective from a month, under its tier', async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')

    const form = within(await screen.findByRole('form', { name: text.addRate }))
    await userEvent.type(form.getByLabelText(text.rateAmountLabel), '50000')
    await userEvent.click(form.getByRole('button', { name: text.addRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', url: expect.stringContaining('/api/dues-tiers/1/rates'), body: { amount: 50000 } })
    // Defaults to the current month; backdating is a deliberate act.
    expect(calls[0].body?.effective_from).toMatch(/^\d{4}-\d{2}$/)
  })

  // PRD section 7.1's live-arrears escape hatch depends on this surviving the
  // move: a rate can start in a month that has already passed.
  it('lets a rate be backdated to a month already gone', async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')

    const form = within(await screen.findByRole('form', { name: text.addRate }))
    await userEvent.type(form.getByLabelText(text.rateAmountLabel), '50000')
    const month = form.getByLabelText(text.effectiveFromLabel)
    await userEvent.clear(month)
    await userEvent.type(month, '2024-03')
    await userEvent.click(form.getByRole('button', { name: text.addRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0].body).toMatchObject({ amount: 50000, effective_from: '2024-03' })
  })

  // PATCH is for a mistyped amount, never a price change - the amount is the
  // only field it carries, so effective_from cannot move through an edit.
  it("corrects a rate's amount and nothing else", async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Pelaksana')], [rate(1, 1, 5_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    const input = screen.getByLabelText(text.rateAmountLabel, { selector: `#rate-amount-1` })
    await userEvent.clear(input)
    await userEvent.type(input, '50000')
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', url: expect.stringContaining('/api/dues-rates/1'), body: { amount: 50000 } })
    expect(calls[0].body).not.toHaveProperty('effective_from')
  })

  // Deleting is what makes a wrong-month rate fixable at all: UNIQUE
  // (tier_id, effective_from) refuses the corrected row while it stands.
  it('deletes a rate filed against the wrong month', async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Pelaksana')], [rate(1, 1, 50_000, '2026-01')], () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.deleteRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/dues-rates/1') })
  })

  it('renames a tier from its dialog', async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Pelaksan')], [], () => jsonResponse(tier(1, 'Pelaksana')))
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await userEvent.click(await screen.findByRole('button', { name: text.editAria('Pelaksan') }))
    expect(currentSearch()).toBe('?edit=tier%3A1')

    const input = await screen.findByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Pelaksana')
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', url: expect.stringContaining('/api/dues-tiers/1'), body: { name: 'Pelaksana' } })
  })

  // #232: deleting takes the tier's rates with it, and confirms in this
  // dialog's own footer - never a nested dialog, never window.confirm().
  it('deletes a tier after confirming in place', async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Madya')], [rate(1, 1, 25_000, '2026-01')], () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')

    await userEvent.click(await screen.findByRole('button', { name: text.delete }))
    // The consequence is named before the action, and nothing has been sent.
    expect(screen.getByText(text.deleteConfirm)).toBeInTheDocument()
    expect(calls).toHaveLength(0)

    await userEvent.click(screen.getByRole('button', { name: text.deleteConfirmAction }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/dues-tiers/1') })
  })

  it('backs out of the delete without sending anything', async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Madya')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')

    await userEvent.click(await screen.findByRole('button', { name: text.delete }))
    await userEvent.click(screen.getByRole('button', { name: text.cancel }))

    expect(screen.queryByText(text.deleteConfirm)).not.toBeInTheDocument()
    expect(calls).toHaveLength(0)
  })

  // The acceptance criterion: the refusal names why, in Indonesian, never the
  // English wire message (ADR-014).
  it('says why a tier a member is in cannot be deleted', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Penuh')], [], () =>
      jsonResponse({ error: { code: 'referenced_by_other_records', message: 'referenced' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=tier:1')

    await userEvent.click(await screen.findByRole('button', { name: text.delete }))
    await userEvent.click(screen.getByRole('button', { name: text.deleteConfirmAction }))

    expect(await screen.findByRole('alert')).toHaveTextContent(text.deleteRefused)
    // Still open, so she can go and change those members' golongan.
    expect(screen.getByRole('dialog')).toBeInTheDocument()
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

  it("leaves another section's ?edit= alone", async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=location:3')

    await screen.findByText('Pelaksana')
    expect(currentSearch()).toBe('?edit=location:3')
  })
})
