import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import MemberPayments from '@/screens/Dues/MemberPayments'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'

const text = copy.dues.history

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function transaction(overrides: Record<string, unknown>) {
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
    note: 'Iuran — Warga Satu',
    created_at: 1,
    ...overrides,
  }
}

// One payment for this member and period, one for another period, one for
// another member, and a kind='normal' row - only the first belongs here.
const rows = [
  transaction({ id: 10 }),
  transaction({ id: 11, dues_period: '2026-02' }),
  transaction({ id: 12, member_id: 2 }),
  transaction({ id: 13, kind: 'normal', member_id: null, dues_period: null }),
]

function stubTransactions(body: unknown = rows) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    if (url.includes('/reversal') && init?.method === 'POST') {
      return Promise.resolve(jsonResponse(transaction({ id: 99, kind: 'adjustment' }), 201))
    }
    if (url.includes('/api/transactions')) return Promise.resolve(jsonResponse(body))
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

/** formatIDR puts a non-breaking space after "Rp", which testing-library's
 * own string matchers normalize away - this one reads el.textContent, so the
 * NBSP is compared as-is. Same helper shape as Status.test.tsx's. */
function amountMatcher(amount: number) {
  return (_content: string, el: Element | null) => el?.tagName === 'SPAN' && el.textContent === formatIDR(amount)
}

function renderPayments(onReversed = vi.fn()) {
  render(<MemberPayments memberId={1} memberName="Warga Satu" period="2026-01" onReversed={onReversed} />)
  return onReversed
}

describe('MemberPayments', () => {
  it('shows only this member\'s rows for this period', async () => {
    vi.stubGlobal('fetch', stubTransactions())
    renderPayments()

    expect(await screen.findByText(amountMatcher(50_000))).toBeInTheDocument()
    // The other three rows are for another period, another member, or not a
    // dues row at all.
    expect(screen.getAllByText(amountMatcher(50_000))).toHaveLength(1)
  })

  it('reverses a payment with a date and a derived note, then tells the roster', async () => {
    const fetchMock = stubTransactions()
    vi.stubGlobal('fetch', fetchMock)
    const onReversed = renderPayments()

    await userEvent.click(await screen.findByRole('button', { name: text.reverse }))
    await userEvent.click(screen.getByRole('button', { name: text.confirm }))

    await waitFor(() => expect(onReversed).toHaveBeenCalledTimes(1))
    const post = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'POST')
    expect(post?.[0]?.toString()).toBe('/api/dues-payments/10/reversal')
    const body = JSON.parse(String((post?.[1] as RequestInit).body))
    // ADR-029: only a date and a note ever cross the wire.
    expect(Object.keys(body).sort()).toEqual(['note', 'occurred_on'])
    // Never empty - the treasurer left the reason blank, so the row still
    // says what it is.
    expect(body.note).toBe(text.note('Warga Satu'))
  })

  it('sends the treasurer\'s own reason when she gives one', async () => {
    const fetchMock = stubTransactions()
    vi.stubGlobal('fetch', fetchMock)
    renderPayments()

    await userEvent.click(await screen.findByRole('button', { name: text.reverse }))
    await userEvent.type(screen.getByLabelText(text.noteLabel), 'Salah orang')
    await userEvent.click(screen.getByRole('button', { name: text.confirm }))

    await waitFor(() => expect(fetchMock.mock.calls.some(([, i]) => (i as RequestInit | undefined)?.method === 'POST')).toBe(true))
    const post = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'POST')
    expect(JSON.parse(String((post?.[1] as RequestInit).body)).note).toBe('Salah orang')
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
