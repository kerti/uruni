import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { copy } from '@/copy/id'
import Transactions from '@/screens/History/Transactions'
import { chooseOption } from '@/test/select'
import type { Purpose } from '@/lib/purposes'
import type { Member, Transaction } from '@/lib/setup'
import type { Transfer } from '@/lib/transfers'

afterEach(() => {
  vi.unstubAllGlobals()
  posted.length = 0
})

const text = copy.history.transactions

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function row(id: number, note: string): Transaction {
  return {
    id,
    account_id: 1,
    purpose_id: 11,
    direction: 'in',
    amount: 10_000,
    occurred_on: '2026-09-01',
    kind: 'normal',
    member_id: null,
    dues_period: null,
    reimbursement_id: null,
    transfer_id: null,
    reverses_transaction_id: null,
    note,
    created_at: id,
  }
}

type Page = { transactions: Transaction[]; next_cursor: string | null }

/** Every purpose the correction picker may offer. `?selectable=true` is what
 * the screen asks for, so a closed envelope is excluded server-side -
 * correcting INTO one is a named refusal (ADR-033). */
const purposes = [
  { id: 11, kind: 'main', name: 'Kas Utama', created_at: 1 },
  { id: 12, kind: 'pass_through', name: 'Titipan', created_at: 1 },
] satisfies Purpose[]

/** The roster the filter dialog offers - an inactive member too, since her
 * rows are still history. */
const members = [
  {
    id: 21,
    name: 'Budi',
    tier_id: null,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
  {
    id: 22,
    name: 'Sari',
    tier_id: null,
    joined_on: '2026-01-01',
    inactive_on: '2026-06-01',
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
] satisfies Member[]

/** Stubs fetch: purposes and members always answer, GET /api/transactions
 * answers whatever pageFor returns for the request's URL (throwing from
 * pageFor simulates fetch failing). Returns every transactions URL requested,
 * in order, so a test can assert on what reached the server; `everything`
 * holds every request, path and query. Correction POSTs go to `posted`. */
function stubApi(pageFor: (url: URL) => Page, everything: string[] = []) {
  const requests: URL[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      const method = (init?.method ?? 'GET').toUpperCase()
      everything.push(`${method} ${url.pathname}${url.search}`)
      if (url.pathname === '/api/purposes') return jsonResponse(purposes)
      if (url.pathname === '/api/members') return jsonResponse({ members, next_cursor: null })
      if (method === 'POST' && url.pathname.endsWith('/purpose-correction')) {
        posted.push({ url: url.pathname, body: JSON.parse(String(init?.body)) })
        return jsonResponse({ id: 1, kind: 'reclass_purpose', created_at: 1 } satisfies Transfer)
      }
      if (url.pathname === '/api/transactions') {
        requests.push(url)
        return jsonResponse(pageFor(url))
      }
      return jsonResponse({ error: { code: 'not_found', message: 'not found' } }, 404)
    }),
  )
  return requests
}

/** Correction POSTs seen by the stub, reset before each test. */
const posted: { url: string; body: unknown }[] = []

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location">{location.search}</output>
}

function renderAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/history/transactions"
          element={
            <>
              <Transactions />
              <LocationProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('Transactions tab', () => {
  it('loads the next page from the cursor and drops the button on the last page', async () => {
    const requests = stubApi((url) =>
      url.searchParams.get('cursor') === 'c1'
        ? { transactions: [row(1, 'Beli sapu')], next_cursor: null }
        : { transactions: [row(2, 'Galon')], next_cursor: 'c1' },
    )
    const user = userEvent.setup()
    renderAt('/history/transactions')

    expect(await screen.findByText('Galon')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: text.loadMore }))

    expect(await screen.findByText('Beli sapu')).toBeInTheDocument()
    expect(screen.getByText('Galon')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.loadMore })).not.toBeInTheDocument()
    expect(requests.at(-1)?.searchParams.get('cursor')).toBe('c1')
  })

  it('reads ?q= from the URL into the field and sends it to the server', async () => {
    const requests = stubApi(() => ({ transactions: [row(3, 'Iuran Budi')], next_cursor: null }))
    renderAt('/history/transactions?q=Budi')

    expect(await screen.findByText('Iuran Budi')).toBeInTheDocument()
    expect(screen.getByLabelText(text.searchLabel)).toHaveValue('Budi')
    expect(requests[0].searchParams.get('q')).toBe('Budi')
  })

  it('writes a typed search to the URL once, after the pause, and refetches with it', async () => {
    const requests = stubApi((url) =>
      url.searchParams.get('q') === 'galon'
        ? { transactions: [row(4, 'Galon Aqua')], next_cursor: null }
        : { transactions: [row(5, 'Beli sapu')], next_cursor: null },
    )
    const user = userEvent.setup()
    renderAt('/history/transactions')

    expect(await screen.findByText('Beli sapu')).toBeInTheDocument()
    await user.type(screen.getByLabelText(text.searchLabel), 'galon')

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('?q=galon'))
    expect(await screen.findByText('Galon Aqua')).toBeInTheDocument()
    // Debounced: no request for "g", "ga", ... - only the finished word.
    expect(requests.filter((url) => url.searchParams.has('q')).map((url) => url.searchParams.get('q'))).toEqual(['galon'])
  })

  // Pos names and the filter options do not change
  // with the search, so a search asks for the page alone.
  it('fetches its lookups once, then only the page on a search', async () => {
    const everything: string[] = []
    stubApi(
      (url) =>
        url.searchParams.get('q') === 'galon'
          ? { transactions: [row(4, 'Galon Aqua')], next_cursor: null }
          : { transactions: [row(5, 'Beli sapu')], next_cursor: null },
      everything,
    )
    const user = userEvent.setup()
    renderAt('/history/transactions')

    expect(await screen.findByText('Beli sapu')).toBeInTheDocument()
    await user.type(screen.getByLabelText(text.searchLabel), 'galon')
    expect(await screen.findByText('Galon Aqua')).toBeInTheDocument()
    // The row's pos is named from the purpose list, not from balances.
    expect(screen.getAllByText('Kas Utama').length).toBeGreaterThan(0)

    expect(everything.toSorted()).toEqual(
      ['GET /api/members', 'GET /api/purposes', 'GET /api/transactions', 'GET /api/transactions?q=galon'].toSorted(),
    )
  })

  // The lookups are kept once loaded, but a failed load is not: Coba lagi
  // asks for them again rather than replaying the failure.
  it('asks for the lookups again on retry after they failed', async () => {
    let purposesDown = true
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = new URL(String(input), 'http://localhost')
        if (url.pathname === '/api/purposes') {
          if (purposesDown) throw new TypeError('Failed to fetch')
          return jsonResponse(purposes)
        }
        if (url.pathname === '/api/members') return jsonResponse({ members, next_cursor: null })
        if (url.pathname === '/api/transactions') return jsonResponse({ transactions: [row(6, 'Galon')], next_cursor: null })
        return jsonResponse({ error: { code: 'not_found', message: 'x' } }, 404)
      }),
    )
    const user = userEvent.setup()
    renderAt('/history/transactions')

    const retry = await screen.findByRole('button', { name: copy.common.retry })
    purposesDown = false
    await user.click(retry)
    expect(await screen.findByText('Galon')).toBeInTheDocument()
  })

  it('says nothing matched when a search comes back empty', async () => {
    stubApi(() => ({ transactions: [], next_cursor: null }))
    renderAt('/history/transactions?q=xyz')

    expect(await screen.findByText(text.noResults('xyz'))).toBeInTheDocument()
  })

  it('shows the connection state, never no-results, when a search cannot reach the server', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    renderAt('/history/transactions?q=xyz')

    expect(await screen.findByText(copy.common.errors.network_error)).toBeInTheDocument()
    expect(screen.queryByText(text.noResults('xyz'))).not.toBeInTheDocument()
    expect(screen.getByLabelText(text.searchLabel)).toHaveValue('xyz')
  })

  // The purpose filter, ADR-032's only route to a closed envelope's record. It
  // arrives as a link, never as a chooser this screen offers.
  it('reads ?purpose= from the URL, names the filter and sends it to the server', async () => {
    const requests = stubApi(() => ({ transactions: [row(1, 'Setoran')], next_cursor: null }))
    renderAt('/history/transactions?purpose=11')

    expect(await screen.findByText(text.purposeFilterLabel('Kas Utama'))).toBeInTheDocument()
    expect(requests[0].searchParams.get('purpose_id')).toBe('11')
  })

  it('ignores a ?purpose= that is not a real id rather than filtering on it', async () => {
    const requests = stubApi(() => ({ transactions: [row(1, 'Setoran')], next_cursor: null }))
    renderAt('/history/transactions?purpose=not-an-id')

    expect(await screen.findByText('Setoran')).toBeInTheDocument()
    expect(requests[0].searchParams.has('purpose_id')).toBe(false)
    expect(screen.queryByLabelText(text.filterClear(text.purposeFilterLabel('Kas Utama')))).not.toBeInTheDocument()
  })

  it('keeps the purpose filter when she searches inside it, and sends both', async () => {
    const user = userEvent.setup()
    const requests = stubApi(() => ({ transactions: [row(1, 'Setoran')], next_cursor: null }))
    renderAt('/history/transactions?purpose=11')
    await screen.findByText('Setoran')

    await user.type(screen.getByLabelText(text.searchLabel), 'kambing')

    // q is what the debounce writes, so it is the one worth waiting on;
    // purpose was already in the URL and is what must survive that write.
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('q=kambing'))
    expect(screen.getByTestId('location')).toHaveTextContent('purpose=11')
    await waitFor(() => expect(requests.length).toBe(2))
    expect(requests[1].searchParams.get('purpose_id')).toBe('11')
    expect(requests[1].searchParams.get('q')).toBe('kambing')
  })

  it('clears the filter from the URL without losing the search', async () => {
    const user = userEvent.setup()
    const requests = stubApi(() => ({ transactions: [row(1, 'Setoran')], next_cursor: null }))
    renderAt('/history/transactions?purpose=11&q=kambing')
    await screen.findByText(text.purposeFilterLabel('Kas Utama'))

    await user.click(screen.getByLabelText(text.filterClear(text.purposeFilterLabel('Kas Utama'))))

    await waitFor(() => expect(screen.getByTestId('location')).not.toHaveTextContent('purpose='))
    expect(screen.getByTestId('location')).toHaveTextContent('q=kambing')
    await waitFor(() => expect(requests.length).toBe(2))
    expect(requests[1].searchParams.has('purpose_id')).toBe(false)
    expect(requests[1].searchParams.get('q')).toBe('kambing')
  })

  it('carries the filter into the next page', async () => {
    const user = userEvent.setup()
    const requests = stubApi((url) =>
      url.searchParams.has('cursor')
        ? { transactions: [row(2, 'Kedua')], next_cursor: null }
        : { transactions: [row(1, 'Pertama')], next_cursor: 'abc' },
    )
    renderAt('/history/transactions?purpose=11')
    await screen.findByText('Pertama')

    await user.click(screen.getByRole('button', { name: text.loadMore }))

    expect(await screen.findByText('Kedua')).toBeInTheDocument()
    expect(requests[1].searchParams.get('purpose_id')).toBe('11')
  })

  it('says the envelope is empty, not that nothing matched, when the filter alone comes back empty', async () => {
    stubApi(() => ({ transactions: [], next_cursor: null }))
    renderAt('/history/transactions?purpose=11')

    expect(await screen.findByText(text.purposeFilterEmpty)).toBeInTheDocument()
  })
})

