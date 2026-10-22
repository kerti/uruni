import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import MemberPayments from '@/screens/Dues/MemberPayments'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { Transaction } from '@/lib/setup'

const text = copy.dues.history

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function transaction(overrides: Partial<Transaction>): Transaction {
  return {
    id: 1,
    account_id: 7,
    purpose_id: 3,
    direction: 'in',
    amount: 50_000,
    occurred_on: '2026-01-05',
    kind: 'dues',
    member_id: 1,
    dues_period: '2026-01',
    reimbursement_id: null,
    transfer_id: null,
    reverses_transaction_id: null,
    note: 'Iuran - Warga Satu',
    created_at: 1,
    ...overrides,
  }
}

// One payment for this member and period, one for another period, one for
// another member, and a kind='normal' row - only the first belongs here. The
// member_id/dues_period filter is server-side, so this fixture is the
// *unfiltered* set the stub below filters from, as the real
// GET /api/transactions?member_id=&dues_period= would.
const rows = [
  transaction({ id: 10 }),
  transaction({ id: 11, dues_period: '2026-02' }),
  transaction({ id: 12, member_id: 2 }),
  transaction({ id: 13, kind: 'normal', member_id: null, dues_period: null }),
]

// stubTransactions filters `body` by the request's member_id/dues_period
// query params before answering, mirroring the server - a test against this
// proves MemberPayments passes those params through, not that it filters
// client-side (illegal for a paged list).
function stubTransactions(body: Transaction[] = rows) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    if (url.includes('/reversal') && init?.method === 'POST') {
      return Promise.resolve(jsonResponse(transaction({ id: 99, kind: 'adjustment' }), 201))
    }
    if (url.includes('/api/transactions')) {
      const params = new URL(url, 'http://localhost').searchParams
      const memberId = params.get('member_id')
      const duesPeriod = params.get('dues_period')
      const filtered = body.filter(
        (t) => (memberId === null || String(t.member_id) === memberId) && (duesPeriod === null || t.dues_period === duesPeriod),
      )
      return Promise.resolve(jsonResponse({ transactions: filtered, next_cursor: null }))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

/** formatIDR puts a non-breaking space after "Rp", which testing-library's
 * string matchers normalize away - this one reads el.textContent, so the NBSP
 * is compared as-is. */
function amountMatcher(amount: number) {
  return (_content: string, el: Element | null) => el?.tagName === 'SPAN' && el.textContent === formatIDR(amount)
}

function renderPayments(onReversed = vi.fn()) {
  render(<MemberPayments memberId={1} period="2026-01" onReversed={onReversed} />)
  return onReversed
}

describe('MemberPayments', () => {
  it("shows only this member's rows for this period", async () => {
    vi.stubGlobal('fetch', stubTransactions())
    renderPayments()

    expect(await screen.findByText(amountMatcher(50_000))).toBeInTheDocument()
    // The other three rows are for another period, another member, or not a
    // dues row at all.
    expect(screen.getAllByText(amountMatcher(50_000))).toHaveLength(1)
  })

  it('reverses a payment, sending null when she leaves the reason blank', async () => {
    const fetchMock = stubTransactions()
    vi.stubGlobal('fetch', fetchMock)
    const onReversed = renderPayments()

    await userEvent.click(await screen.findByRole('button', { name: text.reverse }))
    await userEvent.click(screen.getByRole('button', { name: text.confirm }))

    await waitFor(() => expect(onReversed).toHaveBeenCalledTimes(1))
    const post = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'POST')
    expect(post?.[0]?.toString()).toBe('/api/transactions/10/reversal')
    const body = JSON.parse(String((post?.[1] as RequestInit | undefined)?.body))
    // ADR-029, ADR-042: only a note ever crosses the wire - the date is the
    // ledger's to pick.
    expect(Object.keys(body)).toEqual(['note'])
    // Nothing generated is ever stored - a blank reason sends null, never a
    // derived default. The row explains itself through TransactionList's
    // display label.
    expect(body.note).toBeNull()
  })

  it("states the original's date on the form when no cek kas has counted the payment", async () => {
    vi.stubGlobal('fetch', stubTransactions([transaction({ id: 10, counted: false })]))
    renderPayments()

    await userEvent.click(await screen.findByRole('button', { name: text.reverse }))

    expect(screen.getByText(copy.history.transactions.reverseDateOriginal('5 Januari 2026'))).toBeInTheDocument()
    expect(screen.getByText('Dicatat pada 5 Januari 2026.')).toBeInTheDocument()
  })

  it('says it is recorded today once a cek kas has counted the payment', async () => {
    vi.stubGlobal('fetch', stubTransactions([transaction({ id: 10, counted: true })]))
    renderPayments()

    await userEvent.click(await screen.findByRole('button', { name: text.reverse }))

    expect(screen.getByText('Dicatat hari ini, karena kas sudah dicek sejak transaksi ini.')).toBeInTheDocument()
    expect(screen.queryByText(/^Dicatat pada/)).not.toBeInTheDocument()
  })

  it("sends the treasurer's own reason when she gives one", async () => {
    const fetchMock = stubTransactions()
    vi.stubGlobal('fetch', fetchMock)
    renderPayments()

    await userEvent.click(await screen.findByRole('button', { name: text.reverse }))
    await userEvent.type(screen.getByLabelText(text.noteLabel), 'Salah orang')
    await userEvent.click(screen.getByRole('button', { name: text.confirm }))

    await waitFor(() => expect(fetchMock.mock.calls.some(([, i]) => (i as RequestInit | undefined)?.method === 'POST')).toBe(true))
    const post = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'POST')
    expect(JSON.parse(String((post?.[1] as RequestInit | undefined)?.body)).note).toBe('Salah orang')
  })

  it('never offers to reverse a payment twice, and shows the reversal as its own row', async () => {
    vi.stubGlobal(
      'fetch',
      stubTransactions([
        transaction({ id: 10 }),
        transaction({ id: 99, kind: 'adjustment', direction: 'out', reverses_transaction_id: 10, note: 'Salah orang' }),
      ]),
    )
    renderPayments()

    expect(await screen.findByText(text.reversedBadge)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.reverse })).not.toBeInTheDocument()
    // The correction is visible, never a silent removal.
    expect(screen.getByText(new RegExp(text.reversalRow))).toBeInTheDocument()
  })

  it('says so when the member has no payment for this period', async () => {
    vi.stubGlobal('fetch', stubTransactions([]))
    renderPayments()

    expect(await screen.findByText(text.empty)).toBeInTheDocument()
  })
})
