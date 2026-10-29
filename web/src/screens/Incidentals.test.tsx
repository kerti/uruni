import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Incidentals from '@/screens/Incidentals'
import { chooseOption } from '@/test/select'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { Incidental, IncidentalDetail, IncidentalParticipation, Participant } from '@/lib/incidentals'
import type { Purpose } from '@/lib/purposes'
import type { Account, Member, Transaction } from '@/lib/setup'

const text = copy.incidentals

afterEach(() => {
  vi.unstubAllGlobals()
})

const accounts = [{ id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 }] satisfies Account[]

const openEnvelope = {
  purpose_id: 1,
  occasion: 'Halal bihalal RT',
  target_amount: 500_000,
  opened_on: '2026-09-01',
  closed_on: null,
  created_at: 1,
  minimum_per_member: null,
} satisfies Incidental

const closedEnvelope = {
  purpose_id: 2,
  occasion: '17 Agustus',
  target_amount: null,
  opened_on: '2026-08-01',
  closed_on: '2026-08-20',
  created_at: 2,
  minimum_per_member: null,
} satisfies Incidental

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

type Handler = { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }

/**
 * Routes a stubbed fetch by method + path substring, recording every call.
 * `participation`, when given, answers every GET .../participation request -
 * checked before `handlers`, never through it: a GET .../participation is a
 * suffix of the path most tests' detail handler matches by
 * `u.includes('/api/incidentals/<id>')`. Omitted, participation answers empty
 * (no expected members, no unexpected ones), which is what every test not
 * about the participation table wants.
 */
function routedFetch(handlers: Handler[], participation?: () => Promise<Response>) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()

    if (method === 'GET' && url.includes('/participation')) {
      return participation ? participation() : Promise.resolve(jsonResponse({ expected: [], unexpected: [] }))
    }

    const handler = handlers.find((h) => h.match(method, url))
    if (handler) return handler.handle()
    // The envelope's recent activity fetches on every render of the detail;
    // tests about something else get an empty list.
    if (method === 'GET' && url.includes('/api/transactions')) return Promise.resolve(jsonResponse({ transactions: [], next_cursor: null }))
    // The roster (the edit dialog's recipients picker) - tests about
    // something else get an empty roster.
    if (method === 'GET' && url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
    return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
  })
}

/** The default GET handler backing every test: the accounts list, fetched once
 * on mount. This screen is detail-only, always given a purposeId. */
function getHandlers() {
  return [
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
  ]
}

/** Exposes the router's search string: the rename dialog is addressed by
 * `?edit=incidental:<id>` (ADR-032), as PassThrough.test.tsx asserts. */
function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

/** Incidentals.tsx reads the URL itself (useDialogParam), so every render
 * needs a Router around it. */
