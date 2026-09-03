import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Reconcile from '@/screens/Reconcile'
import { copy } from '@/copy/id'

const text = copy.reconciliation

afterEach(() => {
  vi.unstubAllGlobals()
})

const accounts = [
  { id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 },
  { id: 2, kind: 'bank', name: 'Bank lama', inactive_on: '2026-01-01', created_at: 1 },
  { id: 3, kind: 'bank', name: 'Bank Uji Coba', inactive_on: null, created_at: 1 },
]

const purposes = [
  { id: 10, kind: 'pass_through', name: 'Kas Bidang', created_at: 1 },
  { id: 11, kind: 'main', name: 'Kas utama', created_at: 1 },
]

function balancesBody(tunai: number, bankUjiCoba: number) {
  return {
    fund_total: tunai + bankUjiCoba,
    accounts: [
      { id: 1, kind: 'cash', name: 'Tunai', balance: tunai },
      { id: 2, kind: 'bank', name: 'Bank lama', balance: 0 },
      { id: 3, kind: 'bank', name: 'Bank Uji Coba', balance: bankUjiCoba },
    ],
    purposes: [
      { id: 10, kind: 'pass_through', name: 'Kas Bidang', balance: 0 },
      { id: 11, kind: 'main', name: 'Kas utama', balance: tunai + bankUjiCoba },
    ],
  }
}

const transactions: unknown[] = []

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** Routes a stubbed fetch by method + path substring, same idiom as
 * RecordTransaction.test.tsx's own routedFetch - a handler can be called
 * more than once and each `handle` decides its own response per call. */
function routedFetch(handlers: { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((h) => h.match(method, url))
    if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
    return handler.handle()
  })
}

function stubLoad(balances = balancesBody(100_000, 200_000)) {
  return [
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/balances'), handle: () => Promise.resolve(jsonResponse(balances)) },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/transactions'), handle: () => Promise.resolve(jsonResponse(transactions)) },
  ]
}

function reconciliationDetail(lines: Array<Record<string, unknown>>) {
  return {
    id: 1,
    performed_at: 1_756_000_000,
    through_transaction_id: 9,
    note: null,
    created_at: 1_756_000_000,
    lines,
  }
}

async function waitForForm() {
  await screen.findByText(text.heading)
}

