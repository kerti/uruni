import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Setup from '@/screens/Setup/Setup'
import { copy } from '@/copy/id'

afterEach(() => {
  vi.unstubAllGlobals()
})

const text = copy.setup

const setupResult = {
  fund: { id: 1, name: 'Kas RT 04', currency: 'IDR', report_slug: 'kas-rt-04', created_at: 1 },
  main_purpose_id: 1,
  accounts: [
    { id: 10, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 },
    { id: 11, kind: 'bank', name: 'Bank', inactive_on: null, created_at: 1 },
  ],
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** Routes a stubbed fetch by method + path substring. Every call is recorded
 * on the returned mock's own `.mock.calls`, so a test can assert on what a
 * specific route was called with. */
function routedFetch(handlers: { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((h) => h.match(method, url))
    if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
    return handler.handle()
  })
}

/** Calls made to `fetchMock` whose URL contains `pathFragment`. */
function callsTo(fetchMock: ReturnType<typeof vi.fn>, pathFragment: string) {
  return fetchMock.mock.calls.filter(([input]) => {
    const url = typeof input === 'string' ? input : (input as URL | Request).toString()
    return url.includes(pathFragment)
  }) as [string, RequestInit | undefined][]
}

async function goPastFundName(name = 'Kas RT 04') {
  await userEvent.type(screen.getByLabelText(text.fund.nameLabel), name)
  await userEvent.click(screen.getByRole('button', { name: text.next }))
}

async function goPastLocations() {
  await userEvent.click(screen.getByRole('button', { name: text.next }))
}