describe('Transactions tab: filters (#424)', () => {
  it('applies the dialog to the URL and the request, keeping the search', async () => {
    const user = userEvent.setup()
    const requests = stubApi(() => ({ transactions: [row(1, 'Setoran')], next_cursor: null }))
    renderAt('/history/transactions?q=kambing')
    await screen.findByText('Setoran')

    await user.click(screen.getByRole('button', { name: text.filterButton }))
    await chooseOption(text.filterMember, 'Sari')
    await chooseOption(copy.record.directionLabel, copy.record.directionOut)
    await user.click(screen.getByRole('button', { name: text.filterApply }))

    await waitFor(() => expect(requests.length).toBe(2))
    expect(requests[1].searchParams.get('member_id')).toBe('22')
    expect(requests[1].searchParams.get('direction')).toBe('out')
    expect(requests[1].searchParams.get('q')).toBe('kambing')
    const location = screen.getByTestId('location')
    expect(location).toHaveTextContent('member=22')
    expect(location).toHaveTextContent('dir=out')
    expect(location).not.toHaveTextContent('edit=')
    expect(screen.getByRole('button', { name: text.filterButtonActive(2) })).toBeInTheDocument()
  })

  it('names each filter as a chip and clears one without the others', async () => {
    const user = userEvent.setup()
    const requests = stubApi(() => ({ transactions: [row(1, 'Setoran')], next_cursor: null }))
    renderAt('/history/transactions?month=2026-09&member=21&dir=in')
    await screen.findByText('Setoran')
    expect(requests[0].searchParams.get('month')).toBe('2026-09')

    const monthChip = text.monthFilterLabel('September 2026')
    expect(screen.getByText(monthChip)).toBeInTheDocument()
    expect(await screen.findByText(text.memberFilterLabel('Budi'))).toBeInTheDocument()
    expect(screen.getByText(copy.record.directionIn)).toBeInTheDocument()

    await user.click(screen.getByLabelText(text.filterClear(monthChip)))

    await waitFor(() => expect(requests.length).toBe(2))
    expect(requests[1].searchParams.has('month')).toBe(false)
    expect(requests[1].searchParams.get('member_id')).toBe('21')
    expect(requests[1].searchParams.get('direction')).toBe('in')
  })

  it('ignores a month or direction that cannot be one', async () => {
    const requests = stubApi(() => ({ transactions: [row(1, 'Setoran')], next_cursor: null }))
    renderAt('/history/transactions?month=2026-13&dir=sideways')
    await screen.findByText('Setoran')

    expect(requests[0].searchParams.has('month')).toBe(false)
    expect(requests[0].searchParams.has('direction')).toBe(false)
    expect(screen.getByRole('button', { name: text.filterButton })).toBeInTheDocument()
  })

  it('says nothing matched the filters when they come back empty', async () => {
    stubApi(() => ({ transactions: [], next_cursor: null }))
    renderAt('/history/transactions?purpose=11&dir=out')

    expect(await screen.findByText(text.filterEmpty)).toBeInTheDocument()
  })
})