function renderIncidentals(node: Parameters<typeof render>[0], entry = '/incidentals') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/incidentals"
          element={
            <>
              {node}
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

describe('Incidentals', () => {
  it("shows an open envelope's detail for the given purposeId", async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())
    expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument()
  })

  // The info card reads target, collected, used, remaining, in that order, and
  // Sisa is the server's balance - not collected minus used, which a reopened
  // envelope's earlier roll makes wrong (here 300k - 120k would read 180k,
  // while the envelope holds 30k after its first roll).
  it('shows target, collected, used and remaining in order, remaining from the balance', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 300_000,
      disbursed_amount: 120_000,
      balance_amount: 30_000,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.remainingLabel)).toBeInTheDocument())

    const labels = [text.detail.targetLabel, text.detail.collectedLabel, text.detail.disbursedLabel, text.detail.remainingLabel]
    const rows = labels.map((label) => screen.getByText(label, { exact: true }).parentElement as HTMLElement)
    for (let i = 1; i < rows.length; i++) {
      expect(rows[i - 1].compareDocumentPosition(rows[i]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    }
    expect(rows[0]).toHaveTextContent(formatIDR(500_000))
    expect(rows[1]).toHaveTextContent(formatIDR(300_000))
    expect(rows[2]).toHaveTextContent(formatIDR(120_000))
    expect(rows[3]).toHaveTextContent(formatIDR(30_000))
  })

  it('leaves the target row out when the envelope has none', async () => {
    const detail = {
      ...openEnvelope,
      target_amount: null,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.remainingLabel)).toBeInTheDocument())
    expect(screen.queryByText(text.detail.targetLabel, { exact: true })).not.toBeInTheDocument()
  })

  it('hands contribution/disbursement off to the real record form, pre-chosen to this envelope', async () => {
    // Contributions and disbursements are not this screen's form - one action
    // navigates into RecordTransaction.tsx with the envelope's purpose picked
    // (App.tsx wires onRecordFor to /record?purpose=<id>).
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )

    const onRecordFor = vi.fn()
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={onRecordFor} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.record }))
    // Called with no member: the top action row records without one prefilled
    // - only the participation table's per-row action names a member.
    expect(onRecordFor).toHaveBeenCalledWith(1, undefined)
  })

  it('closes an envelope with a zero rollover, shown honestly rather than hidden', async () => {
    // Collected equals disbursed, so the close rolls nothing - and the readout
    // is derived from those two figures, which is why the refetched detail
    // carries the same pair with closed_on set rather than the close response's
    // rolled_amount being remembered.
    const detail = {
      ...openEnvelope,
      collected_amount: 120_000,
      disbursed_amount: 120_000,
      balance_amount: 0,
      target_amount: null,
      recipients: [],
    } satisfies IncidentalDetail
    const closed = { ...openEnvelope, closed_on: '2026-09-10' } satisfies Incidental
    let detailCalls = 0
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => {
            detailCalls += 1
            return Promise.resolve(jsonResponse(detailCalls === 1 ? detail : { ...detail, closed_on: '2026-09-10' }))
          },
        },
        {
          match: (m: string, u: string) => m === 'POST' && u.includes('/api/incidentals/1/close'),
          handle: () => Promise.resolve(jsonResponse({ incidental: closed, rolled_amount: 0 })),
        },
        ...getHandlers(),
      ]),
    )

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())
    expect(screen.getAllByText(formatIDR(120_000))).toHaveLength(2)

    // Open the close form and submit it
    await userEvent.click(screen.getByRole('button', { name: text.actions.close }))
    await waitFor(() => expect(screen.getByText(text.close.heading)).toBeInTheDocument())
    await chooseOption(text.close.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.close.submit }))

    // The zero rollover is rendered, not hidden.
    await waitFor(() => expect(screen.getByText(text.close.success)).toBeInTheDocument())
    // Anchored to the rollover row: the card's Sisa also reads Rp0 here.
    expect(screen.getByText(text.close.rolledLabel(0)).parentElement).toHaveTextContent(formatIDR(0))
  })

  it('sends the close note to the server, and null when the field was left alone', async () => {
    // The roll is a transfer the treasurer never asks for directly: without a
    // note it lands in the transaction list as two unexplained rows. An
    // untouched field is null, never "".
    const detail = {
      ...openEnvelope,
      collected_amount: 120_000,
      disbursed_amount: 0,
      balance_amount: 120000,
      target_amount: null,
      recipients: [],
    } satisfies IncidentalDetail
    const closed = { ...openEnvelope, closed_on: '2026-09-10' } satisfies Incidental
    let posted: unknown = null
    const closeHandler = (init?: RequestInit) => {
      posted = JSON.parse(String(init?.body))
      return Promise.resolve(jsonResponse({ incidental: closed, rolled_amount: 120_000 }))
    }
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      // Checked before the generic '/api/incidentals/1' branch below - a
      // participation GET is a suffix of that same path.
      if (method === 'GET' && url.includes('/participation')) return Promise.resolve(jsonResponse({ expected: [], unexpected: [] }))
      if (method === 'GET' && url.includes('/api/transactions'))
        return Promise.resolve(jsonResponse({ transactions: [], next_cursor: null }))
      if (method === 'GET' && url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
      if (method === 'POST' && url.includes('/api/incidentals/1/close')) return closeHandler(init)
      if (method === 'GET' && url.includes('/api/incidentals/1')) return Promise.resolve(jsonResponse(detail))
      const handler = getHandlers().find((h) => h.match(method, url))
      if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      return handler.handle()
    })
    vi.stubGlobal('fetch', fetchMock)

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.close }))
    await waitFor(() => expect(screen.getByText(text.close.heading)).toBeInTheDocument())
    await chooseOption(text.close.accountLabel, 'Tunai')
    await userEvent.type(screen.getByLabelText(text.close.noteLabel), 'Sisa halal bihalal')
    await userEvent.click(screen.getByRole('button', { name: text.close.submit }))

    await waitFor(() => expect(screen.getByText(text.close.success)).toBeInTheDocument())
    expect(posted).toMatchObject({ note: 'Sisa halal bihalal' })
  })

  it('sends a null note when the close note was left empty', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 120_000,
      disbursed_amount: 0,
      balance_amount: 120000,
      target_amount: null,
      recipients: [],
    } satisfies IncidentalDetail
    const closed = { ...openEnvelope, closed_on: '2026-09-10' } satisfies Incidental
    let posted: unknown = null
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      // Checked before the generic '/api/incidentals/1' branch below - a
      // participation GET is a suffix of that same path.
      if (method === 'GET' && url.includes('/participation')) return Promise.resolve(jsonResponse({ expected: [], unexpected: [] }))
      if (method === 'GET' && url.includes('/api/transactions'))
        return Promise.resolve(jsonResponse({ transactions: [], next_cursor: null }))
      if (method === 'GET' && url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
      if (method === 'POST' && url.includes('/api/incidentals/1/close')) {
        posted = JSON.parse(String(init?.body))
        return Promise.resolve(jsonResponse({ incidental: closed, rolled_amount: 120_000 }))
      }
      if (method === 'GET' && url.includes('/api/incidentals/1')) return Promise.resolve(jsonResponse(detail))
      const handler = getHandlers().find((h) => h.match(method, url))
      if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      return handler.handle()
    })
    vi.stubGlobal('fetch', fetchMock)

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.close }))
    await waitFor(() => expect(screen.getByText(text.close.heading)).toBeInTheDocument())
    await chooseOption(text.close.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.close.submit }))

    await waitFor(() => expect(screen.getByText(text.close.success)).toBeInTheDocument())
    expect(posted).toMatchObject({ note: null })
  })

  it('a second close attempt surfaces the named 409 refusal', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 50_000,
      disbursed_amount: 0,
      balance_amount: 50000,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        {
          match: (m: string, u: string) => m === 'POST' && u.includes('/api/incidentals/1/close'),
          handle: () => Promise.resolve(jsonResponse({ error: { code: 'incidental_already_closed', message: 'already closed' } }, 409)),
        },
        ...getHandlers(),
      ]),
    )

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.close }))
    await waitFor(() => expect(screen.getByText(text.close.heading)).toBeInTheDocument())
    await chooseOption(text.close.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.close.submit }))

    // The named 409's copy is shown, never the English wire message, and the
    // close form stays open so she can read why.
    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe(text.errors.incidental_already_closed))
    expect(screen.getByText(text.close.heading)).toBeInTheDocument()
  })

  it('a closed envelope shows the reopen affordance instead of record/close', async () => {
    const detail = {
      ...closedEnvelope,
      collected_amount: 50_000,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    expect(screen.getByRole('button', { name: text.actions.reopen })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.record })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.close })).not.toBeInTheDocument()
  })

  it('reopening a closed envelope leads straight into the record/close actions of an open one', async () => {
    const closedDetail = {
      ...closedEnvelope,
      collected_amount: 50_000,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    const reopened = { ...closedEnvelope, closed_on: null } satisfies Incidental
    const reopenedDetail = {
      ...reopened,
      collected_amount: 50_000,
      disbursed_amount: 0,
      balance_amount: 50000,
      recipients: [],
    } satisfies IncidentalDetail
    let detailCalls = 0
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      // Checked before the generic '/api/incidentals/2' branch below - a
      // participation GET is a suffix of that same path.
      if (method === 'GET' && url.includes('/participation')) return Promise.resolve(jsonResponse({ expected: [], unexpected: [] }))
      if (method === 'GET' && url.includes('/api/transactions'))
        return Promise.resolve(jsonResponse({ transactions: [], next_cursor: null }))
      if (method === 'GET' && url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
      if (method === 'POST' && url.includes('/api/incidentals/2/reopen')) {
        return Promise.resolve(jsonResponse(reopened))
      }
      if (method === 'GET' && url.includes('/api/incidentals/2')) {
        detailCalls += 1
        return Promise.resolve(jsonResponse(detailCalls === 1 ? closedDetail : reopenedDetail))
      }
      const handler = getHandlers().find((h) => h.match(method, url))
      if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      return handler.handle()
    })
    vi.stubGlobal('fetch', fetchMock)

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByRole('button', { name: text.actions.reopen })).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.reopen }))

    await waitFor(() => expect(screen.getByText(text.reopen.success)).toBeInTheDocument())
    // Reopened - the record/close actions any open envelope shows, not a bare
    // toggle with nothing next.
    expect(screen.getByRole('button', { name: text.actions.record })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.actions.close })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.reopen })).not.toBeInTheDocument()
  })

  it('calls onBack, now leading to Pengaturan, when backToSettings is clicked', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )
    const onBack = vi.fn()
    renderIncidentals(<Incidentals onBack={onBack} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    await userEvent.click(await screen.findByRole('button', { name: text.detail.backToSettings }))
    expect(onBack).toHaveBeenCalledTimes(1)
  })

  // A closed envelope is off Beranda (ADR-032), so this link is its only route
  // to its record - which is why the button is here for a closed envelope, not
  // only an open one.
  it('links a closed envelope to its own transactions, the only route it has', async () => {
    const detail = {
      ...closedEnvelope,
      collected_amount: 200_000,
      disbursed_amount: 200_000,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )

    const onViewTransactionsFor = vi.fn()
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={onViewTransactionsFor} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: copy.home.recentActivityViewAll }))
    expect(onViewTransactionsFor).toHaveBeenCalledWith(2)
  })

  // The envelope's recent activity, asked for by purpose - the filter "Lihat
  // semua" leads to - so the rows below the actions are this envelope's alone.
  it("lists the envelope's recent transactions, fetched by its purpose", async () => {
    const row = {
      id: 7,
      account_id: 1,
      purpose_id: 2,
      direction: 'in',
      amount: 75_000,
      occurred_on: '2026-09-02',
      kind: 'normal',
      member_id: null,
      dues_period: null,
      reimbursement_id: null,
      transfer_id: null,
      reverses_transaction_id: null,
      note: 'Iuran halal bihalal',
      created_at: 7,
    } satisfies Transaction
    const fetchMock = routedFetch([
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
        handle: () => Promise.resolve(jsonResponse(closedEnvelope)),
      },
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/transactions'),
        handle: () => Promise.resolve(jsonResponse({ transactions: [row], next_cursor: null })),
      },
      ...getHandlers(),
    ])
    vi.stubGlobal('fetch', fetchMock)

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)

    expect(await screen.findByText('Iuran halal bihalal')).toBeInTheDocument()
    expect(screen.getByText(copy.home.recentActivityHeading)).toBeInTheDocument()
    const urls = fetchMock.mock.calls.map(([input]) => String(input))
    expect(urls.some((u) => u.includes('/api/transactions') && u.includes('purpose_id=2'))).toBe(true)
  })

  // --- The rollover, on every visit ----------------------------------------
  //
  // The readout is derived from the two figures on the detail, not kept in
  // state set by the close response, so a treasurer returning to a closed
  // envelope sees a sentence reconciling Terkumpul and Terpakai against a zero
  // balance. Every test here opens the screen cold - no close is performed.

  it("states a closed envelope's rollover on a cold visit, with no close in sight", async () => {
    const detail = {
      ...closedEnvelope,
      collected_amount: 10_000,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    // 10.000 collected, nothing spent, an envelope holding nothing - with the
    // sentence that explains it.
    expect(screen.getByText(text.close.rolledLabel(10_000))).toBeInTheDocument()
    expect(screen.getAllByText(formatIDR(10_000))).toHaveLength(2)
  })

  it('states a shortfall covered from Kas Utama, in its own direction', async () => {
    const detail = {
      ...closedEnvelope,
      collected_amount: 50_000,
      disbursed_amount: 80_000,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    // Signed, so the sentence changes rather than the amount's sign showing
    // (ADR-031); the figure itself stays absolute.
    expect(screen.getByText(text.close.rolledLabel(-30_000))).toBeInTheDocument()
    expect(screen.getByText(formatIDR(30_000))).toBeInTheDocument()
  })

  it('states a square envelope as square, rather than saying nothing', async () => {
    const detail = {
      ...closedEnvelope,
      collected_amount: 75_000,
      disbursed_amount: 75_000,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    // Anchored to the rollover row: the card's Sisa also reads Rp0 here.
    expect(screen.getByText(text.close.rolledLabel(0)).parentElement).toHaveTextContent(formatIDR(0))
  })

  it('says nothing about a rollover on an envelope that is still open', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 120_000,
      disbursed_amount: 20_000,
      balance_amount: 100000,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    // An open envelope has rolled nothing; 100.000 is simply what it holds.
    expect(screen.queryByText(text.close.rolledLabel(100_000))).not.toBeInTheDocument()
  })

  it('a reopened envelope with a past roll does not claim to have rolled', async () => {
    // Reopening does not reverse the roll, so the ledger still carries it - but
    // the envelope is open again and must not read as finished.
    const closedDetail = {
      ...closedEnvelope,
      collected_amount: 50_000,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    const reopened = { ...closedEnvelope, closed_on: null } satisfies Incidental
    const reopenedDetail = {
      ...reopened,
      collected_amount: 50_000,
      disbursed_amount: 0,
      balance_amount: 50000,
      recipients: [],
    } satisfies IncidentalDetail
    let detailCalls = 0
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'POST' && u.includes('/api/incidentals/2/reopen'),
          handle: () => Promise.resolve(jsonResponse(reopened)),
        },
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
          handle: () => {
            detailCalls += 1
            return Promise.resolve(jsonResponse(detailCalls === 1 ? closedDetail : reopenedDetail))
          },
        },
        ...getHandlers(),
      ]),
    )

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.close.rolledLabel(50_000))).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.reopen }))

    await waitFor(() => expect(screen.getByText(text.reopen.success)).toBeInTheDocument())
    expect(screen.queryByText(text.close.rolledLabel(50_000))).not.toBeInTheDocument()
  })

  // --- Rename: correcting a mistyped occasion ------------------------------

  it('opens the rename dialog, addressed by ?edit=incidental:<id>, when Ubah nama is clicked', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.rename }))
    expect(currentSearch()).toBe('?edit=incidental%3A1')

    const dialog = await screen.findByRole('dialog', { name: text.rename.heading })
    expect(within(dialog).getByLabelText(text.rename.nameLabel)).toHaveValue('Halal bihalal RT')
  })

  it("submits a rename, refetches the detail, and shows the corrected occasion through the screen's own feedback banner", async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    const renamedPurpose = { id: 1, kind: 'incidental', name: 'Halal bihalal RT 2026', created_at: 1 } satisfies Purpose
    const renamedDetail = { ...detail, occasion: 'Halal bihalal RT 2026' } satisfies IncidentalDetail
    let detailCalls = 0
    let patched: { method: string; url: string; body: unknown } | null = null
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      // Checked before the generic '/api/incidentals/1' branch below - a
      // participation GET is a suffix of that same path.
      if (method === 'GET' && url.includes('/participation')) return Promise.resolve(jsonResponse({ expected: [], unexpected: [] }))
      if (method === 'GET' && url.includes('/api/transactions'))
        return Promise.resolve(jsonResponse({ transactions: [], next_cursor: null }))
      if (method === 'GET' && url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
      if (method === 'PATCH' && url.includes('/api/purposes/1')) {
        patched = { method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined }
        return Promise.resolve(jsonResponse(renamedPurpose))
      }
      if (method === 'GET' && url.includes('/api/incidentals/1')) {
        detailCalls += 1
        return Promise.resolve(jsonResponse(detailCalls === 1 ? detail : renamedDetail))
      }
      const handler = getHandlers().find((h) => h.match(method, url))
      if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      return handler.handle()
    })
    vi.stubGlobal('fetch', fetchMock)

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.rename }))
    const dialog = await screen.findByRole('dialog', { name: text.rename.heading })
    const input = within(dialog).getByLabelText(text.rename.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Halal bihalal RT 2026')
    await userEvent.click(within(dialog).getByRole('button', { name: text.rename.save }))

    await waitFor(() => expect(patched).not.toBeNull())
    expect(patched).toMatchObject({
      method: 'PATCH',
      url: expect.stringContaining('/api/purposes/1'),
      body: { name: 'Halal bihalal RT 2026' },
    })

    // The dialog closes, the <h1> shows the corrected occasion fetched fresh
    // off the server (never the dialog's local state), and the success message
    // goes through the screen's Feedback banner - the one close/reopen use.
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByRole('heading', { name: 'Halal bihalal RT 2026' })).toBeInTheDocument()
    expect(screen.getByText(text.rename.success)).toBeInTheDocument()
  })

  // The same edit dialog carries the minimum and the recipients (ADR-034),
  // seeded from the envelope and PATCHed to /api/incidentals/{id} when either
  // changes - occasion untouched, so renamePurpose is never called.
  it('edits the minimum and recipients through the same dialog, without touching the occasion', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      minimum_per_member: null,
      recipients: [],
    } satisfies IncidentalDetail
    const updated = { ...openEnvelope, minimum_per_member: 25_000 } satisfies Incidental
    let detailCalls = 0
    let patched: { body: unknown } | null = null
    const budi = member({ id: 7, name: 'Budi' })
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      if (method === 'GET' && url.includes('/participation')) return Promise.resolve(jsonResponse({ expected: [], unexpected: [] }))
      if (method === 'GET' && url.includes('/api/transactions'))
        return Promise.resolve(jsonResponse({ transactions: [], next_cursor: null }))
      if (method === 'GET' && url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [budi], next_cursor: null }))
      if (method === 'PATCH' && url.includes('/api/incidentals/1')) {
        patched = { body: init?.body ? JSON.parse(String(init.body)) : undefined }
        return Promise.resolve(jsonResponse(updated))
      }
      if (method === 'GET' && url.includes('/api/incidentals/1')) {
        detailCalls += 1
        return Promise.resolve(jsonResponse(detailCalls === 1 ? detail : { ...detail, minimum_per_member: 25_000 }))
      }
      const handler = getHandlers().find((h) => h.match(method, url))
      if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      return handler.handle()
    })
    vi.stubGlobal('fetch', fetchMock)

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.rename }))
    const dialog = await screen.findByRole('dialog', { name: text.rename.heading })
    await userEvent.type(within(dialog).getByLabelText(copy.incidentals.open.minimumLabel), '25000')
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'Budi' }))
    await userEvent.click(within(dialog).getByRole('button', { name: text.rename.save }))

    await waitFor(() => expect(patched).not.toBeNull())
    // The PATCH replaces every facet, so the untouched target rides along
    // unchanged rather than being cleared.
    expect(patched).toMatchObject({ body: { target_amount: 500_000, minimum_per_member: 25_000, recipient_member_ids: [7] } })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByText(text.rename.success)).toBeInTheDocument()
  })

  // The target is an expectation like the minimum, corrected in the same
  // dialog - and clearing the field clears the target.
  it.each([
    ['changes', '750000', 750_000],
    ['clears', '', null],
  ])('%s the target through the same dialog', async (_label, typed, sent) => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      minimum_per_member: null,
      recipients: [],
    } satisfies IncidentalDetail
    let patched: { body: unknown } | null = null
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      if (method === 'GET' && url.includes('/participation')) return Promise.resolve(jsonResponse({ expected: [], unexpected: [] }))
      if (method === 'GET' && url.includes('/api/transactions'))
        return Promise.resolve(jsonResponse({ transactions: [], next_cursor: null }))
      if (method === 'GET' && url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [], next_cursor: null }))
      if (method === 'PATCH' && url.includes('/api/incidentals/1')) {
        patched = { body: init?.body ? JSON.parse(String(init.body)) : undefined }
        return Promise.resolve(jsonResponse({ ...openEnvelope, target_amount: sent }))
      }
      if (method === 'GET' && url.includes('/api/incidentals/1')) return Promise.resolve(jsonResponse(detail))
      const handler = getHandlers().find((h) => h.match(method, url))
      if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      return handler.handle()
    })
    vi.stubGlobal('fetch', fetchMock)

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.rename }))
    const dialog = await screen.findByRole('dialog', { name: text.rename.heading })
    const field = within(dialog).getByLabelText(copy.incidentals.open.targetLabel)
    await userEvent.clear(field)
    if (typed !== '') await userEvent.type(field, typed)
    await userEvent.click(within(dialog).getByRole('button', { name: text.rename.save }))

    await waitFor(() => expect(patched).not.toBeNull())
    expect(patched).toMatchObject({ body: { target_amount: sent, minimum_per_member: null, recipient_member_ids: [] } })
  })

  // The typo is usually noticed after the occasion is over, so a closed
  // envelope must offer the same correction.
  it('offers the rename button for a closed envelope too', async () => {
    const detail = {
      ...closedEnvelope,
      collected_amount: 50_000,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    vi.stubGlobal(
      'fetch',
      routedFetch([
        {
          match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
          handle: () => Promise.resolve(jsonResponse(detail)),
        },
        ...getHandlers(),
      ]),
    )
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    expect(screen.getByRole('button', { name: text.actions.rename })).toBeInTheDocument()
  })
})

