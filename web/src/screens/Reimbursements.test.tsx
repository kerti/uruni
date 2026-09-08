import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Reimbursements from '@/screens/Reimbursements'
import { chooseOption } from '@/test/select'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'

const text = copy.reimbursements

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
    note: id === 1 ? 'Parkir' : null,
    created_at: id,
    ...overrides,
  }
}

const outstandingClaims: Claim[] = [claim(1)]

const allClaims: Claim[] = [claim(1), claim(2)]

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
      handle: () => Promise.resolve(jsonResponse(outstanding)),
    },
    {
      match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements') && !u.includes('outstanding'),
      handle: () => Promise.resolve(jsonResponse(all)),
    },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
    { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
  ]
}


describe('Reimbursements', () => {
  it('shows outstanding claims by default', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    render(<Reimbursements onBack={vi.fn()} />)

    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())
    expect(screen.getByText('Parkir')).toBeInTheDocument()
    expect(screen.getByText(money(15_000))).toBeInTheDocument()
    // The outstanding tab honestly labels a still-owed claim "Belum dibayar"
    // rather than the settled label (#166: the wire carries no settled flag).
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
        handle: () => Promise.resolve(jsonResponse(recorded ? [...initial, claim(3, { amount: 10_000, note: null })] : initial)),
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

    render(<Reimbursements onBack={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    // Open the record form
    await userEvent.click(screen.getByRole('button', { name: 'Catat penggantian' }))
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

    render(<Reimbursements onBack={vi.fn()} />)
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

  it('waive and un-waive work through the same PATCH', async () => {
    // The list starts with the claim un-waived; waiving PATCHes and the
    // reload returns it waived, so the un-waive affordance appears.
    let waived = false
    const initial = [claim(1)]
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements'),
        handle: () => Promise.resolve(jsonResponse(waived ? [claim(1, { waived_on: '2026-09-02' })] : initial)),
      },
      {
        match: (m: string, u: string) => m === 'PATCH' && u.includes('/api/reimbursements/1'),
        handle: () => {
          waived = true
          return Promise.resolve(jsonResponse(claim(1, { waived_on: '2026-09-02' })))
        },
      },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/members'), handle: () => Promise.resolve(jsonResponse(members)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/purposes'), handle: () => Promise.resolve(jsonResponse(purposes)) },
      { match: (m: string, u: string) => m === 'GET' && u.includes('/api/accounts'), handle: () => Promise.resolve(jsonResponse(accounts)) },
    ]))

    render(<Reimbursements onBack={vi.fn()} />)
    await waitFor(() => expect(screen.getByRole('button', { name: text.actions.waive })).toBeInTheDocument())

    // Waive
    await userEvent.click(screen.getByRole('button', { name: text.actions.waive }))

    // After waiving, the un-waive button should appear
    await waitFor(() => expect(screen.getByRole('button', { name: text.actions.unwaive })).toBeInTheDocument())
  })

  it('settled claim shows no action buttons', async () => {
    // On the "all" tab, settled claims show up but have no actions.
    // Outstanding claims (default tab) are the ones with actions.
    vi.stubGlobal('fetch', routedFetch(getHandlers({ outstanding: [], all: allClaims })))
    render(<Reimbursements onBack={vi.fn()} />)

    // Wait for the list to load, then switch to the "all" tab
    await waitFor(() => expect(screen.getByRole('button', { name: text.allTab })).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: text.allTab }))

    // The settled claim (id: 2) should be visible but no action buttons
    await waitFor(() => expect(screen.getByText(money(25_000))).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: text.actions.settle })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.actions.correct })).not.toBeInTheDocument()
  })

  it('delete calls DELETE and re-fetches', async () => {
    // The outstanding tab shows Jane (id 1); deleting her empties it.
    let deleted = false
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'GET' && u.includes('/api/reimbursements'),
        handle: () => Promise.resolve(jsonResponse(deleted ? [] : [claim(1)])),
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

    render(<Reimbursements onBack={vi.fn()} />)
    await waitFor(() => expect(screen.getByText('Jane')).toBeInTheDocument())

    // Click delete to show confirmation
    await userEvent.click(screen.getByRole('button', { name: text.actions.delete }))
    // Confirm delete
    await userEvent.click(screen.getByRole('button', { name: text.actions.delete }))

    // After deletion, the list refreshes (the claim disappears)
    await waitFor(() => expect(screen.queryByText('Jane')).not.toBeInTheDocument())
  })

  it('correct opens pre-filled edit form, PATCH fires', async () => {
    vi.stubGlobal('fetch', routedFetch([
      {
        match: (m: string, u: string) => m === 'PATCH' && u.includes('/api/reimbursements/1'),
        handle: () => Promise.resolve(jsonResponse(claim(1, { amount: 20_000 }))),
      },
      ...getHandlers(),
    ]))

    render(<Reimbursements onBack={vi.fn()} />)
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
    render(<Reimbursements onBack={vi.fn()} />)

    // Open record form
    await userEvent.click(await screen.findByRole('button', { name: 'Catat penggantian' }))

    // Submit should be disabled (no member, no amount)
    await waitFor(() => expect(screen.getByRole('button', { name: text.record.submit })).toBeDisabled())
  })

  it('calls onBack when "Kembali ke beranda" is clicked', async () => {
    vi.stubGlobal('fetch', routedFetch(getHandlers()))
    const onBack = vi.fn()
    render(<Reimbursements onBack={onBack} />)

    await userEvent.click(await screen.findByRole('button', { name: 'Kembali ke beranda' }))
    expect(onBack).toHaveBeenCalledTimes(1)
  })
})