describe('Transactions tab: correcting a pos (#276, ADR-033)', () => {
  it("opens the dialog from the detail's Perbaiki pos and writes it to the URL", async () => {
    stubApi(() => ({ transactions: [row(5, 'Setoran seragam')], next_cursor: null }))
    const user = userEvent.setup()
    renderAt('/history/transactions')

    await screen.findByText('Setoran seragam')
    await user.click(screen.getByRole('button', { name: /^Lihat rincian:/ }))
    await user.click(
      within(await screen.findByRole('dialog', { name: copy.transactionDetail.heading })).getByRole('button', {
        name: copy.purposeCorrection.heading,
      }),
    )

    // The dialog is a search parameter, never component state (ADR-032), so
    // back and a deep link both agree with what is on screen.
    expect(await screen.findByText(copy.purposeCorrection.heading)).toBeInTheDocument()
    expect(screen.getByTestId('location').textContent).toContain('edit=purpose-correction%3A5')
  })

  it('posts the correction for the row named in the URL and reloads the list', async () => {
    const requests = stubApi(() => ({ transactions: [row(6, 'Setoran seragam')], next_cursor: null }))
    const user = userEvent.setup()
    renderAt('/history/transactions?edit=purpose-correction:6')

    await screen.findByText(copy.purposeCorrection.heading)
    const before = requests.length

    await chooseOption(copy.purposeCorrection.pickerLabel, 'Titipan')
    await user.click(screen.getByRole('button', { name: copy.purposeCorrection.save }))

    await waitFor(() => expect(posted).toHaveLength(1))
    expect(posted[0]).toMatchObject({ url: '/api/transactions/6/purpose-correction', body: { purpose_id: 12 } })

    // The list is refetched, because the row it corrected now carries a marker
    // and two new legs sit above it.
    await waitFor(() => expect(requests.length).toBeGreaterThan(before))
    expect(await screen.findByText(copy.purposeCorrection.success)).toBeInTheDocument()
  })

  it('says where the money actually is when the row has already been corrected', async () => {
    // The row underneath still shows its STORED tag - the ledger sums stored
    // tags (ADR-033) - so without this line the dialog and the row would
    // appear to disagree about the same entry.
    stubApi(() => ({
      transactions: [{ ...row(7, 'Setoran seragam'), purpose_id: 12, effective_purpose_id: 11 }],
      next_cursor: null,
    }))
    renderAt('/history/transactions?edit=purpose-correction:7')

    expect(await screen.findByText(copy.purposeCorrection.currentLabel('Kas Utama'))).toBeInTheDocument()
  })

  it('strips a dialog param naming a row this page does not hold', async () => {
    stubApi(() => ({ transactions: [row(8, 'Galon')], next_cursor: null }))
    renderAt('/history/transactions?edit=purpose-correction:999')

    await screen.findByText('Galon')
    await waitFor(() => expect(screen.getByTestId('location').textContent).not.toContain('edit='))
    expect(screen.queryByText(copy.purposeCorrection.heading)).not.toBeInTheDocument()
  })
})

