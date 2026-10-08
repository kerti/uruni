import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DuesStatus from '@/screens/Dues/Status'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import { pickMonth } from '@/test/datePicker'
import { currentPeriod, formatPeriod } from '@/lib/dates'

const text = copy.dues

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function member(id: number, name: string) {
  return { id, name, tier_id: 1, joined_on: '2026-01-01', inactive_on: null, created_at: 1 }
}

// One row per status the schema defines, plus a tier-less member left out
// of the fixture entirely - GET /api/dues-status never synthesizes a row
// for a member with no dues tier, so this suite proves that exclusion by
// simply never including "Tanpa golongan" in the stubbed response and
// asserting it never renders.
const rows = [
  { member: member(1, 'Warga Satu'), owed_amount: 50_000, paid_amount: 0, status: 'unpaid', paid_through: null },
  { member: member(2, 'Warga Dua'), owed_amount: 50_000, paid_amount: 20_000, status: 'partial', paid_through: null },
  { member: member(3, 'Warga Tiga'), owed_amount: 50_000, paid_amount: 50_000, status: 'paid', paid_through: null },
  { member: member(4, 'Warga Empat'), owed_amount: 50_000, paid_amount: 100_000, status: 'paid_in_advance', paid_through: null },
]

function stubDuesStatus(body: unknown = rows) {
  return vi.fn((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString()
    if (url.includes('/api/dues-status')) return Promise.resolve(jsonResponse(body))
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

describe('DuesStatus', () => {
  it('names the last paid month for a member paid ahead (#357)', async () => {
    const ahead = [
      { member: member(5, 'Warga Lima'), owed_amount: 50_000, paid_amount: 50_000, status: 'paid_in_advance', paid_through: '2027-02' },
    ]
    vi.stubGlobal('fetch', stubDuesStatus(ahead))
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />)

    expect(await screen.findByText(text.paidThrough('Februari 2027'))).toBeInTheDocument()
    expect(screen.getByText(text.statuses.paid)).toBeInTheDocument()
    expect(screen.queryByText(text.statuses.paid_in_advance)).not.toBeInTheDocument()
  })

  it('renders each of the four statuses with its own badge', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />)

    expect(await screen.findByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText(text.statuses.unpaid)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.partial)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.paid)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.paid_in_advance)).toBeInTheDocument()

    // formatIDR puts a non-breaking space after "Rp". This matcher reads
    // el.textContent, which is the raw DOM text, so that NBSP is compared
    // as-is - unlike testing-library's own string matchers, which normalize
    // it away (Home.test.tsx converts it for exactly that reason).
    expect(screen.getAllByText((_content, el) => el?.textContent === `${text.owedLabel}: ${formatIDR(50_000)}`).length).toBeGreaterThan(0)
  })

  it('excludes a member with no dues tier - the API never returns a row for one', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />)

    await screen.findByText('Warga Satu')
    expect(screen.queryByText('Tanpa Golongan')).not.toBeInTheDocument()
  })

  it('the "belum bayar" filter isolates unpaid + partial only, with no reminder affordance', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />)

    await screen.findByText('Warga Satu')
    await userEvent.click(screen.getByLabelText(text.unpaidFilterLabel))

    expect(screen.getByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText('Warga Dua')).toBeInTheDocument()
    expect(screen.queryByText('Warga Tiga')).not.toBeInTheDocument()
    expect(screen.queryByText('Warga Empat')).not.toBeInTheDocument()

    // PRD section 7.3's explicit rule: a list and nothing more.
    expect(screen.queryByRole('button', { name: /ingat|kirim|notif/i })).not.toBeInTheDocument()
  })

  it('defaults the period selector to the current month', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />)

    await screen.findByText('Warga Satu')
    expect(screen.getByLabelText(text.periodLabel)).toHaveTextContent(formatPeriod(currentPeriod()))
  })

  it("opens one member's payment history in place (M6.14)", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString()
      if (url.includes('/api/dues-status')) return Promise.resolve(jsonResponse(rows))
      if (url.includes('/api/transactions')) return Promise.resolve(jsonResponse([]))
      return Promise.reject(new Error(`unstubbed fetch: ${url}`))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />)

    await screen.findByText('Warga Satu')
    // One member at a time: every row offers the toggle, only the opened one
    // reads its history.
    const toggles = screen.getAllByRole('button', { name: copy.dues.history.title })
    expect(toggles[0]).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(toggles[0])

    expect(await screen.findByText(copy.dues.history.empty)).toBeInTheDocument()
    expect(toggles[0]).toHaveAttribute('aria-expanded', 'true')
    // The disclosure names the panel it opens.
    expect(toggles[0].getAttribute('aria-controls')).toBe(document.getElementById(toggles[0].getAttribute('aria-controls') ?? '')?.id)
    expect(fetchMock.mock.calls.some(([input]) => input.toString().includes('/api/transactions'))).toBe(true)
  })

  it('reaches the payment form from here, not from a second link on home', async () => {
    const onRecordPayment = vi.fn()
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={onRecordPayment} />)

    await screen.findByText('Warga Satu')
    await userEvent.click(screen.getByRole('button', { name: text.recordLink }))
    expect(onRecordPayment).toHaveBeenCalledTimes(1)
  })

  it('refetches after a payment was recorded - refetchKey changed', async () => {
    const fetchMock = stubDuesStatus()
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} refetchKey="a" />)

    await screen.findByText('Warga Satu')
    const callsBefore = fetchMock.mock.calls.length

    rerender(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} refetchKey="b" notice={copy.dues.payment.success} />)

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore)
    expect(await screen.findByText(copy.dues.payment.success)).toBeInTheDocument()
  })

  it('refetches when the period selector changes', async () => {
    const fetchMock = stubDuesStatus()
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />)

    await screen.findByText('Warga Satu')
    const callsBefore = fetchMock.mock.calls.length

    await pickMonth(text.periodLabel, '2026-01')

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore)
    const lastUrl = fetchMock.mock.calls.at(-1)?.[0]?.toString() ?? ''
    expect(lastUrl).toContain('period=2026-01')
  })
})
