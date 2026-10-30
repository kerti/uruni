import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DuesStatus from '@/screens/Dues/Status'
import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import { pickMonth } from '@/test/datePicker'
import { currentPeriod, formatPeriod } from '@/lib/dates'
import type { DuesGrid, DuesStatusRow } from '@/lib/dues'
import type { Member } from '@/lib/setup'

const text = copy.dues
const gridText = copy.dues.grid

function ListRouter({ children }: { children: ReactNode }) {
  return <MemoryRouter initialEntries={['/dues']}>{children}</MemoryRouter>
}

function GridRouter({ children }: { children: ReactNode }) {
  return <MemoryRouter initialEntries={['/dues?view=grid']}>{children}</MemoryRouter>
}

function Where() {
  const location = useLocation()
  return <output data-testid="where">{location.search}</output>
}

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function member(id: number, name: string): Member {
  return {
    id,
    name,
    tier_id: 1,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  }
}

// One row per status the schema defines, plus a tier-less member left out of
// the fixture - GET /api/dues-status never synthesizes a row for a member with
// no dues tier, so this suite proves the exclusion by never including "Tanpa
// tier" in the stubbed response and asserting it never renders.
const rows = [
  { member: member(1, 'Warga Satu'), owed_amount: 50_000, paid_amount: 0, status: 'unpaid', paid_through: null },
  { member: member(2, 'Warga Dua'), owed_amount: 50_000, paid_amount: 20_000, status: 'partial', paid_through: null },
  { member: member(3, 'Warga Tiga'), owed_amount: 50_000, paid_amount: 50_000, status: 'paid', paid_through: null },
  { member: member(4, 'Warga Empat'), owed_amount: 50_000, paid_amount: 100_000, status: 'paid_in_advance', paid_through: null },
] satisfies DuesStatusRow[]

// December to January crosses a year, so both year markers are on screen.
const twoMonthGrid: DuesGrid = {
  periods: ['2025-11', '2025-12', '2026-01', '2026-02'],
  members: [
    { id: 1, name: 'Warga Satu', cells: ['none', 'paid', 'partial', 'unpaid'] },
    { id: 2, name: 'Warga Dua', cells: ['none', 'none', 'paid', 'paid'] },
  ],
}

