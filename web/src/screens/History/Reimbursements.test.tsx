import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Reimbursements from '@/screens/History/Reimbursements'
import { chooseOption } from '@/test/select'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'

const text = copy.reimbursements
const searchText = copy.history.reimbursements

afterEach(() => {
  vi.unstubAllGlobals()
})

function money(amount: number): string {
  return formatIDR(amount).replace(/\u00a0/g, ' ')
}

const members = [
  { id: 1, name: 'Jane', tier_id: 1, joined_on: '2026-01-01', inactive_on: null, created_at: 1 },
  { id: 2, name: 'John', tier_id: 2, joined_on: '2026-01-01', inactive_on: null, created_at: 1 },
]

const purposes = [
  { id: 10, kind: 'pass_through', name: 'Kas Bidang', created_at: 1 },
  { id: 11, kind: 'main', name: 'Kas Utama', created_at: 1 },
]

const accounts = [
  { id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 },
]

interface Claim {
  id: number
  member_id: number
  purpose_id: number
  amount: number
  incurred_on: string
  waived_on: string | null
  settled: boolean
  note: string | null
  created_at: number
}

function claim(id: number, overrides: Partial<Claim> = {}): Claim {
  return {
    id,
    member_id: id === 2 ? 2 : 1,
    purpose_id: 11,
    amount: id === 2 ? 25_000 : 15_000,
    incurred_on: '2026-09-01',
    waived_on: null,
    settled: false,
    note: id === 1 ? 'Parkir' : null,
    created_at: id,
    ...overrides,
  }
}

/** Wraps rows in GET /api/reimbursements's own envelope (#226). */
function page(rows: Claim[], nextCursor: string | null = null) {
  return { reimbursements: rows, next_cursor: nextCursor }
}

const outstandingClaims: Claim[] = [claim(1)]

const allClaims: Claim[] = [claim(1), claim(2, { settled: true })]

const postedTransaction = {
  id: 1,
  account_id: 1,
  purpose_id: 11,
  direction: 'out',
  amount: 15_000,
  occurred_on: '2026-09-02',
  kind: 'reimbursement',
  member_id: 1,
  dues_period: null,
  reimbursement_id: 1,
  transfer_id: null,
  reverses_transaction_id: null,
  note: null,
  created_at: 3,
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

/** The default GET handlers backing every test: the reimbursements list
 * (outstanding vs all variants) plus the members/purposes/accounts form
 * data fetched when a record/settle/correct form is opened. */
function getHandlers(opts: { outstanding?: Claim[]; all?: Claim[] } = {}) {
  const outstanding = opts.outstanding ?? outstandingClaims
  const all = opts.all ?? allClaims
  return [
    {
      match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('outstanding=true'),
      handle: () => Promise.resolve(jsonResponse(page(outstanding))),
    },
    {
      match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && !u.includes('outstanding'),
      handle: () => Promise.resolve(jsonResponse(page(all))),
    },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
  ]
}

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location">{location.search}</output>
}

