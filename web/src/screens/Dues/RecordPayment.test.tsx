import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

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
    if (url.includes('/api/members')) return Promise.resolve(jsonResponse({ members, next_cursor: null }))
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

  it("sends the client's own local month as ?through", async () => {
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
      // #257: no note is typed on this form, and nothing is generated onto
      // the wire - the row explains itself through TransactionList's own
      // display label instead.
      note: null,
      periods: [
        { dues_period: '2026-01', amount: 50_000 },
        { dues_period: '2026-02', amount: 30_000 },
      ],
    })
    // #473: the server owns the dues purpose - always Kas Utama.
    expect(lastPostedBody(fetchMock)).not.toHaveProperty('purpose_id')
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

  describe('paying ahead (#357)', () => {
    // Only Date is faked: userEvent still needs real timers.
    beforeEach(() => {
      vi.useFakeTimers({ toFake: ['Date'] })
      vi.setSystemTime(new Date(2026, 9, 1))
    })
    afterEach(() => {
      vi.useRealTimers()
    })

    /** A server that owes every month from `from` through ?through, at the
     * full rate - the way OutstandingDuesForMember answers a later through. */
    function stubAhead(from: string) {
      const base = stubApi()
      return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === 'string' ? input : input.toString()
        if (!url.includes('/outstanding-dues')) return base(input, init)
        const through = decodeURIComponent(new URL(url, 'http://x').searchParams.get('through') ?? '')
        const periods = []
        for (let m = from; m <= through;) {
          periods.push({ period: m, owed_amount: 50_000, paid_amount: 0, status: 'unpaid' })
          const [y, mo] = m.split('-').map(Number)
          m = mo === 12 ? `${y + 1}-01` : `${y}-${String(mo + 1).padStart(2, '0')}`
        }
        return Promise.resolve(jsonResponse(periods))
      })
    }

    function throughs(fetchMock: ReturnType<typeof stubAhead>) {
      return fetchMock.mock.calls
        .map(([input]) => input.toString())
        .filter((u) => u.includes('/outstanding-dues'))
        .map((u) => decodeURIComponent(u.split('through=')[1]))
    }

    it('adds the next month to the list and posts it with the rest', async () => {
      const fetchMock = stubAhead('2026-10')
      const onRecorded = vi.fn()
      vi.stubGlobal('fetch', fetchMock)
      render(<RecordDuesPayment onRecorded={onRecorded} onCancel={vi.fn()} />)
      await pickFirstMember()

      await userEvent.click(await screen.findByRole('button', { name: text.addMonth('November 2026') }))
      await userEvent.click(await screen.findByRole('button', { name: text.addMonth('Desember 2026') }))

      expect(await screen.findByLabelText(text.amountLabel('Desember 2026'))).toHaveValue(formatIDR(50_000))
      expect(throughs(fetchMock)).toEqual(['2026-10', '2026-11', '2026-12'])

      for (const name of ['Oktober 2026', 'November 2026', 'Desember 2026']) {
        await userEvent.click(screen.getByRole('checkbox', { name }))
      }
      await userEvent.click(screen.getByRole('button', { name: text.submit }))

      await waitFor(() => expect(onRecorded).toHaveBeenCalledTimes(1))
      expect(lastPostedBody(fetchMock).periods).toEqual([
        { dues_period: '2026-10', amount: 50_000 },
        { dues_period: '2026-11', amount: 50_000 },
        { dues_period: '2026-12', amount: 50_000 },
      ])
    })

    it('keeps what was ticked and edited when a month is added', async () => {
      const fetchMock = stubAhead('2026-10')
      vi.stubGlobal('fetch', fetchMock)
      render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
      await pickFirstMember()

      await userEvent.click(await screen.findByRole('checkbox', { name: 'Oktober 2026' }))
      const october = screen.getByLabelText(text.amountLabel('Oktober 2026'))
      await userEvent.clear(october)
      await userEvent.type(october, '40000')

      await userEvent.click(screen.getByRole('button', { name: text.addMonth('November 2026') }))

      await screen.findByLabelText(text.amountLabel('November 2026'))
      expect(screen.getByRole('checkbox', { name: 'Oktober 2026' })).toBeChecked()
      expect(screen.getByLabelText(text.amountLabel('Oktober 2026'))).toHaveValue(formatIDR(40_000))
    })

    it('offers the next month to a member who owes nothing yet', async () => {
      vi.stubGlobal('fetch', stubAhead('2026-11'))
      render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
      await pickFirstMember()

      expect(await screen.findByText(text.noOutstanding)).toBeInTheDocument()
      await userEvent.click(screen.getByRole('button', { name: text.addMonth('November 2026') }))
      expect(await screen.findByRole('checkbox', { name: 'November 2026' })).toBeInTheDocument()
    })

    it('stops a year past the current month', async () => {
      const fetchMock = stubAhead('2026-10')
      vi.stubGlobal('fetch', fetchMock)
      render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
      await pickFirstMember()

      for (let i = 0; i < 12; i++) {
        await userEvent.click(await screen.findByRole('button', { name: /^Tambah / }))
      }

      await screen.findByRole('checkbox', { name: 'Oktober 2027' })
      expect(screen.queryByRole('button', { name: /^Tambah / })).not.toBeInTheDocument()
      expect(throughs(fetchMock).at(-1)).toBe('2027-10')
    })

    it('starts over from the current month when the member changes', async () => {
      const fetchMock = stubAhead('2026-10')
      vi.stubGlobal('fetch', fetchMock)
      render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
      await pickFirstMember()

      await userEvent.click(await screen.findByRole('button', { name: text.addMonth('November 2026') }))
      await screen.findByRole('checkbox', { name: 'November 2026' })
      await chooseOption(text.memberLabel, 'Warga Dua')

      await screen.findByRole('checkbox', { name: 'Oktober 2026' })
      expect(screen.queryByRole('checkbox', { name: 'November 2026' })).not.toBeInTheDocument()
      expect(throughs(fetchMock).at(-1)).toBe('2026-10')
    })
  })
})