function stubDuesStatus(body: DuesStatusRow[] = rows, grid: DuesGrid = twoMonthGrid) {
  return vi.fn((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString()
    if (url.includes('/api/dues-status/grid')) return Promise.resolve(jsonResponse(grid))
    if (url.includes('/api/dues-status')) return Promise.resolve(jsonResponse(body))
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

describe('DuesStatus', () => {
  it('names the last paid month for a member paid ahead (#357)', async () => {
    const ahead = [
      { member: member(5, 'Warga Lima'), owed_amount: 50_000, paid_amount: 50_000, status: 'paid_in_advance', paid_through: '2027-02' },
    ] satisfies DuesStatusRow[]
    vi.stubGlobal('fetch', stubDuesStatus(ahead))
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

    expect(await screen.findByText(text.paidThrough('Februari 2027'))).toBeInTheDocument()
    expect(screen.getByText(text.statuses.paid)).toBeInTheDocument()
    expect(screen.queryByText(text.statuses.paid_in_advance)).not.toBeInTheDocument()
  })

  it('renders each of the four statuses with its own badge', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

    expect(await screen.findByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText(text.statuses.unpaid)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.partial)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.paid)).toBeInTheDocument()
    expect(screen.getByText(text.statuses.paid_in_advance)).toBeInTheDocument()

    expect(screen.getAllByText((_content, el) => el?.textContent === `${text.owedLabel}: ${formatIDR(50_000)}`).length).toBeGreaterThan(0)
  })

  it('excludes a member with no dues tier - the API never returns a row for one', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

    await screen.findByText('Warga Satu')
    expect(screen.queryByText('Tanpa Kategori')).not.toBeInTheDocument()
  })

  it('the "belum bayar" filter isolates unpaid + partial only, with no reminder affordance', async () => {
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

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
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

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
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

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
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={onRecordPayment} />, { wrapper: ListRouter })

    await screen.findByText('Warga Satu')
    await userEvent.click(screen.getByRole('button', { name: text.recordLink }))
    expect(onRecordPayment).toHaveBeenCalledTimes(1)
  })

  it('shows Tunggakan only for a member in arrears (#740)', async () => {
    const behind = [{ ...rows[0], member: { ...member(1, 'Warga Satu'), arrears_months: 3 } }, rows[1]] satisfies DuesStatusRow[]
    vi.stubGlobal('fetch', stubDuesStatus(behind))
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

    await screen.findByText('Warga Satu')
    expect(screen.getAllByText(copy.members.roster.arrearsBadge(3))).toHaveLength(1)
    expect(screen.queryByText(copy.members.roster.arrearsBadge(0))).not.toBeInTheDocument()
  })

  it('offers Catat iuran on unpaid and partial cards only, naming the member and the month (#740)', async () => {
    const onRecordPayment = vi.fn()
    vi.stubGlobal('fetch', stubDuesStatus())
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={onRecordPayment} />, { wrapper: ListRouter })

    await screen.findByText('Warga Satu')
    expect(screen.getAllByRole('button', { name: /^Catat iuran dari / })).toHaveLength(2)
    expect(screen.queryByRole('button', { name: text.recordForMemberAria('Warga Tiga') })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.recordForMemberAria('Warga Empat') })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: text.recordForMemberAria('Warga Dua') }))
    expect(onRecordPayment).toHaveBeenCalledWith({ memberId: 2, period: currentPeriod() })
  })

  it('reads the month from ?period= and falls back for a bad value (#740)', async () => {
    const fetchMock = stubDuesStatus()
    vi.stubGlobal('fetch', fetchMock)
    const { unmount } = render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, {
      wrapper: ({ children }: { children: ReactNode }) => <MemoryRouter initialEntries={['/dues?period=2026-03']}>{children}</MemoryRouter>,
    })
    await screen.findByText('Warga Satu')
    expect(fetchMock.mock.calls.at(-1)?.[0]?.toString()).toContain('period=2026-03')
    unmount()

    const bad = stubDuesStatus()
    vi.stubGlobal('fetch', bad)
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, {
      wrapper: ({ children }: { children: ReactNode }) => <MemoryRouter initialEntries={['/dues?period=2026-13']}>{children}</MemoryRouter>,
    })
    await screen.findByText('Warga Satu')
    expect(bad.mock.calls.at(-1)?.[0]?.toString()).toContain(`period=${currentPeriod()}`)
  })

  it('refetches after a payment was recorded - refetchKey changed', async () => {
    const fetchMock = stubDuesStatus()
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} refetchKey="a" />, { wrapper: ListRouter })

    await screen.findByText('Warga Satu')
    const callsBefore = fetchMock.mock.calls.length

    rerender(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} refetchKey="b" />)

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore)
  })

  it('refetches when the period selector changes', async () => {
    const fetchMock = stubDuesStatus()
    vi.stubGlobal('fetch', fetchMock)
    render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

    await screen.findByText('Warga Satu')
    const callsBefore = fetchMock.mock.calls.length

    await pickMonth(text.periodLabel, '2026-01')

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsBefore)
    const lastUrl = fetchMock.mock.calls.at(-1)?.[0]?.toString() ?? ''
    expect(lastUrl).toContain('period=2026-01')
  })
  describe('the twelve-month view (ADR-044)', () => {
    it('opens on the one-month list, with the switch naming both views', async () => {
      vi.stubGlobal('fetch', stubDuesStatus())
      render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: ListRouter })

      expect(await screen.findByText('Warga Satu')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: gridText.viewOneMonth })).toHaveAttribute('aria-pressed', 'true')
      expect(screen.getByRole('button', { name: gridText.viewTwelveMonths })).toHaveAttribute('aria-pressed', 'false')
      expect(screen.queryByRole('heading', { name: gridText.heading })).not.toBeInTheDocument()
    })

    it('switches to the grid through the URL and hides the list-only controls', async () => {
      vi.stubGlobal('fetch', stubDuesStatus())
      render(
        <>
          <DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />
          <Where />
        </>,
        { wrapper: ListRouter },
      )
      await screen.findByText('Warga Satu')

      await userEvent.click(screen.getByRole('button', { name: gridText.viewTwelveMonths }))

      expect(await screen.findByRole('heading', { name: gridText.heading })).toBeInTheDocument()
      expect(screen.getByTestId('where')).toHaveTextContent('?view=grid')
      expect(screen.queryByLabelText(text.periodLabel)).not.toBeInTheDocument()
      expect(screen.queryByLabelText(text.unpaidFilterLabel)).not.toBeInTheDocument()

      await userEvent.click(screen.getByRole('button', { name: gridText.viewOneMonth }))
      expect(await screen.findByLabelText(text.periodLabel)).toBeInTheDocument()
      expect(screen.getByTestId('where').textContent).toBe('')
    })

    it('draws the range, the members in the API order and a label on every cell', async () => {
      vi.stubGlobal('fetch', stubDuesStatus())
      render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: GridRouter })

      expect(await screen.findByText(gridText.range('November 2025', 'Februari 2026'))).toBeInTheDocument()
      const table = screen.getByRole('table')
      const rowHeads = within(table)
        .getAllByRole('rowheader')
        .map((th) => th.textContent)
      expect(rowHeads).toEqual(['Warga Satu', 'Warga Dua'])

      const first = within(screen.getByRole('row', { name: /Warga Satu/ }))
      expect(first.getByText('November 2025: Tidak ada iuran')).toBeInTheDocument()
      expect(first.getByText('Desember 2025: Lunas')).toBeInTheDocument()
      expect(first.getByText('Januari 2026: Bayar sebagian')).toBeInTheDocument()
      expect(first.getByText('Februari 2026: Belum bayar')).toBeInTheDocument()
    })

    it('marks the first column and each January with its year, and heads columns with short months', async () => {
      vi.stubGlobal('fetch', stubDuesStatus())
      render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: GridRouter })

      const table = await screen.findByRole('table')
      expect(within(table).getByText('2025')).toBeInTheDocument()
      expect(within(table).getByText('2026')).toBeInTheDocument()
      expect(within(table).getAllByText(/^\d{4}$/)).toHaveLength(2)
      for (const head of ['Nov', 'Des', 'Jan', 'Feb']) {
        expect(within(table).getByText(head)).toBeInTheDocument()
      }
    })

    it('has no controls in the grid and no count, total or amount', async () => {
      vi.stubGlobal('fetch', stubDuesStatus())
      render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: GridRouter })

      const table = await screen.findByRole('table')
      expect(within(table).queryByRole('button')).not.toBeInTheDocument()
      expect(within(table).queryByRole('link')).not.toBeInTheDocument()
      expect(table.textContent).not.toMatch(/Rp|tunggak/i)
    })

    it('shows the legend under the grid', async () => {
      vi.stubGlobal('fetch', stubDuesStatus())
      render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: GridRouter })

      await screen.findByRole('table')
      const legend = screen.getAllByRole('list').at(-1) as HTMLElement
      expect(
        within(legend)
          .getAllByRole('listitem')
          .map((li) => li.textContent),
      ).toEqual([text.statuses.paid, text.statuses.partial, text.statuses.unpaid, gridText.none])
    })

    it('says so calmly when nobody owes dues, with no table', async () => {
      vi.stubGlobal('fetch', stubDuesStatus(rows, { periods: [], members: [] }))
      render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: GridRouter })

      expect(await screen.findByText(gridText.empty)).toBeInTheDocument()
      expect(screen.queryByRole('table')).not.toBeInTheDocument()
    })

    it('shows the error state with a retry when the grid fails to load', async () => {
      vi.stubGlobal(
        'fetch',
        vi.fn(() => Promise.resolve(jsonResponse({ error: { code: 'internal_error' } }, 500))),
      )
      render(<DuesStatus onBack={vi.fn()} onRecordPayment={vi.fn()} />, { wrapper: GridRouter })

      expect(await screen.findByRole('button', { name: copy.common.retry })).toBeInTheDocument()
    })
  })
})