describe('Transactions tab: reversing a row (ADR-042)', () => {
  it('reverses from the entry detail, shows the one success line, and reloads the list', async () => {
    const posts: { url: string; body: unknown }[] = []
    let reversedNow = false
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = new URL(String(input), 'http://localhost')
        if (url.pathname === '/api/purposes') return jsonResponse(purposes)
        if (url.pathname === '/api/members') return jsonResponse({ members, next_cursor: null })
        if (url.pathname.endsWith('/reversal') && init?.method === 'POST') {
          posts.push({ url: url.pathname, body: JSON.parse(String(init.body)) })
          reversedNow = true
          return jsonResponse({ ...row(99, ''), kind: 'adjustment' }, 201)
        }
        if (url.pathname === '/api/transactions') {
          return jsonResponse({
            transactions: [
              { ...row(5, 'Salah arah'), reversible: !reversedNow, reversed: reversedNow, counted: false },
            ] satisfies Transaction[],
            next_cursor: null,
          })
        }
        return jsonResponse({ error: { code: 'not_found', message: 'x' } }, 404)
      }),
    )
    const user = userEvent.setup()
    renderAt('/history/transactions')

    await user.click(await screen.findByRole('button', { name: /^Lihat rincian:/ }))
    await user.click(await screen.findByRole('button', { name: text.reverse }))
    await user.click(await screen.findByRole('button', { name: text.reverseConfirm }))

    expect(await screen.findByText('Transaksi dibatalkan.')).toBeInTheDocument()
    expect(posts).toEqual([{ url: '/api/transactions/5/reversal', body: { note: null } }])
    expect(await screen.findByText('Sudah dibatalkan')).toBeInTheDocument()
  })
})
