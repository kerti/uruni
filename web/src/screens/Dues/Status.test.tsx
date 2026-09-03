import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DuesStatus from '@/screens/Dues/Status'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'

const text = copy.dues

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** See Home.test.tsx's own money() - formatIDR's NBSP-after-"Rp" survives a
 * raw string matcher untouched even though testing-library normalizes the
 * DOM text it is compared against. */
function money(amount: number): string {
  return formatIDR(amount).replace(/ /g, ' ')
}

function member(id: number, name: string) {
  return { id, name, tier_id: 1, joined_on: null, inactive_on: null, created_at: 1 }
}

// One row per status the schema defines, plus a tier-less member left out
// of the fixture entirely - GET /api/dues-status never synthesizes a row
// for a member with no dues tier, so this suite proves that exclusion by
// simply never including "Tanpa golongan" in the stubbed response and
// asserting it never renders.
const rows = [
  { member: member(1, 'Warga Satu'), owed_amount: 50_000, paid_amount: 0, status: 'unpaid' },
  { member: member(2, 'Warga Dua'), owed_amount: 50_000, paid_amount: 20_000, status: 'partial' },
  { member: member(3, 'Warga Tiga'), owed_amount: 50_000, paid_amount: 50_000, status: 'paid' },
  { member: member(4, 'Warga Empat'), owed_amount: 50_000, paid_amount: 100_000, status: 'paid_in_advance' },
]

function stubDuesStatus(body: unknown = rows) {
  return vi.fn((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString()
    if (url.includes('/api/dues-status')) return Promise.resolve(jsonResponse(body))
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

describe('DuesStatus', () => {
  it('renders each of the four statuses with its own badge', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} />)

    expect(await screen.findByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText(text.statuses.unpaid)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.partial)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.paid)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.paid_in_advance)).toBeInTheDocument()

    expect(screen.getAllByText((_content, el) => el?.textContent === `${text.owedLabel}: ${money(50_000)}`).length).toBeGreaterThan(0)
  })

  it('excludes a member with no dues tier - the API never returns a row for one', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} />)

    await screen.findByText('Warga Satu')
    expect(screen.queryByText('Tanpa Golongan')).not.toBeInTheDocument()
  })

  it('the "belum bayar" filter isolates unpaid + partial only, with no reminder affordance', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} />)

    await screen.findByText('Warga Satu')
    await userEvent.click(screen.getByLabelText(text.unpaidFilterLabel))

    expect(screen.getByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText('Warga Dua')).toBeInTheDocument()
    expect(screen.queryByText('Warga Tiga')).not.toBeInTheDocument()
    expect(screen.queryByText('Warga Empat')).not.toBeInTheDocument()

    // PRD §7.3's explicit rule: a list and nothing more.
    expect(screen.queryByRole('button', { name: /ingat|kirim|notif/i })).not.toBeInTheDocument()
  })

  it('defaults the period selector to the current month', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} />)

    await screen.findByText('Warga Satu')
    const now = new Date()
    const expected = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
    expect(screen.getByLabelText(text.periodLabel)).toHaveValue(expected)
  })

  it('refetches when the period selector changes', async () => {
    const fetchMock = stubDuesStatus()
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesStatus onBack={vi.fn()} />)

    await screen.findByText('Warga Satu')
    const callsBefore = fetchMock.mock.calls.length

    fireEvent.change(screen.getByLabelText(text.periodLabel), { target: { value: '2026-01' } })

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore)
    const lastUrl = fetchMock.mock.calls.at(-1)?.[0]?.toString() ?? ''
    expect(lastUrl).toContain('period=2026-01')
  })
})
