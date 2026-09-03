import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import RecordDuesPayment from '@/screens/Dues/RecordPayment'
import { copy } from '@/copy/id'
import { chooseOption } from '@/test/select'
import { formatIDR } from '@/lib/money'

const text = copy.dues.payment

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const members = [
  { id: 1, name: 'Warga Satu', tier_id: 1, joined_on: '2026-01-01', inactive_on: null, created_at: 1 },
  { id: 2, name: 'Warga Dua', tier_id: 1, joined_on: '2026-01-01', inactive_on: null, created_at: 1 },
]

const accounts = [{ id: 7, kind: 'cash', name: 'Kas tunai', inactive_on: null, created_at: 1 }]
const purposes = [{ id: 3, kind: 'main', name: 'Kas umum', created_at: 1 }]

// One unpaid period and one part-paid one: the pre-fill is the whole rate
// for the first and only the sisa for the second.
const outstanding = [
  { period: '2026-01', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
  { period: '2026-02', owed_amount: 50_000, paid_amount: 20_000, status: 'partial' },
]

function stubApi(periods: unknown = outstanding) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    if (url.includes('/outstanding-dues')) return Promise.resolve(jsonResponse(periods))
    if (url.includes('/api/members')) return Promise.resolve(jsonResponse(members))
    if (url.includes('/api/accounts')) return Promise.resolve(jsonResponse(accounts))
    if (url.includes('/api/purposes')) return Promise.resolve(jsonResponse(purposes))
    if (url.includes('/api/dues-payments') && init?.method === 'POST') return Promise.resolve(jsonResponse([], 201))
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

async function pickFirstMember() {
  // The themed Select's options live in a portal (M6.15) - open it, then
  // pick by the name she actually reads.
  await screen.findByLabelText(text.memberLabel)
  await chooseOption(text.memberLabel, 'Warga Satu')
}

/** The parsed body of the last POST /api/dues-payments the stub saw. */
function lastPostedBody(fetchMock: ReturnType<typeof stubApi>) {
  const call = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'POST').at(-1)
  return JSON.parse(String((call?.[1] as RequestInit).body))
}

describe('RecordDuesPayment', () => {
  it('asks for a member before offering any period', async () => {
    vi.stubGlobal('fetch', stubApi())
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)

    expect(await screen.findByText(text.noMemberYet)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.submit })).toBeDisabled()
  })

  it('pre-fills each period from the tier rate the server returned - the sisa for a part-paid one', async () => {
    vi.stubGlobal('fetch', stubApi())
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await pickFirstMember()

    expect(await screen.findByLabelText(text.amountLabel('Januari 2026'))).toHaveValue(formatIDR(50_000))
    expect(screen.getByLabelText(text.amountLabel('Februari 2026'))).toHaveValue(formatIDR(30_000))
  })

  it('sends the client\'s own local month as ?through', async () => {
    const fetchMock = stubApi()
    vi.stubGlobal('fetch', fetchMock)
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await pickFirstMember()

    await screen.findByLabelText(text.amountLabel('Januari 2026'))
    const now = new Date()
    const month = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
    const url = fetchMock.mock.calls.map(([input]) => input.toString()).find((u) => u.includes('/outstanding-dues')) ?? ''
    expect(url).toContain(`/api/members/1/outstanding-dues?through=${encodeURIComponent(month)}`)
  })

  it('posts every selected period in one request', async () => {
    const fetchMock = stubApi()
    const onRecorded = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    render(<RecordDuesPayment onRecorded={onRecorded} onCancel={vi.fn()} />)
    await pickFirstMember()

    await userEvent.click(await screen.findByRole('checkbox', { name: 'Januari 2026' }))
    await userEvent.click(screen.getByRole('checkbox', { name: 'Februari 2026' }))
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    await waitFor(() => expect(onRecorded).toHaveBeenCalledTimes(1))
    const posts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'POST')
    expect(posts).toHaveLength(1)
    expect(lastPostedBody(fetchMock)).toMatchObject({
      member_id: 1,
      account_id: 7,
      purpose_id: 3,
      // Never null: a dues row has to read as one wherever it surfaces.
      note: text.note('Warga Satu'),
      periods: [
        { dues_period: '2026-01', amount: 50_000 },
        { dues_period: '2026-02', amount: 30_000 },
      ],
    })
  })

  it('posts only the periods that were ticked, at the amount as edited', async () => {
    const fetchMock = stubApi()
    vi.stubGlobal('fetch', fetchMock)
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await pickFirstMember()

    await userEvent.click(await screen.findByRole('checkbox', { name: 'Januari 2026' }))
    const amount = screen.getByLabelText(text.amountLabel('Januari 2026'))
    await userEvent.clear(amount)
    await userEvent.type(amount, '25000')
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === 'POST')).toBe(true))
    expect(lastPostedBody(fetchMock).periods).toEqual([{ dues_period: '2026-01', amount: 25_000 }])
  })

  it('says the member is square when nothing is outstanding, and refuses to submit', async () => {
    vi.stubGlobal('fetch', stubApi([]))
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await pickFirstMember()

    expect(await screen.findByText(text.noOutstanding)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.submit })).toBeDisabled()
  })
})
