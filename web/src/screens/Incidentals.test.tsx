import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

/** The default GET handlers backing every test: the envelope list
 * (open vs all variants) plus accounts, fetched once on mount. */
function getHandlers(opts: { open?: Envelope[]; all?: Envelope[] } = {}) {
  const open = opts.open ?? [openEnvelope]
  const all = opts.all ?? [openEnvelope, closedEnvelope]
  return [
    {
      match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals') && u.includes('open=true'),
      handle: () => Promise.resolve(jsonResponse(open)),
    },
    {
      match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals') && !u.includes('open=') && !u.includes('/api/incidentals/'),
      handle: () => Promise.resolve(jsonResponse(all)),
    },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
  ]
}

describe('Incidentals', () => {
  it('shows open envelopes by default', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)

    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())
    expect(screen.queryByText('17 Agustus')).not.toBeInTheDocument()
  })

  it('the all tab shows a closed envelope the open tab filters out', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)

    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: text.allTab }))

    await waitFor(() => expect(screen.getByText('17 Agustus')).toBeInTheDocument())
    expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument()
    expect(screen.getByText(text.status.closed, { selector: 'span' })).toBeInTheDocument()
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
    render(<Incidentals onBack={vi.fn()} onRecordFor={onRecordFor} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: /Halal bihalal RT/ }))
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.record }))
    expect(onRecordFor).toHaveBeenCalledWith(1)
  })

  it('closes an envelope with a zero rollover, shown honestly rather than hidden', async () => {
    const detail = { ...openEnvelope, collected_amount: 120_000, disbursed_amount: 100_000, target_amount: null }
    const closed = { ...openEnvelope, closed_on: '2026-09-10' }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'), handle: () => Promise.resolve(jsonResponse(detail)) },
      {
        match: (m: string, u: string) => m === 'POST' && u.includes('/api/incidentals/1/close'),
        handle: () => Promise.resolve(jsonResponse({ incidental: closed, rolled_amount: 0 })),
      },
      ...getHandlers(),
    ]))

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    // Open the envelope's detail
    await userEvent.click(screen.getByRole('button', { name: /Halal bihalal RT/ }))
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())
    expect(screen.getByText(money(120_000))).toBeInTheDocument()
    expect(screen.getByText(money(100_000))).toBeInTheDocument()

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

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /Halal bihalal RT/ }))
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

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /Halal bihalal RT/ }))
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

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: /Halal bihalal RT/ }))
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

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: text.allTab }))
    await waitFor(() => expect(screen.getByText('17 Agustus')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: /17 Agustus/ }))
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

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: text.allTab }))
    await waitFor(() => expect(screen.getByText('17 Agustus')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: /17 Agustus/ }))
    await waitFor(() => expect(screen.getByRole('button', { name: text.actions.reopen })).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.reopen }))

    await waitFor(() => expect(screen.getByText(text.reopen.success)).toBeInTheDocument())
    // Reopened - the same record/close actions any open envelope shows, not
    // a bare toggle with nothing next.
    expect(screen.getByRole('button', { name: text.actions.record })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.actions.close })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.reopen })).not.toBeInTheDocument()
  })

  it('opens an envelope, sending a null target when none was typed, and returns to the open tab', async () => {
    // The target is optional (PRD section 7.5): an untouched AmountInput is 0, which
    // means "no target" on the wire, not a target of nothing.
    const opened = { ...openEnvelope, purpose_id: 3, occasion: 'Kerja bakti', target_amount: null, opened_on: '2026-09-09' }
    let posted: unknown = null
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      if (method === 'POST' && url.includes('/api/incidentals')) {
        posted = JSON.parse(String(init?.body))
        return Promise.resolve(jsonResponse(opened, 201))
      }
      const handler = getHandlers().find((h) => h.match(method, url))
      if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      return handler.handle()
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.open.heading }))
    await userEvent.type(await screen.findByLabelText(text.open.occasionLabel), 'Kerja bakti')
    await userEvent.click(screen.getByRole('button', { name: text.open.submit }))

    await waitFor(() => expect(screen.getByText(text.open.success)).toBeInTheDocument())
    expect(posted).toEqual({ occasion: 'Kerja bakti', target_amount: null, opened_on: expect.any(String) })

    // The form closes and the list is back, so the new envelope is reachable.
    expect(screen.queryByLabelText(text.open.occasionLabel)).not.toBeInTheDocument()
  })

  it('sends the typed target amount as a plain integer when one was given', async () => {
    let posted: unknown = null
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      const method = (init?.method ?? 'GET').toUpperCase()
      if (method === 'POST' && url.includes('/api/incidentals')) {
        posted = JSON.parse(String(init?.body))
        return Promise.resolve(jsonResponse(openEnvelope, 201))
      }
      const handler = getHandlers().find((h) => h.match(method, url))
      if (!handler) return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
      return handler.handle()
    })
    vi.stubGlobal('fetch', fetchMock)

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.open.heading }))
    await userEvent.type(await screen.findByLabelText(text.open.occasionLabel), 'Kerja bakti')
    await userEvent.type(screen.getByLabelText(text.open.targetLabel), '500000')
    await userEvent.click(screen.getByRole('button', { name: text.open.submit }))

    await waitFor(() => expect(screen.getByText(text.open.success)).toBeInTheDocument())
    expect(posted).toMatchObject({ target_amount: 500_000 })
  })

  it('returns from a detail view to the list', async () => {
    const detail = { ...openEnvelope, collected_amount: 0, disbursed_amount: 0 }
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/incidentals/1'), handle: () => Promise.resolve(jsonResponse(detail)) },
      ...getHandlers(),
    ]))

    render(<Incidentals onBack={vi.fn()} onRecordFor={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Halal bihalal RT')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: /Halal bihalal RT/ }))
    await waitFor(() => expect(screen.getByText(text.detail.collectedLabel)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.detail.backToList }))
    await waitFor(() => expect(screen.getByRole('button', { name: text.openTab })).toBeInTheDocument())
    expect(screen.queryByText(text.detail.collectedLabel)).not.toBeInTheDocument()
  })

  it('calls onBack when backToHome is clicked', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    const onBack = vi.fn()
    render(<Incidentals onBack={onBack} onRecordFor={vi.fn()} />)

    await userEvent.click(await screen.findByRole('button', { name: text.backToHome }))
    expect(onBack).toHaveBeenCalledTimes(1)
  })
})
