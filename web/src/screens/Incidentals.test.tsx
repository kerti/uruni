import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Incidentals from '@/screens/Incidentals'
import { chooseOption } from '@/test/select'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'

const text = copy.incidentals

afterEach(() => {
  vi.unstubAllGlobals()
})

function money(amount: number): string {
  return formatIDR(amount).replace(/\u00a0/g, ' ')
}

const accounts = [{ id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 }]

interface Envelope {
  purpose_id: number
  occasion: string
  target_amount: number | null
  opened_on: string
  closed_on: string | null
  created_at: number
}

const openEnvelope: Envelope = {
  purpose_id: 1,
  occasion: 'Halal bihalal RT',
  target_amount: 500_000,
  opened_on: '2026-09-01',
  closed_on: null,
  created_at: 1,
}

const closedEnvelope: Envelope = {
  purpose_id: 2,
  occasion: '17 Agustus',
  target_amount: null,
  opened_on: '2026-08-01',
  closed_on: '2026-08-20',
  created_at: 2,
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

type Handler = { match: (method: string, url: string) => boolean; handle: () => Promise<Response> }

/** Routes a stubbed fetch by method + path substring, recording every call */
function routedFetch(handlers: Handler[]) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = (init?.method ?? 'GET').toUpperCase()
    const handler = handlers.find((h) => h.match(method, url))
    if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
    return handler.handle()
  })
}

/** The default GET handler backing every test: the accounts list, fetched
 * once on mount. Envelope list routes are gone with the list view (#263) -
 * this screen is detail-only now, always given a purposeId. */
function getHandlers() {
  return [{ match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) }]
}

/** Exposes the router's search string: the rename dialog is addressed by
 * `?edit=incidental:<id>` (ADR-032), the same pattern PassThrough.test.tsx
 * asserts on directly. */
function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