function member(overrides: Partial<{ id: number; name: string; inactive_on: string | null }> = {}): Member {
  return {
    id: 1,
    name: 'Budi',
    tier_id: null,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
    ...overrides,
  }
}

/** What the participation route sends for a member: no tier, rate or arrears. */
function participant(overrides: Partial<{ id: number; name: string; inactive_on: string | null }> = {}) {
  const { id, name, joined_on, inactive_on, created_at } = member(overrides)
  return { id, name, joined_on, inactive_on, created_at } satisfies Participant
}

describe('Incidentals participation table (ADR-034, #211, #333)', () => {
  const participationText = copy.incidentals.participation

  function stub(detail: IncidentalDetail, participation: IncidentalParticipation) {
    vi.stubGlobal(
      'fetch',
      routedFetch(
        [
          {
            match: (m: string, u: string) => m === 'GET' && u.includes(`/api/incidentals/${detail.purpose_id}`),
            handle: () => Promise.resolve(jsonResponse(detail)),
          },
          ...getHandlers(),
        ],
        () => Promise.resolve(jsonResponse(participation)),
      ),
    )
  }

  it('shows Sudah menyumbang with the amount, Belum menyumbang with none, and Kurang dari minimal for a partial gift', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 150_000,
      disbursed_amount: 0,
      balance_amount: 150000,
      minimum_per_member: 50_000,
      recipients: [],
    } satisfies IncidentalDetail
    stub(detail, {
      expected: [
        { member: participant({ id: 1, name: 'Budi' }), contributed_amount: 100_000, state: 'sudah' },
        { member: participant({ id: 2, name: 'Sri' }), contributed_amount: 0, state: 'belum' },
        { member: participant({ id: 3, name: 'Ani' }), contributed_amount: 20_000, state: 'kurang' },
      ],
      unexpected: [],
    })

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    expect(await screen.findByText('Budi')).toBeInTheDocument()
    expect(screen.getByText(participationText.states.given)).toBeInTheDocument()
    expect(screen.getByText(formatIDR(100_000))).toBeInTheDocument()

    expect(screen.getByText('Sri')).toBeInTheDocument()
    expect(screen.getByText(participationText.states.notGiven)).toBeInTheDocument()

    expect(screen.getByText('Ani')).toBeInTheDocument()
    expect(screen.getByText(participationText.states.underMinimum)).toBeInTheDocument()
    expect(screen.getByText(formatIDR(20_000))).toBeInTheDocument()
  })

  it('renders "Kurang dari minimal" in neutral ink, never terracotta (Design-System.md)', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 20_000,
      disbursed_amount: 0,
      balance_amount: 20000,
      minimum_per_member: 50_000,
      recipients: [],
    } satisfies IncidentalDetail
    stub(detail, { expected: [{ member: participant(), contributed_amount: 20_000, state: 'kurang' }], unexpected: [] })

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    const label = await screen.findByText(participationText.states.underMinimum)
    expect(label.className).not.toContain('attention')
  })

  it('lists an unexpected giver under Sumbangan lain, with their amount and no state', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 999_000,
      disbursed_amount: 0,
      balance_amount: 999000,
      recipients: [],
    } satisfies IncidentalDetail
    stub(detail, { expected: [], unexpected: [{ member: participant({ id: 5, name: 'Tante Wati' }), contributed_amount: 30_000 }] })

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    expect(await screen.findByText(participationText.otherHeading)).toBeInTheDocument()
    expect(screen.getByText('Tante Wati')).toBeInTheDocument()
    expect(screen.getByText(formatIDR(30_000))).toBeInTheDocument()
  })

  it('shows the recipients as "Untuk: ..."', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [
        { member_id: 9, member_name: 'Keluarga Pak Joko' },
        { member_id: 10, member_name: 'Bu Joko' },
      ],
    } satisfies IncidentalDetail
    stub(detail, { expected: [], unexpected: [] })

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    expect(await screen.findByText(participationText.recipientsLine('Keluarga Pak Joko, Bu Joko'))).toBeInTheDocument()
  })

  it('records a contribution with the member prefilled - one action per expected row', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    stub(detail, { expected: [{ member: participant({ id: 7, name: 'Budi' }), contributed_amount: 0, state: 'belum' }], unexpected: [] })

    const onRecordFor = vi.fn()
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={onRecordFor} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    await userEvent.click(await screen.findByRole('button', { name: participationText.recordAria('Budi') }))

    expect(onRecordFor).toHaveBeenCalledWith(1, 7)
  })

  it('offers no reminder, share or message action anywhere on this screen (PRD sections 4 and 7.5)', async () => {
    const detail = {
      ...openEnvelope,
      collected_amount: 0,
      disbursed_amount: 0,
      balance_amount: 0,
      recipients: [],
    } satisfies IncidentalDetail
    stub(detail, { expected: [{ member: participant({ id: 7, name: 'Budi' }), contributed_amount: 0, state: 'belum' }], unexpected: [] })

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await screen.findByText('Budi')

    for (const button of screen.getAllByRole('button')) {
      expect(button.textContent?.toLowerCase()).not.toMatch(/ingat|kirim|pesan|reminder|share/)
    }
  })
})
