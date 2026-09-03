import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DuesTiers from '@/screens/Members/DuesTiers'
import { copy } from '@/copy/id'

const text = copy.members.tiers

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

describe('DuesTiers', () => {
  it('renders a tier with no rates plainly, not as an error', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesTiers onTiersChanged={vi.fn()} />)

    expect(await screen.findByText('Pelaksana')).toBeInTheDocument()
    // A tier whose price is not decided yet is a legal state (PRD §6).
    // findByText, not getByText: a tier's rates load one request behind the
    // tier list itself, so the section is still "Memuat…" at this point.
    expect(await screen.findByText(text.noRates)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('lists a tier\'s rates with the month each starts from', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [rate(1, 1, 50_000, '2026-01'), rate(2, 1, 60_000, '2026-07')])
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesTiers onTiersChanged={vi.fn()} />)

    // The old rate stays: it is what explains the months it covered.
    expect(await screen.findByText(text.effectiveFrom('Januari 2026'))).toBeInTheDocument()
    expect(screen.getByText(text.effectiveFrom('Juli 2026'))).toBeInTheDocument()
  })

  it('adds a tier', async () => {
    const { fetchMock, calls } = stubTiers([], [])
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesTiers onTiersChanged={vi.fn()} />)
    await screen.findByText(text.empty)

    const form = within(screen.getByRole('form', { name: text.add }))
    await userEvent.type(form.getByLabelText(text.nameLabel), 'Fungsional')
    await userEvent.click(form.getByRole('button', { name: text.add }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', url: expect.stringContaining('/api/dues-tiers'), body: { name: 'Fungsional' } })
  })

  it('adds a rate effective from a month, under its tier', async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Pelaksana')], [])
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesTiers onTiersChanged={vi.fn()} />)
    await screen.findByText('Pelaksana')

    const form = within(screen.getByRole('form', { name: text.addRate }))
    await userEvent.type(form.getByLabelText(text.rateAmountLabel), '50000')
    await userEvent.click(form.getByRole('button', { name: text.addRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', url: expect.stringContaining('/api/dues-tiers/1/rates'), body: { amount: 50000 } })
    // Defaults to the current month; backdating is a deliberate act.
    expect(calls[0].body?.effective_from).toMatch(/^\d{4}-\d{2}$/)
  })

  // PATCH is for a mistyped amount, never a price change - the amount is the
  // only field it carries, so effective_from cannot move through an edit.
  it('corrects a rate\'s amount and nothing else', async () => {
    const { fetchMock, calls } = stubTiers([tier(1, 'Pelaksana')], [rate(1, 1, 5_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesTiers onTiersChanged={vi.fn()} />)
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
    render(<DuesTiers onTiersChanged={vi.fn()} />)
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.deleteRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/dues-rates/1') })
  })

  it('renders a duplicate tier name as the 409 it is', async () => {
    const { fetchMock } = stubTiers([tier(1, 'Pelaksana')], [], () =>
      jsonResponse({ error: { code: 'unique_violation', message: 'conflict' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesTiers onTiersChanged={vi.fn()} />)
    await screen.findByText('Pelaksana')

    const form = within(screen.getByRole('form', { name: text.add }))
    await userEvent.type(form.getByLabelText(text.nameLabel), 'Pelaksana')
    await userEvent.click(form.getByRole('button', { name: text.add }))

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.unique_violation)
  })
})