/** Incidentals.tsx now reads the URL itself (useDialogParam, #264), so every
 * render needs a Router around it - the same requirement PassThrough.tsx's
 * own renderAt satisfies for its screen. */
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
  it('shows an open envelope\'s detail for the given purposeId', async () => {
    const detail = { ...openEnvelope, collected_amount: 0, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())
    expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument()
  })

  it('hands contribution/disbursement off to the real record form, pre-chosen to this envelope', async () => {
    // Contributions and disbursements are not this screen's own form - one
    // action navigates into RecordTransaction.tsx with the envelope's
    // purpose already picked (App.tsx wires onRecordFor to
    // /record?purpose=<id>); direction is that form's own toggle.
    const detail = { ...openEnvelope, collected_amount: 0, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))

    const onRecordFor = vi.fn()
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={onRecordFor} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.record }))
    expect(onRecordFor).toHaveBeenCalledWith(1)
  })

  it('closes an envelope with a zero rollover, shown honestly rather than hidden', async () => {
    // Collected equals disbursed, so the close rolls nothing - and the
    // readout is derived from exactly those two figures (#270), which is why
    // the refetched detail carries the same pair with closed_on set rather
    // than the close response's rolled_amount being remembered.
    const detail = { ...openEnvelope, collected_amount: 120_000, disbursed_amount: 120_000, target_amount: null }
    const closed = { ...openEnvelope, closed_on: '2026-09-10' }
    let detailCalls = 0
    vi.stubGlobal('fetch', routedFetch([
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
    ]))

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())
    expect(screen.getAllByText(money(120_000))).toHaveLength(2)

    // Open the close form and submit it
    await userEvent.click(screen.getByRole('button', { name: text.actions.close }))
    await waitFor(() => expect(screen.getByText(text.close.heading)).toBeInTheDocument())
    await chooseOption(text.close.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.close.submit }))

    // The zero rollover is rendered, not hidden.
    await waitFor(() => expect(screen.getByText(text.close.success)).toBeInTheDocument())
    expect(screen.getByText(text.close.rolledLabel(0))).toBeInTheDocument()
    expect(screen.getByText(money(0))).toBeInTheDocument()
  })

  it('sends the close note to the server, and null when the field was left alone', async () => {
    // The roll is a transfer the treasurer never asks for directly (#210):
    // without a note it lands in the transaction list as two unexplained
    // rows. An untouched field is null, never "".
    const detail = { ...openEnvelope, collected_amount: 120_000, disbursed_amount: 0, target_amount: null }
    const closed = { ...openEnvelope, closed_on: '2026-09-10' }
    let posted: unknown = null
    const closeHandler = (init?: RequestInit) => {
      posted = JSON.parse(String(init?.body))
      return Promise.resolve(jsonResponse({ incidental: closed, rolled_amount: 120_000 }))
    }
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
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
    const detail = { ...openEnvelope, collected_amount: 120_000, disbursed_amount: 0, target_amount: null }
    const closed = { ...openEnvelope, closed_on: '2026-09-10' }
    let posted: unknown = null
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
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
    const detail = { ...openEnvelope, collected_amount: 50_000, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'), handle: () => Promise.resolve(jsonResponse(detail)) },
      {
        match: (m: string, u: string) => m === 'POST' && u.includes('/api/incidentals/1/close'),
        handle: () =>
          Promise.resolve(jsonResponse({ error: { code: 'incidental_already_closed', message: 'already closed' } }, 409)),
      },
      ...getHandlers(),
    ]))

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.close }))
    await waitFor(() => expect(screen.getByText(text.close.heading)).toBeInTheDocument())
    await chooseOption(text.close.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.close.submit }))

    // The named 409's own copy is shown, never the English wire message, and
    // the close form stays open so she can read why.
    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe(text.errors.incidental_already_closed))
    expect(screen.getByText(text.close.heading)).toBeInTheDocument()
  })

  it('a closed envelope shows the reopen affordance instead of record/close', async () => {
    const detail = { ...closedEnvelope, collected_amount: 50_000, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    expect(screen.getByRole('button', { name: text.actions.reopen })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.record })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.close })).not.toBeInTheDocument()
  })

  it('reopening a closed envelope leads straight into the record/close actions of an open one', async () => {
    const closedDetail = { ...closedEnvelope, collected_amount: 50_000, disbursed_amount: 0 }
    const reopened = { ...closedEnvelope, closed_on: null }
    const reopenedDetail = { ...reopened, collected_amount: 50_000, disbursed_amount: 0 }
    let detailCalls = 0
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
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
    // Reopened - the same record/close actions any open envelope shows, not
    // a bare toggle with nothing next.
    expect(screen.getByRole('button', { name: text.actions.record })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.actions.close })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.reopen })).not.toBeInTheDocument()
  })

  it('calls onBack, now leading to Pengaturan, when backToSettings is clicked', async () => {
    const detail = { ...openEnvelope, collected_amount: 0, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))
    const onBack = vi.fn()
    renderIncidentals(<Incidentals onBack={onBack} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)

    await userEvent.click(await screen.findByRole('button', { name: text.detail.backToSettings }))
    expect(onBack).toHaveBeenCalledTimes(1)
  })


  // #262: a closed envelope is off Beranda (ADR-032), so this link is the
  // only route it has to its own record - which is why the button is here
  // for a closed envelope, not only an open one.
  it('links a closed envelope to its own transactions, the only route it has', async () => {
    const detail = { ...closedEnvelope, collected_amount: 200_000, disbursed_amount: 200_000 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))

    const onViewTransactionsFor = vi.fn()
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={onViewTransactionsFor} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.viewTransactions }))
    expect(onViewTransactionsFor).toHaveBeenCalledWith(2)
  })

  // --- The rollover, on every visit (#270) --------------------------------
  //
  // The readout used to live in component state set by the close response,
  // so it survived exactly one screen-lifetime: a treasurer returning to a
  // closed envelope saw Terkumpul and Terpakai against a zero balance and
  // no sentence reconciling them. It is derived from those same two figures
  // now, so every one of these opens the screen cold - no close is
  // performed anywhere below.

  it('states a closed envelope\'s rollover on a cold visit, with no close in sight', async () => {
    const detail = { ...closedEnvelope, collected_amount: 10_000, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    // The exact walk #270 reported: 10.000 collected, nothing spent, and an
    // envelope holding nothing - now with the sentence that explains it.
    expect(screen.getByText(text.close.rolledLabel(10_000))).toBeInTheDocument()
    expect(screen.getAllByText(money(10_000))).toHaveLength(2)
  })

  it('states a shortfall covered from Kas Utama, in its own direction', async () => {
    const detail = { ...closedEnvelope, collected_amount: 50_000, disbursed_amount: 80_000 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    // Signed, so the sentence changes rather than the amount's sign showing
    // (ADR-031); the figure itself stays absolute.
    expect(screen.getByText(text.close.rolledLabel(-30_000))).toBeInTheDocument()
    expect(screen.getByText(money(30_000))).toBeInTheDocument()
  })

  it('states a square envelope as square, rather than saying nothing', async () => {
    const detail = { ...closedEnvelope, collected_amount: 75_000, disbursed_amount: 75_000 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    expect(screen.getByText(text.close.rolledLabel(0))).toBeInTheDocument()
    expect(screen.getByText(money(0))).toBeInTheDocument()
  })

  it('says nothing about a rollover on an envelope that is still open', async () => {
    const detail = { ...openEnvelope, collected_amount: 120_000, disbursed_amount: 20_000 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    // An open envelope has rolled nothing; 100.000 is simply what it holds.
    expect(screen.queryByText(text.close.rolledLabel(100_000))).not.toBeInTheDocument()
  })

  it('a reopened envelope with a past roll does not claim to have rolled', async () => {
    // Reopening does not reverse the roll, so the ledger still carries it -
    // but the envelope is open again and must not read as finished.
    const closedDetail = { ...closedEnvelope, collected_amount: 50_000, disbursed_amount: 0 }
    const reopened = { ...closedEnvelope, closed_on: null }
    const reopenedDetail = { ...reopened, collected_amount: 50_000, disbursed_amount: 0 }
    let detailCalls = 0
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'POST' && u.includes('/api/incidentals/2/reopen'), handle: () => Promise.resolve(jsonResponse(reopened)) },
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'),
        handle: () => {
          detailCalls += 1
          return Promise.resolve(jsonResponse(detailCalls === 1 ? closedDetail : reopenedDetail))
        },
      },
      ...getHandlers(),
    ]))

    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.close.rolledLabel(50_000))).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.reopen }))

    await waitFor(() => expect(screen.getByText(text.reopen.success)).toBeInTheDocument())
    expect(screen.queryByText(text.close.rolledLabel(50_000))).not.toBeInTheDocument()
  })

  // --- Rename (#264): correcting a mistyped occasion ---------------------

  it('opens the rename dialog, addressed by ?edit=incidental:<id>, when Ubah nama is clicked', async () => {
    const detail = { ...openEnvelope, collected_amount: 0, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={1} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.rename }))
    expect(currentSearch()).toBe('?edit=incidental%3A1')

    const dialog = await screen.findByRole('dialog', { name: text.rename.heading })
    expect(within(dialog).getByLabelText(text.rename.nameLabel)).toHaveValue('Halal bihalal RT')
  })

  it('submits a rename, refetches the detail, and shows the corrected occasion through the screen\'s own feedback banner', async () => {
    const detail = { ...openEnvelope, collected_amount: 0, disbursed_amount: 0 }
    const renamedPurpose = { id: 1, kind: 'incidental', name: 'Halal bihalal RT 2026', created_at: 1 }
    const renamedDetail = { ...detail, occasion: 'Halal bihalal RT 2026' }
    let detailCalls = 0
    let patched: { method: string; url: string; body: unknown } | null = null
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
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
    // off the server (never the dialog's own local state), and the success
    // message reaches the treasurer through the screen's existing Feedback
    // banner - the same one close/reopen already use, not a second one.
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.getByRole('heading', { name: 'Halal bihalal RT 2026' })).toBeInTheDocument()
    expect(screen.getByText(text.rename.success)).toBeInTheDocument()
  })

  // The whole point of #264: the typo is usually noticed after the occasion
  // is over, so a closed envelope must offer the same correction.
  it('offers the rename button for a closed envelope too', async () => {
    const detail = { ...closedEnvelope, collected_amount: 50_000, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/2'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))
    renderIncidentals(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} onViewTransactionsFor={vi.fn()} purposeId={2} />)
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    expect(screen.getByRole('button', { name: text.actions.rename })).toBeInTheDocument()
  })
})