describe('Setup', () => {
  it('renders the fund name step first', () => {
    render(<Setup onDone={vi.fn()} />)
    expect(screen.getByText(text.fund.heading)).toBeInTheDocument()
  })

  it('cannot remove the last remaining location row', async () => {
    vi.stubGlobal('fetch', vi.fn())
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()

    expect(screen.getByText(text.locations.heading)).toBeInTheDocument()
    const removeButtons = screen.getAllByRole('button', { name: text.locations.removeRow })
    expect(removeButtons).toHaveLength(2)

    await userEvent.click(removeButtons[0])
    // Down to one row: its own remove button is now disabled, and the
    // minimum-one-location message is shown.
    const remaining = screen.getAllByRole('button', { name: text.locations.removeRow })
    expect(remaining).toHaveLength(1)
    expect(remaining[0]).toBeDisabled()
    expect(screen.getByText(text.locations.minOneLocation)).toBeInTheDocument()
  })

  it('sends no request on the locations step - only the balances step fires POST /api/setup', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()

    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('posts POST /api/setup exactly once from the balances step, with a renamed default location under its new name', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      { match: (m, u) => m === 'POST' && (u.includes('/api/dues-tiers') || u.includes('/api/members')), handle: () => Promise.resolve(jsonResponse({})) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()

    // Rename the seeded "Tunai" row to something else - the row must post
    // under the new name, not get filtered out as a "default".
    const nameInputs = screen.getAllByLabelText(text.locations.nameLabel)
    await userEvent.clear(nameInputs[0])
    await userEvent.type(nameInputs[0], 'Kas Ketua RT')
    await goPastLocations()

    await screen.findByText(text.balances.heading)
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    await screen.findByText(text.roster.heading)

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(1)
    const body = JSON.parse(setupCalls[0][1]?.body as string) as { name: string; accounts: { kind: string; name: string }[] }
    expect(body.name).toBe('Kas RT 04')
    expect(body.accounts.map((a) => a.name)).toEqual(['Kas Ketua RT', 'Bank'])
  })

  it('sends no opening_balance for a blank field, and skips the roster with no roster requests', async () => {
    const onDone = vi.fn()
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      { match: (m, u) => m === 'POST' && (u.includes('/api/dues-tiers') || u.includes('/api/members')), handle: () => Promise.resolve(jsonResponse({})) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={onDone} />)
    await goPastFundName()
    await goPastLocations()

    // Now on the balances step, every field left blank.
    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(1)
    const body = JSON.parse(setupCalls[0][1]?.body as string) as { accounts: { opening_balance?: unknown }[] }
    expect(body.accounts.every((a) => a.opening_balance === undefined)).toBe(true)

    // On to the roster step - skip it entirely.
    expect(await screen.findByText(text.roster.heading)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: text.roster.skip }))

    expect(callsTo(fetchMock, '/api/dues-tiers')).toHaveLength(0)
    expect(callsTo(fetchMock, '/api/members')).toHaveLength(0)
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1))
  })

  it('sends an opening_balance only for the row whose field was filled in, on the single POST /api/setup', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()

    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    const amountField = screen.getByLabelText(text.balances.amountLabel('Tunai'))
    await userEvent.type(amountField, '50000')

    await userEvent.click(screen.getByRole('button', { name: text.next }))

    await screen.findByText(text.roster.heading)

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(1)
    const body = JSON.parse(setupCalls[0][1]?.body as string) as {
      accounts: { name: string; opening_balance?: { amount: number; note: string } }[]
    }
    const tunai = body.accounts.find((a) => a.name === 'Tunai')
    const bank = body.accounts.find((a) => a.name === 'Bank')
    // The note names its own location: this row is the first entry in the
    // fund's ledger and it is read again months later, in home's recent
    // activity and in the public report, without the wizard around it.
    expect(tunai?.opening_balance).toMatchObject({ amount: 50000, note: text.balances.note('Tunai') })
    expect(bank?.opening_balance).toBeUndefined()
  })

  it('going back from balances to locations and returning keeps names and amounts', async () => {
    vi.stubGlobal('fetch', vi.fn())
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()

    const nameInputs = screen.getAllByLabelText(text.locations.nameLabel)
    await userEvent.clear(nameInputs[0])
    await userEvent.type(nameInputs[0], 'Kas Ketua RT')
    await goPastLocations()

    await screen.findByText(text.balances.heading)
    const tunaiAmount = screen.getByLabelText(text.balances.amountLabel('Kas Ketua RT'))
    await userEvent.type(tunaiAmount, '75000')

    await userEvent.click(screen.getByRole('button', { name: text.back }))
    expect(await screen.findByText(text.locations.heading)).toBeInTheDocument()
    expect(screen.getAllByLabelText(text.locations.nameLabel)[0]).toHaveValue('Kas Ketua RT')

    await goPastLocations()
    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    expect(screen.getByLabelText(text.balances.amountLabel('Kas Ketua RT'))).toHaveValue('75.000')
  })

  it('removing a location after going back drops only its own amount - the rest stay on their own rows', async () => {
    vi.stubGlobal('fetch', vi.fn())
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()

    await screen.findByText(text.balances.heading)
    await userEvent.type(screen.getByLabelText(text.balances.amountLabel('Tunai')), '10000')
    await userEvent.type(screen.getByLabelText(text.balances.amountLabel('Bank')), '20000')

    await userEvent.click(screen.getByRole('button', { name: text.back }))
    expect(await screen.findByText(text.locations.heading)).toBeInTheDocument()

    // Remove the first row (Tunai) - Bank's own amount must survive.
    await userEvent.click(screen.getAllByRole('button', { name: text.locations.removeRow })[0])
    await goPastLocations()

    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    expect(screen.queryByLabelText(text.balances.amountLabel('Tunai'))).not.toBeInTheDocument()
    expect(screen.getByLabelText(text.balances.amountLabel('Bank'))).toHaveValue('20.000')
  })

  it('stays on the balances step when POST /api/setup fails, and a resubmit sends one complete request', async () => {
    let calls = 0
    const fetchMock = routedFetch([
      {
        match: (m, u) => m === 'POST' && u.includes('/api/setup'),
        handle: () => {
          calls += 1
          if (calls === 1) return Promise.resolve(jsonResponse({ error: { code: 'invalid_argument', message: 'bad' } }, 400))
          return Promise.resolve(jsonResponse(setupResult, 201))
        },
      },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()

    await screen.findByText(text.balances.heading)
    await userEvent.type(screen.getByLabelText(text.balances.amountLabel('Tunai')), '50000')
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    // Still on balances, with the failure rendered - never advanced past a
    // call that did not succeed.
    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.invalid_argument)
    expect(screen.getByText(text.balances.heading)).toBeInTheDocument()
    expect(screen.queryByText(text.roster.heading)).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(2)
    const body = JSON.parse(setupCalls[1][1]?.body as string) as {
      accounts: { name: string; opening_balance?: { amount: number } }[]
    }
    expect(body.accounts.find((a) => a.name === 'Tunai')?.opening_balance).toMatchObject({ amount: 50000 })
  })

  it('never calls the deleted per-account opening-balance route', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await goPastLocations()
    await screen.findByText(text.balances.heading)
    await userEvent.type(screen.getByLabelText(text.balances.amountLabel('Tunai')), '50000')
    await userEvent.click(screen.getByRole('button', { name: text.next }))
    await screen.findByText(text.roster.heading)

    expect(callsTo(fetchMock, '/opening-balance')).toHaveLength(0)
  })
})
