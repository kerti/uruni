import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DuesTierScreen from '@/screens/DuesTier'
import { copy } from '@/copy/id'
import { formatPeriod } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { pickMonth } from '@/test/datePicker'
import type { DuesRate, DuesTier, DuesTierWithRates } from '@/lib/setup'

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

function stubTier(tiers: DuesTier[], rates: DuesRate[], writeResponse?: () => Response) {
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

/** Exact text of an element, including the no-break space formatIDR writes
 * (the default matcher folds it to a plain space on one side only). */
function exactly(expected: string) {
  return (_content: string, element: Element | null) => element?.children.length === 0 && element.textContent === expected
}

function renderScreen(tierId = 1) {
  const onBack = vi.fn()
  render(<DuesTierScreen tierId={tierId} onBack={onBack} />)
  return { onBack }
}

describe('A tier screen', () => {
  it("shows the tier's name and its whole rate history", async () => {
    const { fetchMock } = stubTier([tier(1, 'Reguler')], [rate(1, 1, 50_000, '2026-01'), rate(2, 1, 60_000, '2026-07')])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    expect(await screen.findByRole('heading', { name: 'Reguler', level: 1 })).toBeInTheDocument()
    // The old rate stays: it is what explains the months it covered.
    expect(screen.getByText(text.effectiveFrom('Januari 2026'))).toBeInTheDocument()
    expect(screen.getByText(text.effectiveFrom('Juli 2026'))).toBeInTheDocument()
  })

  it('renders a tier with no rates plainly, not as an error', async () => {
    const { fetchMock } = stubTier([tier(1, 'Senior')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    // A tier whose price is not decided yet is a legal state (PRD section 6).
    expect(await screen.findByText(text.noRates)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // The whole reason this is a screen: no dialog anywhere on it.
  it('holds no dialog at all', async () => {
    const { fetchMock } = stubTier([tier(1, 'Reguler')], [rate(1, 1, 50_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByRole('heading', { name: 'Reguler', level: 1 })

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    // Including while a rate is being corrected - a dialog shape put a second
    // save/cancel pair inside a dialog that already had a footer.
    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    // One save per thing being saved, each naming what it saves.
    expect(screen.getAllByRole('button', { name: text.saveRate })).toHaveLength(1)
  })

  it('renames the tier', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Regule')], [], () => jsonResponse(tier(1, 'Reguler')))
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    const input = await screen.findByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Reguler')
    await userEvent.click(screen.getByRole('button', { name: text.saveName }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({
      method: 'PATCH',
      url: expect.stringContaining('/api/dues-tiers/1'),
      body: { name: 'Reguler' },
    })
  })

  it('adds a rate effective from a month', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Reguler')], [])
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
    const { fetchMock, calls } = stubTier([tier(1, 'Reguler')], [])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()

    await userEvent.type(await screen.findByLabelText(text.rateAmountLabel), '50000')
    await pickMonth(text.effectiveFromLabel, '2024-03')
    await userEvent.click(screen.getByRole('button', { name: text.addRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0].body).toMatchObject({ amount: 50000, effective_from: '2024-03' })
  })

  // PATCH is for a mistyped amount, never a price change - the amount is the
  // only field it carries, so effective_from cannot move through an edit.
  it("corrects a rate's amount and nothing else", async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Reguler')], [rate(1, 1, 5_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    const input = screen.getByLabelText(text.rateAmountLabel, { selector: '#rate-amount-1' })
    await userEvent.clear(input)
    await userEvent.type(input, '50000')
    await userEvent.click(screen.getByRole('button', { name: text.saveRate }))
    expect(calls).toHaveLength(0)
    await userEvent.click(screen.getByRole('button', { name: text.rateCorrectConfirmAction }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({
      method: 'PATCH',
      url: expect.stringContaining('/api/dues-rates/1'),
      body: { amount: 50000 },
    })
    expect(calls[0].body).not.toHaveProperty('effective_from')
  })

  // ADR-040: a correction reprices the months the rate covers, so the
  // confirmation names them before anything is sent.
  describe('confirming a correction', () => {
    async function startCorrection(rates: DuesRate[], rateId: number, typed: string) {
      const { fetchMock, calls } = stubTier([tier(1, 'Reguler')], rates)
      vi.stubGlobal('fetch', fetchMock)
      renderScreen()
      await screen.findByRole('heading', { name: 'Reguler', level: 1 })
      const row = screen.getByText(text.effectiveFrom(formatPeriod(rates.find((r) => r.id === rateId)!.effective_from))).closest('li')!
      await userEvent.click(within(row).getByRole('button', { name: text.editRate }))
      const input = within(row).getByLabelText(text.rateAmountLabel)
      await userEvent.clear(input)
      await userEvent.type(input, typed)
      await userEvent.click(within(row).getByRole('button', { name: text.saveRate }))
      return { calls, row }
    }

    it('names the months up to the next rate that stands', async () => {
      const { calls, row } = await startCorrection([rate(1, 1, 50_000, '2026-01'), rate(2, 1, 60_000, '2026-07')], 1, '55000')

      expect(
        within(row).getByText(exactly(text.rateCorrectConfirm(formatIDR(50_000), formatIDR(55_000), 'Januari 2026 sampai Juni 2026'))),
      ).toBeInTheDocument()
      expect(calls).toHaveLength(0)
    })

    it('says "seterusnya" for the last rate', async () => {
      const { row } = await startCorrection([rate(1, 1, 50_000, '2026-01'), rate(2, 1, 60_000, '2026-07')], 2, '65000')

      expect(
        within(row).getByText(exactly(text.rateCorrectConfirm(formatIDR(60_000), formatIDR(65_000), 'Juli 2026 seterusnya'))),
      ).toBeInTheDocument()
    })

    it('names a single month when the next rate starts right after', async () => {
      const { row } = await startCorrection([rate(1, 1, 50_000, '2026-03'), rate(2, 1, 60_000, '2026-04')], 1, '51000')

      expect(
        within(row).getByText(exactly(text.rateCorrectConfirm(formatIDR(50_000), formatIDR(51_000), 'Maret 2026'))),
      ).toBeInTheDocument()
    })

    it('sends nothing when she backs out, and keeps the form to edit again', async () => {
      const { calls, row } = await startCorrection([rate(1, 1, 50_000, '2026-01')], 1, '55000')

      await userEvent.click(within(row).getByRole('button', { name: copy.common.close }))

      expect(within(row).getByRole('button', { name: text.saveRate })).toBeInTheDocument()
      expect(within(row).queryByText(/seterusnya/)).not.toBeInTheDocument()
      expect(calls).toHaveLength(0)
    })

    it('asks nothing when the amount is unchanged', async () => {
      const { calls, row } = await startCorrection([rate(1, 1, 50_000, '2026-01')], 1, '50000')

      expect(within(row).queryByRole('button', { name: text.rateCorrectConfirmAction })).not.toBeInTheDocument()
      expect(calls).toHaveLength(0)
    })
  })

  // A correction is a new row with a new id: a stale page is refused 409
  // dues_rate_superseded, and the list must reload.
  it('reloads the rate list and says so when a rate was already corrected elsewhere', async () => {
    let listed = [rate(1, 1, 50_000, '2026-01')]
    let rateReads = 0
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = init?.method ?? 'GET'
      if (url.includes('/api/dues-rates/1') && method === 'PATCH') {
        // Meanwhile another screen corrected it: id 1 no longer stands.
        listed = [rate(7, 1, 52_000, '2026-01')]
        return Promise.resolve(jsonResponse({ error: { code: 'dues_rate_superseded', message: 'superseded' } }, 409))
      }
      if (url.includes('/api/dues-tiers')) {
        rateReads++
        return Promise.resolve(jsonResponse(withRates([tier(1, 'Reguler')], listed)))
      }
      return Promise.reject(new Error(`unstubbed fetch: ${url}`))
    })
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    const input = screen.getByLabelText(text.rateAmountLabel, { selector: '#rate-amount-1' })
    await userEvent.clear(input)
    await userEvent.type(input, '55000')
    await userEvent.click(screen.getByRole('button', { name: text.saveRate }))
    await userEvent.click(screen.getByRole('button', { name: text.rateCorrectConfirmAction }))

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.dues_rate_superseded)
    await waitFor(() => expect(rateReads).toBeGreaterThan(1))
    expect(await screen.findByText(exactly(formatIDR(52_000)))).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.editRate })).toBeInTheDocument()
  })

  it('backs out of a correction without sending anything', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Reguler')], [rate(1, 1, 50_000, '2026-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    await userEvent.click(screen.getByRole('button', { name: copy.common.back }))

    expect(screen.getByRole('button', { name: text.editRate })).toBeInTheDocument()
    expect(calls).toHaveLength(0)
  })

  // Deleting is what makes a wrong-month rate fixable at all: UNIQUE
  // (tier_id, effective_from) refuses the corrected row while it stands.
  it('deletes a rate filed against the wrong month', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Reguler')], [rate(1, 1, 50_000, '2026-01')], () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    renderScreen()
    await screen.findByText(text.effectiveFrom('Januari 2026'))

    await userEvent.click(screen.getByRole('button', { name: text.editRate }))
    await userEvent.click(screen.getByRole('button', { name: text.deleteRate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/dues-rates/1') })
  })

  // Deleting takes the tier's rates with it, and confirms in place -
  // never window.confirm(), and on a screen there is no dialog to nest.
  it('deletes the tier after confirming in place', async () => {
    const { fetchMock, calls } = stubTier([tier(1, 'Senior')], [rate(1, 1, 25_000, '2026-01')], () => new Response(null, { status: 204 }))
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

  it('says why a tier a member is in cannot be deleted', async () => {
    const { fetchMock } = stubTier([tier(1, 'Penuh')], [], () =>
      jsonResponse({ error: { code: 'referenced_by_other_records', message: 'referenced' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    const { onBack } = renderScreen()

    await userEvent.click(await screen.findByRole('button', { name: text.delete }))
    await userEvent.click(screen.getByRole('button', { name: text.deleteConfirmAction }))

    expect(await screen.findByRole('alert')).toHaveTextContent(text.deleteRefused)
    // Still here, so she can read why and rename it instead.
    expect(onBack).not.toHaveBeenCalled()
  })

  // A stale bookmark, or a tier deleted from another device: App.tsx
  // answers a missing parameter, and this answers a parameter that named
  // something real once.
  it('goes back when the id names no tier', async () => {
    const { fetchMock } = stubTier([tier(1, 'Reguler')], [])
    vi.stubGlobal('fetch', fetchMock)
    const { onBack } = renderScreen(99)

    await waitFor(() => expect(onBack).toHaveBeenCalled())
  })

  it('leaves through its own back control', async () => {
    const { fetchMock } = stubTier([tier(1, 'Reguler')], [])
    vi.stubGlobal('fetch', fetchMock)
    const { onBack } = renderScreen()

    await userEvent.click(await screen.findByRole('button', { name: text.backToSettings }))
    expect(onBack).toHaveBeenCalled()
  })
})
