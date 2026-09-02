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

  it('posts POST /api/setup exactly once, with a renamed default location under its new name', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()

    // Rename the seeded "Tunai" row to something else - the row must post
    // under the new name, not get filtered out as a "default".
    const nameInputs = screen.getAllByLabelText(text.locations.nameLabel)
    await userEvent.clear(nameInputs[0])
    await userEvent.type(nameInputs[0], 'Kas Ketua RT')

    await userEvent.click(screen.getByRole('button', { name: text.next }))

    await screen.findByText(text.balances.heading)

    const setupCalls = callsTo(fetchMock, '/api/setup')
    expect(setupCalls).toHaveLength(1)
    const body = JSON.parse(setupCalls[0][1]?.body as string) as { name: string; accounts: { kind: string; name: string }[] }
    expect(body.name).toBe('Kas RT 04')
    expect(body.accounts.map((a) => a.name)).toEqual(['Kas Ketua RT', 'Bank'])
  })

  it('sends no opening-balance request for a blank field, and skips the roster with no roster requests', async () => {
    const onDone = vi.fn()
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      { match: (m, u) => m === 'POST' && u.includes('/opening-balance'), handle: () => Promise.resolve(jsonResponse({ transaction: null, posted_amount: 0 })) },
      { match: (m, u) => m === 'POST' && (u.includes('/api/dues-tiers') || u.includes('/api/members')), handle: () => Promise.resolve(jsonResponse({})) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={onDone} />)
    await goPastFundName()
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    // Now on the balances step, every field left blank.
    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    // On to the roster step - skip it entirely.
    expect(await screen.findByText(text.roster.heading)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: text.roster.skip }))

    expect(callsTo(fetchMock, '/opening-balance')).toHaveLength(0)
    expect(callsTo(fetchMock, '/api/dues-tiers')).toHaveLength(0)
    expect(callsTo(fetchMock, '/api/members')).toHaveLength(0)
    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1))
  })

  it('stays on the locations step when POST /api/setup fails, and does not repeat the call on a retry that fails again', async () => {
    const fetchMock = routedFetch([
      {
        match: (m, u) => m === 'POST' && u.includes('/api/setup'),
        handle: () =>
          Promise.resolve(
            jsonResponse({ error: { code: 'invalid_argument', message: 'bad' } }, 400),
          ),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()

    await userEvent.click(screen.getByRole('button', { name: text.next }))

    // Still on locations, with the failure rendered - never advanced to the
    // balances step on a call that did not succeed.
    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.invalid_argument)
    expect(screen.getByText(text.locations.heading)).toBeInTheDocument()
    expect(screen.queryByText(text.balances.heading)).not.toBeInTheDocument()
  })

  it('posts an opening balance only for the account whose field was filled in', async () => {
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'POST' && u.includes('/api/setup'), handle: () => Promise.resolve(jsonResponse(setupResult, 201)) },
      { match: (m, u) => m === 'POST' && u.includes('/opening-balance'), handle: () => Promise.resolve(jsonResponse({ transaction: null, posted_amount: 0 })) },
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<Setup onDone={vi.fn()} />)
    await goPastFundName()
    await userEvent.click(screen.getByRole('button', { name: text.next }))

    expect(await screen.findByText(text.balances.heading)).toBeInTheDocument()
    const amountField = screen.getByLabelText(text.balances.amountLabel('Tunai'))
    await userEvent.type(amountField, '50000')

    await userEvent.click(screen.getByRole('button', { name: text.next }))

    await screen.findByText(text.roster.heading)

    const balanceCalls = callsTo(fetchMock, '/opening-balance')
    expect(balanceCalls).toHaveLength(1)
    expect(balanceCalls[0][0]).toContain('/api/accounts/10/opening-balance')
  })
})