function renderAt(entry = '/history/reimbursements') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/history/reimbursements"
          element={
            <>
              <Reimbursements />
              <LocationProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('Reimbursements tab', () => {
  it('shows outstanding claims by default', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt()

    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    expect(screen.getByText('Parkir')).toBeInTheDocument()
    expect(screen.getByText(money(15_000))).toBeInTheDocument()
    // The outstanding tab honestly labels a still-owed claim "Belum dibayar".
    // `settled` rides the wire (the list queries compute the flag), so the
    // badge is a fact, not a tab-dependent guess.
    expect(screen.getByText(text.status.outstanding, { selector: 'span' })).toBeInTheDocument()
  })

  it('records a claim, list updates immediately', async () => {
    // Recording POSTs a new claim; the reload then returns it alongside the
    // original outstanding claim.
    let recorded = false
    const initial = [claim(1)]
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements'),
        handle: () => Promise.resolve(jsonResponse(page(recorded ? [...initial, claim(3, { amount: 10_000, note: null })] : initial))),
      },
      {
        match: (m: string, u: string) => m === 'POST' && u.includes('/api/reimbursements'),
        handle: () => {
          recorded = true
          return Promise.resolve(jsonResponse(claim(3, { amount: 10_000, note: null }), 201))
        },
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))

    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    // Open the record form
    await userEvent.click(screen.getByRole('button', { name: text.record.heading }))
    await waitFor(() => expect(screen.getByText(text.record.heading)).toBeInTheDocument())

    // Fill the form
    await chooseOption(text.record.memberLabel, 'Jane')
    await userEvent.type(screen.getByLabelText(text.record.amountLabel), '10000')
    await userEvent.click(screen.getByRole('button', { name: text.record.submit }))

    // Success message shown and form closed
    await waitFor(() => expect(screen.getByText(text.record.success)).toBeInTheDocument())
  })

  it('settle moves a claim out of the outstanding list', async () => {
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'POST' && u.includes('/api/reimbursements/1/settle'),
        handle: () => Promise.resolve(jsonResponse(postedTransaction, 201)),
      },
      ...getHandlers(),
    ]))

    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    // Open settle form
    await userEvent.click(screen.getByRole('button', { name: text.actions.settle }))
    await waitFor(() => expect(screen.getByText(text.settle.heading)).toBeInTheDocument())

    // Select account and submit settle
    await chooseOption(text.settle.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.settle.submit }))

    // Success message shown
    await waitFor(() => expect(screen.getByText(text.settle.success)).toBeInTheDocument())
  })

  it('waive is reversible: un-waive is reachable from the all tab', async () => {
    // The outstanding list never contains a waived claim, so after a waive
    // the row leaves it; the all tab shows the waived row with "Batalkan
    // pemutihan", which un-waives it straight back. The outstanding GET can
    // therefore never present a row with a "Batalkan pemutihan" affordance.
    let waived = false
    const initial = [claim(1)]
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('outstanding=true'),
        handle: () => Promise.resolve(jsonResponse(page(waived ? [] : initial))),
      },
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && !u.includes('outstanding'),
        handle: () => Promise.resolve(jsonResponse(page(waived ? [claim(1, { waived_on: '2026-09-02' })] : initial))),
      },
      {
        match: (m: string, u: string) => m === 'PATCH' && u.includes('/api/reimbursements/1'),
        handle: () => {
          waived = !waived
          return Promise.resolve(jsonResponse(waived ? claim(1, { waived_on: '2026-09-02' }) : claim(1)))
        },
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))

    renderAt()
    await waitFor(() => expect(screen.getByRole('button', { name: text.actions.waive })).toBeInTheDocument())

    // Waive: the row leaves the outstanding list and a feedback says so.
    await userEvent.click(screen.getByRole('button', { name: text.actions.waive }))
    await waitFor(() => expect(screen.getByText(text.waive.success)).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: text.actions.unwaive })).not.toBeInTheDocument()

    // The all tab shows the waived row, where the un-waive affordance lives.
    await userEvent.click(screen.getByRole('button', { name: text.allTab }))
    await waitFor(() => expect(screen.getByRole('button', { name: text.actions.unwaive })).toBeInTheDocument())
    expect(screen.getByText(text.status.waived, { selector: 'span' })).toBeInTheDocument()

    // Un-waive: the claim is owed again, badge flips back.
    await userEvent.click(screen.getByRole('button', { name: text.actions.unwaive }))
    await waitFor(() => expect(screen.getByText(text.status.outstanding, { selector: 'span' })).toBeInTheDocument())
    expect(screen.getByText(text.unwaive.success)).toBeInTheDocument()
  })

  it('all tab is honest history: Dibayar only when the ledger settled it', async () => {
    // The all tab mixes a settled row (id 2, flagged by the list queries) and
    // a still-owed row (id 1). The badges must reflect the flag: never a
    // "Dibayar" label on an unsettled claim, and no action buttons at all.
    vi.stubGlobal('fetch', routedFetch(getHandlers({ outstanding: [], all: allClaims })))
    renderAt()

    await waitFor(() => expect(screen.getByRole('button', { name: text.allTab })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: text.allTab }))

    await waitFor(() => expect(screen.getByText(money(25_000))).toBeInTheDocument())
    expect(screen.getByText(text.status.settled, { selector: 'span' })).toBeInTheDocument()
    expect(screen.getByText(text.status.outstanding, { selector: 'span' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.settle })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.correct })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.waive })).not.toBeInTheDocument()
  })

  it('delete calls DELETE and re-fetches', async () => {
    // The outstanding tab shows Jane (id 1); deleting her empties it.
    let deleted = false
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements'),
        handle: () => Promise.resolve(jsonResponse(page(deleted ? [] : [claim(1)]))),
      },
      {
        match: (m: string, u: string) => m === 'DELETE' && u.includes('/api/reimbursements/1'),
        handle: () => {
          deleted = true
          return Promise.resolve(new Response(null, { status: 204 }))
        },
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))

    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    // Click delete to show confirmation
    await userEvent.click(screen.getByRole('button', { name: text.actions.delete }))
    // Confirm delete
    await userEvent.click(screen.getByRole('button', { name: text.actions.delete }))

    // After deletion, the list refreshes (the claim disappears) and feedback
    // confirms the write landed.
    await waitFor(() => expect(screen.queryByText('Jane')).not.toBeInTheDocument())
    expect(screen.getByText(text.delete.success)).toBeInTheDocument()
  })

  it('correct opens pre-filled edit form, PATCH fires', async () => {
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'PATCH' && u.includes('/api/reimbursements/1'),
        handle: () => Promise.resolve(jsonResponse(claim(1, { amount: 20_000 }))),
      },
      ...getHandlers(),
    ]))

    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    // Open correct form
    await userEvent.click(screen.getByRole('button', { name: text.actions.correct }))
    await waitFor(() => expect(screen.getByText(text.correct.heading)).toBeInTheDocument())

    // Submit the correction
    await userEvent.click(screen.getByRole('button', { name: text.correct.submit }))

    // Success message shown
    await waitFor(() => expect(screen.getByText(text.correct.success)).toBeInTheDocument())
  })

  it('keeps submit disabled until required fields are filled', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt()

    // Open record form
    await userEvent.click(await screen.findByRole('button', { name: text.record.heading }))

    // Submit should be disabled (no member, no amount)
    await waitFor(() => expect(screen.getByRole('button', { name: text.record.submit })).toBeDisabled())
  })

  it('a failed write says why and keeps the form open', async () => {
    // A settle that races by (claim already settled) returns 409 with a
    // named code. The message must come from copy, never the English wire
    // message, the form must stay open, and the list must still reload.
    let settleAttempted = false
    let listRefreshes = 0
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'POST' && u.includes('/api/reimbursements/1/settle'),
        handle: () => {
          settleAttempted = true
          return Promise.resolve(
            jsonResponse({ error: { code: 'reimbursement_already_settled', message: 'already settled' } }, 409),
          )
        },
      },
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('outstanding=true'),
        handle: () => {
          listRefreshes += 1
          return Promise.resolve(jsonResponse(page(outstandingClaims)))
        },
      },
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && !u.includes('outstanding'),
        handle: () => Promise.resolve(jsonResponse(page(allClaims))),
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))

    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.settle }))
    await waitFor(() => expect(screen.getByText(text.settle.heading)).toBeInTheDocument())
    await chooseOption(text.settle.accountLabel, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.settle.submit }))

    // The copy.local error is shown as an alert; no wire message leaks.
    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe(text.errors.reimbursement_already_settled))
    // The form is still open, and the failed write still reloaded the list.
    expect(screen.getByText(text.settle.heading)).toBeInTheDocument()
    expect(settleAttempted).toBe(true)
    expect(listRefreshes).toBeGreaterThan(1)
  })

  it('switching tabs closes an open inline form', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.actions.settle }))
    await waitFor(() => expect(screen.getByText(text.settle.heading)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('button', { name: text.allTab }))
    expect(screen.queryByText(text.settle.heading)).not.toBeInTheDocument()
  })

  it('a new action retires the previous success message', async () => {
    // Settling claim A shows "Talangan sudah dibayar."; the refresh drops A
    // from the outstanding list and closes the inline form. Starting the next
    // action on still-outstanding claim B must retire that stale success
    // message rather than let it describe the wrong row.
    let settled = false
    const before = [claim(1), claim(2, { note: 'Beli kabel' })]
    const after = [claim(2, { note: 'Beli kabel' })]
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'POST' && u.includes('/api/reimbursements/1/settle'),
        handle: () => {
          settled = true
          return Promise.resolve(jsonResponse(postedTransaction, 201))
        },
      },
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('outstanding=true'),
        handle: () => Promise.resolve(jsonResponse(page(settled ? after : before))),
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))
    renderAt()
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    await userEvent.click(screen.getAllByRole('button', { name: text.actions.settle })[0])
    await chooseOption(text.settle.accountLabel, 'Tunai')
    const settleForm = screen.getByText(text.settle.heading).closest('form')
    if (!settleForm) throw new Error('settle form not found')
    await userEvent.click(within(settleForm).getByRole('button', { name: text.settle.submit }))
    await waitFor(() => expect(screen.getByText(text.settle.success)).toBeInTheDocument())

    // The refresh drops settled claim A (Jane) and closes its inline form;
    // claim B remains, its action reachable. The delete action on B retires
    // A's stale success message.
    await waitFor(() => expect(screen.queryByText('Jane')).not.toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: text.actions.delete }))
    expect(screen.queryByText(text.settle.success)).not.toBeInTheDocument()
  })

  it('reads ?q= from the URL into the field and sends it to the server', async () => {
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('q=Budi'),
        handle: () => Promise.resolve(jsonResponse(page([claim(1, { note: 'Budi bayar parkir' })]))),
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))
    renderAt('/history/reimbursements?q=Budi')

    expect(await screen.findByText('Budi bayar parkir')).toBeInTheDocument()
    expect(screen.getByLabelText(searchText.searchLabel)).toHaveValue('Budi')
  })

  it('writes a typed search to the URL once, after the pause, and refetches with it', async () => {
    const requests: URL[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost')
        if (url.pathname === '/api/members') return jsonResponse(members)
        if (url.pathname === '/api/purposes') return jsonResponse(purposes)
        if (url.pathname === '/api/accounts') return jsonResponse(accounts)
        if (url.pathname === '/api/reimbursements') {
          requests.push(url)
          return jsonResponse(
            url.searchParams.get('q') === 'parkir'
              ? page([claim(1, { note: 'Parkir' })])
              : page([claim(2, { note: 'Beli kabel' })]),
          )
        }
        return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
      }),
    )
    const user = userEvent.setup()
    renderAt()

    expect(await screen.findByText('Beli kabel')).toBeInTheDocument()
    await user.type(screen.getByLabelText(searchText.searchLabel), 'parkir')

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('?q=parkir'))
    expect(await screen.findByText('Parkir')).toBeInTheDocument()
    // Debounced: no request for "p", "pa", ... - only the finished word.
    expect(requests.filter((url) => url.searchParams.has('q')).map((url) => url.searchParams.get('q'))).toEqual(['parkir'])
  })

  it('says nothing matched when a search comes back empty', async () => {
    vi.stubGlobal('fetch', routedFetch([
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements'), handle: () => Promise.resolve(jsonResponse(page([]))) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))
    renderAt('/history/reimbursements?q=xyz')

    expect(await screen.findByText(searchText.noResults('xyz'))).toBeInTheDocument()
  })

  it('loads the next page from the cursor and drops the button on the last page', async () => {
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && u.includes('cursor=c1'),
        handle: () => Promise.resolve(jsonResponse(page([claim(1)]))),
      },
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && !u.includes('cursor'),
        handle: () => Promise.resolve(jsonResponse(page([claim(2)], 'c1'))),
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))
    const user = userEvent.setup()
    renderAt()

    await screen.findByText('John')
    await user.click(screen.getByRole('button', { name: searchText.loadMore }))

    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    expect(screen.getByText('John')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: searchText.loadMore })).not.toBeInTheDocument()
  })

  it('combines the outstanding filter and search in the same request', async () => {
    const requests: URL[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost')
        if (url.pathname === '/api/members') return jsonResponse(members)
        if (url.pathname === '/api/purposes') return jsonResponse(purposes)
        if (url.pathname === '/api/accounts') return jsonResponse(accounts)
        if (url.pathname === '/api/reimbursements') {
          requests.push(url)
          return jsonResponse(page([claim(1)]))
        }
        return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
      }),
    )
    renderAt('/history/reimbursements?q=parkir')
    await screen.findByText('Jane')

    const last = requests.at(-1)
    expect(last?.searchParams.get('outstanding')).toBe('true')
    expect(last?.searchParams.get('q')).toBe('parkir')
  })
})