describe('Reconcile', () => {
  it('excludes a retired account from the count list', async () => {
    vi.stubGlobal('fetch', routedFetch(stubLoad()))
    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)

    await waitForForm()
    expect(screen.getByLabelText(text.actualLabel('Tunai'))).toBeInTheDocument()
    expect(screen.getByLabelText(text.actualLabel('Bank Uji Coba'))).toBeInTheDocument()
    expect(screen.queryByLabelText(text.actualLabel('Bank lama'))).not.toBeInTheDocument()
  })

  it('posts every active account as "matched" with no fix when every count equals the recorded balance', async () => {
    const fetchMock = routedFetch([
      ...stubLoad(),
      {
        match: (m, u) => m === 'POST' && u.includes('/api/reconciliations'),
        handle: () =>
          Promise.resolve(
            jsonResponse(
              reconciliationDetail([
                { id: 1, account_id: 1, recorded_amount: 100_000, actual_amount: 100_000, difference_amount: 0, resolution: 'matched', adjustment_transaction_id: null },
                { id: 2, account_id: 3, recorded_amount: 200_000, actual_amount: 200_000, difference_amount: 0, resolution: 'matched', adjustment_transaction_id: null },
              ]),
              201,
            ),
          ),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)

    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)
    await waitForForm()

    await userEvent.type(screen.getByLabelText(text.actualLabel('Tunai')), '100000')
    await userEvent.type(screen.getByLabelText(text.actualLabel('Bank Uji Coba')), '200000')

    // A zero-gap line needs no resolution choice - matched is reached
    // without ever touching a resolution button.
    expect(await screen.findAllByText(text.matched)).toHaveLength(2)
    expect(screen.getByRole('button', { name: text.submit })).not.toBeDisabled()

    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    const postCall = fetchMock.mock.calls.find(([input]) => (input as string).toString().includes('/api/reconciliations') )
    expect(postCall).toBeDefined()
    const body = JSON.parse((postCall![1] as RequestInit).body as string) as { counts: Array<Record<string, unknown>> }
    expect(body.counts).toHaveLength(2)
    for (const count of body.counts) {
      expect(count.resolution).toBe('matched')
      expect(count).not.toHaveProperty('fix')
    }

    // The confirmation renders from the POST response, not the preview.
    await waitFor(() => expect(screen.getByText(text.matched)).toBeInTheDocument())
  })

  it('reaches all three per-line resolutions and posts the fix for entry_added', async () => {
    const fetchMock = routedFetch([
      ...stubLoad(),
      {
        match: (m, u) => m === 'POST' && u.includes('/api/reconciliations'),
        handle: () =>
          Promise.resolve(
            jsonResponse(
              reconciliationDetail([
                { id: 1, account_id: 1, recorded_amount: 100_000, actual_amount: 150_000, difference_amount: 50_000, resolution: 'entry_added', adjustment_transaction_id: null },
                { id: 2, account_id: 3, recorded_amount: 200_000, actual_amount: 200_000, difference_amount: 0, resolution: 'matched', adjustment_transaction_id: null },
              ]),
              201,
            ),
          ),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)

    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)
    await waitForForm()

    await userEvent.type(screen.getByLabelText(text.actualLabel('Tunai')), '150000')
    await userEvent.type(screen.getByLabelText(text.actualLabel('Bank Uji Coba')), '200000')

    expect(await screen.findByText(text.discrepancy('Rp 50.000'))).toBeInTheDocument()

    // All three resolutions are reachable from the same gap.
    expect(screen.getByRole('button', { name: text.resolutionOptions.entry_added })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.resolutionOptions.adjusted })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.resolutionOptions.left_open })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: text.resolutionOptions.entry_added }))

    // The fix's amount/direction default off the gap: actual > recorded is
    // an unrecorded inflow, so direction defaults to "in" and amount to the
    // gap's own magnitude - nothing left to type for the common case.
    expect(screen.getByRole('button', { name: copy.record.directionIn })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText(text.fixAmountLabel)).toHaveValue('Rp 50.000')

    expect(screen.getByRole('button', { name: text.submit })).not.toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    const postCall = fetchMock.mock.calls.find(([input]) => (input as string).toString().includes('/api/reconciliations'))
    const body = JSON.parse((postCall![1] as RequestInit).body as string) as {
      counts: Array<{ account_id: number; resolution: string; fix?: Record<string, unknown> }>
    }
    const tunaiCount = body.counts.find((c) => c.account_id === 1)
    expect(tunaiCount?.resolution).toBe('entry_added')
    expect(tunaiCount?.fix).toMatchObject({ purpose_id: 11, direction: 'in', amount: 50_000 })
    expect(typeof tunaiCount?.fix?.occurred_on).toBe('string')
  })

  it('reaches the adjusted resolution and posts kind-appropriate fix data', async () => {
    const fetchMock = routedFetch([
      ...stubLoad(),
      {
        match: (m, u) => m === 'POST' && u.includes('/api/reconciliations'),
        handle: () =>
          Promise.resolve(
            jsonResponse(
              reconciliationDetail([
                { id: 1, account_id: 1, recorded_amount: 100_000, actual_amount: 80_000, difference_amount: -20_000, resolution: 'adjusted', adjustment_transaction_id: 55 },
                { id: 2, account_id: 3, recorded_amount: 200_000, actual_amount: 200_000, difference_amount: 0, resolution: 'matched', adjustment_transaction_id: null },
              ]),
              201,
            ),
          ),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)

    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)
    await waitForForm()

    await userEvent.type(screen.getByLabelText(text.actualLabel('Tunai')), '80000')
    await userEvent.type(screen.getByLabelText(text.actualLabel('Bank Uji Coba')), '200000')

    await userEvent.click(await screen.findByRole('button', { name: text.resolutionOptions.adjusted }))
    // actual < recorded is an unrecorded outflow - direction defaults "out".
    expect(screen.getByRole('button', { name: copy.record.directionOut })).toHaveAttribute('aria-pressed', 'true')

    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    const postCall = fetchMock.mock.calls.find(([input]) => (input as string).toString().includes('/api/reconciliations'))
    const body = JSON.parse((postCall![1] as RequestInit).body as string) as {
      counts: Array<{ account_id: number; resolution: string; fix?: Record<string, unknown> }>
    }
    const tunaiCount = body.counts.find((c) => c.account_id === 1)
    expect(tunaiCount?.resolution).toBe('adjusted')
    expect(tunaiCount?.fix).toMatchObject({ direction: 'out', amount: 20_000 })
  })

  it('reaches the left_open resolution and posts no fix at all', async () => {
    const fetchMock = routedFetch([
      ...stubLoad(),
      {
        match: (m, u) => m === 'POST' && u.includes('/api/reconciliations'),
        handle: () =>
          Promise.resolve(
            jsonResponse(
              reconciliationDetail([
                { id: 1, account_id: 1, recorded_amount: 100_000, actual_amount: 80_000, difference_amount: -20_000, resolution: 'left_open', adjustment_transaction_id: null },
                { id: 2, account_id: 3, recorded_amount: 200_000, actual_amount: 200_000, difference_amount: 0, resolution: 'matched', adjustment_transaction_id: null },
              ]),
              201,
            ),
          ),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)

    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)
    await waitForForm()

    await userEvent.type(screen.getByLabelText(text.actualLabel('Tunai')), '80000')
    await userEvent.type(screen.getByLabelText(text.actualLabel('Bank Uji Coba')), '200000')

    await userEvent.click(await screen.findByRole('button', { name: text.resolutionOptions.left_open }))
    // Choosing "left_open" surfaces no fix fields - there is nothing left to
    // fill in for a gap she is deliberately leaving open.
    expect(screen.queryByLabelText(text.fixAmountLabel)).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    const postCall = fetchMock.mock.calls.find(([input]) => (input as string).toString().includes('/api/reconciliations'))
    const body = JSON.parse((postCall![1] as RequestInit).body as string) as { counts: Array<{ account_id: number; resolution: string; fix?: unknown }> }
    const tunaiCount = body.counts.find((c) => c.account_id === 1)
    expect(tunaiCount?.resolution).toBe('left_open')
    expect(tunaiCount).not.toHaveProperty('fix')

    // The confirmation still reflects a remaining open gap - never the calm
    // "cocok" copy when a line was deliberately left open.
    await waitFor(() => expect(screen.getByText(text.discrepancy('Rp 20.000'))).toBeInTheDocument())
  })

  // Orchestrator ruling 2: the server rejects a "matched" line whose real
  // difference is no longer 0 because a write landed between the count and
  // the submit (reconciliation.go:185-188). The client must re-read the
  // numbers and ask her to resolve again - never look like a crash, and
  // never silently resubmit.
  it('recovers from a rejected "matched" line by re-reading balances instead of resubmitting', async () => {
    let balancesCallCount = 0
    const fetchMock = routedFetch([
      { match: (m, u) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
      { match: (m, u) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      {
        match: (m, u) => m === 'GET' && u.includes('/api/balances'),
        handle: () => {
          balancesCallCount += 1
          // Second read (after the rejected submit) shows Tunai's recorded
          // balance moved from 100_000 to 130_000 - a transaction landed in
          // between, exactly the mechanism this failure path exists for.
          const balances = balancesCallCount === 1 ? balancesBody(100_000, 200_000) : balancesBody(130_000, 200_000)
          return Promise.resolve(jsonResponse(balances))
        },
      },
      { match: (m, u) => m === 'GET' && u.includes('/api/transactions'), handle: () => Promise.resolve(jsonResponse(transactions)) },
      {
        match: (m, u) => m === 'POST' && u.includes('/api/reconciliations'),
        handle: () =>
          Promise.resolve(
            jsonResponse({ error: { code: 'invalid_argument', message: 'resolution "matched" for account 1 has a difference of 30000' } }, 400),
          ),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)

    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)
    await waitForForm()

    await userEvent.type(screen.getByLabelText(text.actualLabel('Tunai')), '100000')
    await userEvent.type(screen.getByLabelText(text.actualLabel('Bank Uji Coba')), '200000')
    expect(await screen.findAllByText(text.matched)).toHaveLength(2)

    const callsBeforeSubmit = fetchMock.mock.calls.length
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    // The stale notice appears, balances were re-read, and the gap for
    // Tunai now shows against the updated recorded figure - all without a
    // second POST /api/reconciliations.
    await screen.findByText(text.staleNotice)
    await waitFor(() => expect(screen.getByText(text.discrepancy('Rp 30.000'))).toBeInTheDocument())

    const postCallsAfter = fetchMock.mock.calls.filter(([input]) => (input as string).toString().includes('/api/reconciliations')).length
    expect(postCallsAfter).toBe(1)
    const callsAfter = fetchMock.mock.calls.length
    expect(callsAfter).toBeGreaterThan(callsBeforeSubmit)
    expect(balancesCallCount).toBe(2)

    // Not an error screen and not a blank one - her typed figures are still
    // there and she is asked to resolve again, resolution buttons intact.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByLabelText(text.actualLabel('Tunai'))).toHaveValue('Rp 100.000')
    expect(screen.getByRole('button', { name: text.resolutionOptions.entry_added })).toBeInTheDocument()
  })

  // The same `invalid_argument` code covers every ledger validation failure
  // (internal/http/errors.go:53), so a rejected request whose counts are
  // still perfectly current is NOT the stale-count race. Saying it was
  // would send her hunting for a transaction nobody posted.
  it('reports a rejected submit as an error when nothing moved under her', async () => {
    const fetchMock = routedFetch([
      ...stubLoad(),
      {
        match: (m, u) => m === 'POST' && u.includes('/api/reconciliations'),
        handle: () =>
          Promise.resolve(jsonResponse({ error: { code: 'invalid_argument', message: 'invalid fix' } }, 400)),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)

    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)
    await waitForForm()

    await userEvent.type(screen.getByLabelText(text.actualLabel('Tunai')), '100000')
    await userEvent.type(screen.getByLabelText(text.actualLabel('Bank Uji Coba')), '200000')
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    // The balances come back unchanged, so every "matched" count still has
    // a zero difference: the request was the problem, not a race.
    await screen.findByText(copy.common.errors.invalid_argument)
    expect(screen.queryByText(text.staleNotice)).not.toBeInTheDocument()
    // And her counts are still on screen either way. formatIDR puts a
    // U+00A0 after "Rp" and toHaveValue compares the raw value, unnormalized.
    expect(screen.getByLabelText(text.actualLabel('Tunai'))).toHaveValue(`Rp\u00a0100.000`)
  })

  it('keeps her typed fix when the same resolution is tapped twice', async () => {
    vi.stubGlobal('fetch', routedFetch(stubLoad()))
    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)
    await waitForForm()

    await userEvent.type(screen.getByLabelText(text.actualLabel('Tunai')), '70000')
    await userEvent.click(screen.getByRole('button', { name: text.resolutionOptions.entry_added }))

    const noteField = screen.getByLabelText(text.fixNoteLabel)
    await userEvent.type(noteField, 'Beli galon, lupa dicatat')

    // A stray second tap on the option already chosen must not reset the
    // fix back to its gap-derived defaults - these buttons read as toggles
    // and a phone makes a double tap easy.
    await userEvent.click(screen.getByRole('button', { name: text.resolutionOptions.entry_added }))
    expect(noteField).toHaveValue('Beli galon, lupa dicatat')
  })

  // The re-read after a rejected submit can fail too. It must not escape as
  // an unhandled rejection and leave the form re-enabled with no notice -
  // that reads as "my submit worked".
  it('reports the failed submit when the balances re-read also fails', async () => {
    let balancesCalls = 0
    const fetchMock = routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/balances'),
        handle: () => {
          balancesCalls += 1
          return balancesCalls === 1
            ? Promise.resolve(jsonResponse(balancesBody(100_000, 200_000)))
            : Promise.reject(new TypeError('network down'))
        },
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/transactions'), handle: () => Promise.resolve(jsonResponse(transactions)) },
      {
        match: (m: string, u: string) => m === 'POST' && u.includes('/api/reconciliations'),
        handle: () => Promise.resolve(jsonResponse({ error: { code: 'invalid_argument', message: 'stale' } }, 400)),
      },
    ])
    vi.stubGlobal('fetch', fetchMock)

    render(<Reconcile onDone={vi.fn()} onCancel={vi.fn()} />)
    await waitForForm()

    await userEvent.type(screen.getByLabelText(text.actualLabel('Tunai')), '100000')
    await userEvent.type(screen.getByLabelText(text.actualLabel('Bank Uji Coba')), '200000')
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    await screen.findByText(copy.common.errors.invalid_argument)
    expect(screen.queryByText(text.staleNotice)).not.toBeInTheDocument()
    // Re-enabled for another try, not stuck mid-submit.
    expect(screen.getByRole('button', { name: text.submit })).toBeEnabled()
  })

  it('leaves without submitting anything when cancelled', async () => {
    const onCancel = vi.fn()
    vi.stubGlobal('fetch', routedFetch(stubLoad()))
    render(<Reconcile onDone={vi.fn()} onCancel={onCancel} />)

    await userEvent.click(await screen.findByRole('button', { name: text.cancel }))
    expect(onCancel).toHaveBeenCalledTimes(1)
  })
})
