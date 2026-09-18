import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DuesTierScreen from '@/screens/DuesTier'
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

function stubTier(tiers: ReturnType<typeof tier>[], rates: ReturnType<typeof rate>[], writeResponse?: () => Response) {
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

function renderScreen(tierId = 1) {
  const onBack = vi.fn()
  render(<DuesTierScreen tierId={tierId} onBack={onBack} />)
  return { onBack }
}

describe('A golongan screen', () => {
  it("shows the golongan's name and its whole rate history", async () => {
    const { fetchMock } = stubTier(
      [tier(1, 'Pelaksana')],
      [rate(1, 1, 50_000, '2026-01'), rate(2, 1, 60_000, '2026-07')],
    )
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    expect(await screen.findByRole('heading', { name: 'Pelaksana', level: 1 })).toBeInTheDocument()
    // The old rate stays: it is what explains the months it covered.
    expect(screen.getByText(text.effectiveFrom('Januari 2026'))).toBeInTheDocument()
    expect(screen.getByText(text.effectiveFrom('Juli 2026'))).toBeInTheDocument()
  })

  it('renders a golongan with no rates plainly, not as an error', async () => {
    const { fetchMock } = stubTier([tier(1, 'Madya')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    // A golongan whose price is not decided yet is a legal state (PRD section 6).
    expect(await screen.findByText(text.noRates)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // #285: the whole reason this is a screen. No dialog anywhere on it.
  it('holds no dialog at all', async () => {
    const { fetchMock } = stubTier([tier(1, 'Pelaksana')], [rate(1, 1, 50_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByRole('heading', { name: 'Pelaksana', level: 1 })

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    // Including while a rate is being corrected - the old shape put a
    // second save/cancel pair inside a dialog that already had a footer.
    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    // One save per thing being saved, each naming what it saves.
    expect(screen.getAllByRole('button', { name: text.saveRate })).toHaveLength(1)
  })

  it('renames the golongan', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Pelaksan')], [], () => jsonResponse(tier(1, 'Pelaksana')))
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    const input = await screen.findByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Pelaksana')
    await userEvent.click(screen.getByRole('button', { name: text.saveName }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({
      method: 'PATCH',
      url: expect.stringContaining('/api/dues-tiers/1'),
      body: { name: 'Pelaksana' },
    })
  })

  it('adds a rate effective from a month', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    await userEvent.type(await screen.findByLabelText(text.rateAmountLabel), '50000')
    await userEvent.click(screen.getByRole('button', { name: text.addRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({
      method: 'POST',
      url: expect.stringContaining('/api/dues-tiers/1/rates'),
      body: { amount: 50000 },
    })
    // Defaults to the current month; backdating is a deliberate act.
    expect(calls[0].body?.effective_from).toMatch(/^\d{4}-\d{2}$/)
  })

  // PRD section 7.1's live-arrears escape hatch depends on this: a rate can
  // start in a month that has already passed.
  it('lets a rate be backdated to a month already gone', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    await userEvent.type(await screen.findByLabelText(text.rateAmountLabel), '50000')
    const month = screen.getByLabelText(text.effectiveFromLabel)
    await userEvent.clear(month)
    await userEvent.type(month, '2024-03')
    await userEvent.click(screen.getByRole('button', { name: text.addRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0].body).toMatchObject({ amount: 50000, effective_from: '2024-03' })
  })

  // PATCH is for a mistyped amount, never a price change - the amount is the
  // only field it carries, so effective_from cannot move through an edit.
  it("corrects a rate's amount and nothing else", async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Pelaksana')], [rate(1, 1, 5_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    const input = screen.getByLabelText(text.rateAmountLabel, { selector: '#rate-amount-1' })
    await userEvent.clear(input)
    await userEvent.type(input, '50000')
    await userEvent.click(screen.getByRole('button', { name: text.saveRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({
      method: 'PATCH',
      url: expect.stringContaining('/api/dues-rates/1'),
      body: { amount: 50000 },
    })
    expect(calls[0].body).not.toHaveProperty('effective_from')
  })

  it('backs out of a correction without sending anything', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Pelaksana')], [rate(1, 1, 50_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    await userEvent.click(screen.getByRole('button', { name: text.cancel }))

    expect(screen.getByRole('button', { name: text.editRate })).toBeInTheDocument()
    expect(calls).toHaveLength(0)
  })

  // Deleting is what makes a wrong-month rate fixable at all: UNIQUE
  // (tier_id, effective_from) refuses the corrected row while it stands.
  it('deletes a rate filed against the wrong month', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Pelaksana')], [rate(1, 1, 50_000, '2026-01')], () =>
      new Response(null, { status: 204 }),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    await userEvent.click(screen.getByRole('button', { name: text.deleteRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/dues-rates/1') })
  })

  // Deleting takes the golongan's rates with it, and confirms in place -
  // never window.confirm(), and on a screen there is no dialog to nest.
  it('deletes the golongan after confirming in place', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Madya')], [rate(1, 1, 25_000, '2026-01')], () =>
      new Response(null, { status: 204 }),
    )
    vi.stubGlobal('fetch', fetchMock)
    const { onBack } = renderScreen()

    await userEvent.click(await screen.findByRole('button', { name: text.delete }))
    // The consequence is named before the action, and nothing has been sent.
    expect(screen.getByText(text.deleteConfirm)).toBeInTheDocument()
    expect(calls).toHaveLength(0)

    await userEvent.click(screen.getByRole('button', { name: text.deleteConfirmAction }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/dues-tiers/1') })
    // Nothing left to show, so she lands back on the list she came from.
    await waitFor(() => expect(onBack).toHaveBeenCalled())
  })

  it('says why a golongan a member is in cannot be deleted', async () => {
    const { fetchMock } = stubTier([tier(1, 'Penuh')], [], () =>
      jsonResponse({ error: { code: 'referenced_by_other_records', message: 'referenced' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    const { onBack } = renderScreen()

    await userEvent.click(await screen.findByRole('button', { name: text.delete }))
    await userEvent.click(screen.getByRole('button', { name: text.deleteConfirmAction }))

    expect(await screen.findByRole('alert')).toHaveTextContent(text.deleteRefused)
    // Still here, so she can go and change those members' golongan.
    expect(onBack).not.toHaveBeenCalled()
  })

  // A stale bookmark, or a golongan deleted from another device: App.tsx
  // answers a missing parameter, and this answers a parameter that named
  // something real once.
  it('goes back when the id names no golongan', async () => {
    const { fetchMock } = stubTier([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    const { onBack } = renderScreen(99)

    await waitFor(() => expect(onBack).toHaveBeenCalled())
  })

  it('leaves through its own back control', async () => {
    const { fetchMock } = stubTier([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    const { onBack } = renderScreen()

    await userEvent.click(await screen.findByRole('button', { name: text.backToSettings }))
    expect(onBack).toHaveBeenCalled()
  })
})
