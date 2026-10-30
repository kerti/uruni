import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RecordDuesPayment from '@/screens/Dues/RecordPayment'
import { copy } from '@/copy/id'
import { chooseOption, selectOptionNames, selectedOptionName } from '@/test/select'
import { formatIDR } from '@/lib/money'
import type { OutstandingDuesPeriod } from '@/lib/dues'
import type { Purpose } from '@/lib/purposes'
import type { Account, Member } from '@/lib/setup'

const text = copy.dues.payment

afterEach(() => {
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const members = [
  {
    id: 1,
    name: 'Warga Satu',
    tier_id: 1,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
  {
    id: 2,
    name: 'Warga Dua',
    tier_id: 1,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
] satisfies Member[]

const accounts = [{ id: 7, kind: 'cash', name: 'Kas tunai', inactive_on: null, created_at: 1 }] satisfies Account[]
const twoAccounts = [...accounts, { id: 8, kind: 'bank', name: 'Bank Uji Coba', inactive_on: null, created_at: 1 }] satisfies Account[]
const purposes = [{ id: 3, kind: 'main', name: 'Kas umum', created_at: 1 }] satisfies Purpose[]

// One unpaid period and one part-paid one: the pre-fill is the whole rate
// for the first and only the sisa for the second.
const outstanding = [
  { period: '2026-01', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
  { period: '2026-02', owed_amount: 50_000, paid_amount: 20_000, status: 'partial' },
] satisfies OutstandingDuesPeriod[]

function stubApi(periods: OutstandingDuesPeriod[] = outstanding, accountList: Account[] = accounts) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    if (url.includes('/outstanding-dues')) return Promise.resolve(jsonResponse(periods))
    if (url.includes('/api/members')) return Promise.resolve(jsonResponse({ members, next_cursor: null }))
    if (url.includes('/api/accounts')) return Promise.resolve(jsonResponse(accountList))
    if (url.includes('/api/purposes')) return Promise.resolve(jsonResponse(purposes))
    if (url.includes('/api/dues-payments') && init?.method === 'POST') return Promise.resolve(jsonResponse([], 201))
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

async function pickFirstMember() {
  // The themed Select's options live in a portal - open it, then pick by the
  // name she reads.
  await screen.findByLabelText(text.memberLabel)
  await chooseOption(text.memberLabel, 'Warga Satu')
}

/** Taps the edit button on a ticked month and returns the amount field it opens. */
async function openEditor(period: string) {
  await userEvent.click(screen.getByRole('button', { name: new RegExp(`^${text.editAmount(period)}`) }))
  return screen.getByLabelText(text.amountLabel(period))
}

/** The parsed body of the last POST /api/dues-payments the stub saw. */
function lastPostedBody(fetchMock: ReturnType<typeof stubApi>) {
  const call = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'POST').at(-1)
  return JSON.parse(String((call?.[1] as RequestInit | undefined)?.body))
}

describe('RecordDuesPayment', () => {
  it('seeds the member and ticks arrears plus the month when opened from a status card (#740)', async () => {
    vi.stubGlobal('fetch', stubApi())
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} initialMemberId={2} initialPeriod="2026-02" />)

    // The boxes render with the periods (two fetches in, so slow under load);
    // the ticks land one effect later.
    const january = await screen.findByRole('checkbox', { name: /Januari 2026/ }, { timeout: 5000 })
    await waitFor(() => expect(january).toBeChecked())
    await waitFor(() =>
      expect(screen.getByRole('button', { name: new RegExp(`^${text.editAmount('Januari 2026')}`) })).toHaveTextContent(formatIDR(50_000)),
    )
    expect(screen.getByRole('checkbox', { name: /Februari 2026/ })).toBeChecked()
    await waitFor(() => expect(selectedOptionName(text.memberLabel)).toBe('Warga Dua'))
  })

  it('ticks nothing and picks nobody without the status card props (#740)', async () => {
    vi.stubGlobal('fetch', stubApi())
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await pickFirstMember()

    const boxes = await screen.findAllByRole('checkbox')
    for (const box of boxes) expect(box).not.toBeChecked()
  })

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

    await screen.findByRole('checkbox', { name: /Januari 2026/ })
    const rows = screen.getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent(formatIDR(50_000))
    expect(rows[1]).toHaveTextContent(formatIDR(30_000))
    // Compact: no amount field until a ticked month's edit button is tapped,
    // and only a ticked month offers one.
    expect(screen.queryByLabelText(text.amountLabel('Januari 2026'))).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Ubah jumlah/ })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('checkbox', { name: /Januari 2026/ }))
    expect(screen.getAllByRole('button', { name: /^Ubah jumlah/ })).toHaveLength(1)
    expect(screen.queryByLabelText(text.amountLabel('Januari 2026'))).not.toBeInTheDocument()
    // Autofocused, so it shows the bare digits it holds.
    expect(await openEditor('Januari 2026')).toHaveValue('50000')
    // Unticking closes the editor.
    await userEvent.click(screen.getByRole('checkbox', { name: /Januari 2026/ }))
    expect(screen.queryByLabelText(text.amountLabel('Januari 2026'))).not.toBeInTheDocument()
  })

  it('keeps a part-paid month on one line: "sebagian" after the month, the paid part read but not drawn', async () => {
    vi.stubGlobal('fetch', stubApi())
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await pickFirstMember()

    const february = await screen.findByRole('checkbox', { name: 'Februari 2026' })
    const row = screen.getAllByRole('listitem')[1]
    expect(row).toHaveTextContent(new RegExp(`Februari 2026.*${text.partialSuffix}.*${formatIDR(30_000).replace('.', '\\.')}`))
    expect(row).not.toHaveTextContent(copy.dues.paidLabel)
    expect(february).toHaveAccessibleDescription(text.paidSoFar(formatIDR(20_000)))
    expect(screen.getAllByRole('listitem')[0]).not.toHaveTextContent(text.partialSuffix)

    // Paying less than the sisa: only the figure changes; the month's word holds still.
    await userEvent.click(february)
    const amount = (await openEditor('Februari 2026')) as HTMLInputElement
    await waitFor(() => expect([amount.selectionStart, amount.selectionEnd]).toEqual([0, amount.value.length]))
    await userEvent.keyboard('10000')
    expect(screen.getByRole('button', { name: /^Ubah jumlah Februari 2026/ })).toHaveTextContent(formatIDR(10_000))
    expect(row).toHaveTextContent(text.partialSuffix)
  })

  it("sends the client's own local month as ?through", async () => {
    const fetchMock = stubApi()
    vi.stubGlobal('fetch', fetchMock)
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await pickFirstMember()

    await screen.findByRole('checkbox', { name: /Januari 2026/ })
    const now = new Date()
    const month = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
    const url = fetchMock.mock.calls.map(([input]) => input.toString()).find((u) => u.includes('/outstanding-dues')) ?? ''
    expect(url).toContain(`/api/members/1/outstanding-dues?through=${encodeURIComponent(month)}`)
  })

  it('opens on the last-used location, falling back to the first active one (#737)', async () => {
    window.localStorage.setItem('uruni:record:last-account-id', '8')
    vi.stubGlobal('fetch', stubApi(outstanding, twoAccounts))
    const { unmount } = render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await waitFor(() => expect(selectedOptionName(text.locationLabel)).toBe('Bank Uji Coba'))
    unmount()

    window.localStorage.setItem('uruni:record:last-account-id', '99')
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await waitFor(() => expect(selectedOptionName(text.locationLabel)).toBe('Kas tunai'))
  })

  it('remembers the location a payment was posted from', async () => {
    const fetchMock = stubApi(outstanding, twoAccounts)
    vi.stubGlobal('fetch', fetchMock)
    const onRecorded = vi.fn()
    render(<RecordDuesPayment onRecorded={onRecorded} onCancel={vi.fn()} />)
    await pickFirstMember()
    await chooseOption(text.locationLabel, 'Bank Uji Coba')
    await userEvent.click(await screen.findByRole('checkbox', { name: 'Januari 2026' }))
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    await waitFor(() => expect(onRecorded).toHaveBeenCalledTimes(1))
    expect(window.localStorage.getItem('uruni:record:last-account-id')).toBe('8')
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
      // No note is typed on this form and nothing is generated onto the wire -
      // the row explains itself through TransactionList's display label.
      note: null,
      periods: [
        { dues_period: '2026-01', amount: 50_000 },
        { dues_period: '2026-02', amount: 30_000 },
      ],
    })
    // The server owns the dues purpose - always Kas Utama.
    expect(lastPostedBody(fetchMock)).not.toHaveProperty('purpose_id')
  })

  it('posts only the periods that were ticked, at the amount as edited', async () => {
    const fetchMock = stubApi()
    vi.stubGlobal('fetch', fetchMock)
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await pickFirstMember()

    await userEvent.click(await screen.findByRole('checkbox', { name: 'Januari 2026' }))
    const amount = (await openEditor('Januari 2026')) as HTMLInputElement
    // The editor opens focused with the whole figure selected, so typing
    // replaces it rather than appending to it.
    await waitFor(() => expect(amount).toHaveFocus())
    await waitFor(() => expect([amount.selectionStart, amount.selectionEnd]).toEqual([0, amount.value.length]))
    await userEvent.keyboard('25000')
    expect(screen.getByText(text.totalLabel).parentElement).toHaveTextContent(formatIDR(25_000))
    await userEvent.click(screen.getByRole('button', { name: text.submit }))

    await waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === 'POST')).toBe(true))
    expect(lastPostedBody(fetchMock).periods).toEqual([{ dues_period: '2026-01', amount: 25_000 }])
  })

  it('shows the arrears beside a member in the picker, without changing the option name', async () => {
    const withArrears = members.map((m) => (m.id === 2 ? { ...m, arrears_months: 3 } : m))
    const base = stubApi()
    vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      if (url.includes('/api/members') && !url.includes('/outstanding-dues'))
        return Promise.resolve(jsonResponse({ members: withArrears, next_cursor: null }))
      return base(input, init)
    })
    render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
    await screen.findByLabelText(text.memberLabel)
    const names = await selectOptionNames(text.memberLabel)
    expect(names[0]).toBe('Warga Satu')
    expect(names[1]).toContain(copy.members.roster.arrearsBadge(3))
    expect(screen.getByRole('option', { name: 'Warga Dua' })).toBeInTheDocument()
  })

  describe('select all arrears', () => {
    beforeEach(() => {
      vi.useFakeTimers({ toFake: ['Date'] })
      vi.setSystemTime(new Date('2026-03-10T09:00:00+07:00'))
    })
    afterEach(() => {
      vi.useRealTimers()
    })

    it('ticks exactly the months before the current one, keeping what was ticked', async () => {
      const periods: OutstandingDuesPeriod[] = [
        { period: '2026-01', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
        { period: '2026-02', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
        { period: '2026-03', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
      ]
      const fetchMock = stubApi(periods)
      vi.stubGlobal('fetch', fetchMock)
      render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
      await pickFirstMember()

      await userEvent.click(await screen.findByRole('checkbox', { name: 'Maret 2026' }))
      await userEvent.click(screen.getByRole('button', { name: text.selectAllArrears }))
      expect(screen.getByRole('checkbox', { name: 'Januari 2026' })).toBeChecked()
      expect(screen.getByRole('checkbox', { name: /^Februari 2026/ })).toBeChecked()
      expect(screen.getByRole('checkbox', { name: 'Maret 2026' })).toBeChecked()

      await userEvent.click(screen.getByRole('checkbox', { name: 'Maret 2026' }))
      await userEvent.click(screen.getByRole('button', { name: text.selectAllArrears }))
      expect(screen.getByRole('checkbox', { name: 'Maret 2026' })).not.toBeChecked()
      await userEvent.click(screen.getByRole('button', { name: text.submit }))
      await waitFor(() =>
        expect(lastPostedBody(fetchMock).periods).toEqual([
          { dues_period: '2026-01', amount: 50_000 },
          { dues_period: '2026-02', amount: 50_000 },
        ]),
      )
    })

    it('notes older owed months passed over, without blocking Simpan', async () => {
      const periods: OutstandingDuesPeriod[] = [
        { period: '2026-01', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
        { period: '2026-02', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
        { period: '2026-03', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
      ]
      vi.stubGlobal('fetch', stubApi(periods))
      render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
      await pickFirstMember()

      // Oldest first: nothing passed over, no note.
      await userEvent.click(await screen.findByRole('checkbox', { name: 'Januari 2026' }))
      expect(screen.queryByText(text.skippedArrears(1))).toBeNull()

      // March ticked past an unticked February: one month passed over.
      await userEvent.click(screen.getByRole('checkbox', { name: 'Maret 2026' }))
      expect(screen.getByRole('status')).toHaveTextContent(text.skippedArrears(1))
      expect(screen.getByRole('button', { name: text.submit })).toBeEnabled()

      await userEvent.click(screen.getByRole('checkbox', { name: 'Januari 2026' }))
      expect(screen.getByRole('status')).toHaveTextContent(text.skippedArrears(2))

      await userEvent.click(screen.getByRole('button', { name: text.selectAllArrears }))
      expect(screen.queryByText(/tunggakan yang lebih lama/)).toBeNull()
    })

    it('is absent when nothing is owed before the current month', async () => {
      vi.stubGlobal('fetch', stubApi([{ period: '2026-03', owed_amount: 50_000, paid_amount: 0, status: 'unpaid' }]))
      render(<RecordDuesPayment onRecorded={vi.fn()} onCancel={vi.fn()} />)
      await pickFirstMember()

      await screen.findByRole('checkbox', { name: 'Maret 2026' })
      expect(screen.queryByRole('button', { name: text.selectAllArrears })).not.toBeInTheDocument()
    })
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
      vi.setSystemTime(new Date('2026-10-01T09:00:00+07:00')) // the fund's day, whatever zone this machine is in
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
        const periods: OutstandingDuesPeriod[] = []
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

      await screen.findByRole('checkbox', { name: 'Desember 2026' })
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
      const october = (await openEditor('Oktober 2026')) as HTMLInputElement
      await waitFor(() => expect([october.selectionStart, october.selectionEnd]).toEqual([0, october.value.length]))
      await userEvent.keyboard('40000')

      await userEvent.click(screen.getByRole('button', { name: text.addMonth('November 2026') }))

      await screen.findByRole('checkbox', { name: 'November 2026' })
      expect(screen.getByRole('checkbox', { name: 'Oktober 2026' })).toBeChecked()
      expect(screen.getByLabelText(text.amountLabel('Oktober 2026'))).toHaveValue('40000')
      expect(screen.getByRole('button', { name: /^Ubah jumlah Oktober 2026/ })).toHaveTextContent(formatIDR(40_000))
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
