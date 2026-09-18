import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Roster from '@/screens/Members/Roster'
import { copy } from '@/copy/id'
import { chooseOption } from '@/test/select'

const text = copy.members.roster

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function member(
  id: number,
  name: string,
  overrides: Partial<{
    tier_id: number | null
    joined_on: string | null
    inactive_on: string | null
    tier_name: string | null
    current_rate: number | null
    arrears_months: number
  }> = {},
) {
  return {
    id,
    name,
    tier_id: 1,
    joined_on: '2026-01-05',
    inactive_on: null,
    created_at: 1,
    tier_name: 'Pelaksana',
    current_rate: 50_000,
    arrears_months: 0,
    ...overrides,
  }
}

const tiers = [
  { id: 1, name: 'Pelaksana', created_at: 1 },
  { id: 2, name: 'Fungsional', created_at: 1 },
]

/** One stub for the screen: GET /api/members answers the paged envelope
 * ({members, next_cursor}), GET /api/dues-tiers answers the picker's list,
 * and every write is recorded so a test can assert the exact body - which
 * matters here more than anywhere else in the app, because PATCH
 * /api/members/{id} reads an absent key and an explicit null as two
 * different instructions.
 *
 * `pages` lets a paging test hand back more than one page keyed by the
 * cursor requested; a plain array behaves as a single, unpaged page (no
 * next_cursor).
 */
function stubRoster(
  membersOrPages: ReturnType<typeof member>[] | Record<string, { members: ReturnType<typeof member>[]; next_cursor: string | null }>,
  writeResponse?: () => Response,
) {
  const calls: { method: string; url: string; body: Record<string, unknown> | undefined }[] = []
  const getCalls: string[] = []
  const paged = !Array.isArray(membersOrPages)

  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/dues-tiers')) return Promise.resolve(jsonResponse(tiers))
    if (url.includes('/api/members')) {
      if (method === 'GET') {
        getCalls.push(url)
        if (paged) {
          const parsed = new URL(url, 'http://localhost')
          const cursor = parsed.searchParams.get('cursor') ?? 'first'
          const page = membersOrPages[cursor] ?? { members: [], next_cursor: null }
          return Promise.resolve(jsonResponse(page))
        }
        return Promise.resolve(jsonResponse({ members: membersOrPages, next_cursor: null }))
      }
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(writeResponse ? writeResponse() : jsonResponse(member(9, 'Baru')))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls, getCalls }
}

/** Exposes the router's current search string, the same probe
 * Locations.test.tsx uses - the whole point of this screen's dialogs and
 * search is that the URL is the state. */
function SearchProbe() {
  const location = useLocation()
  return <output data-testid="roster-search">{location.search}</output>
}

function renderAt(entry = '/members') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/members"
          element={
            <>
              <Roster />
              <SearchProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

function currentSearch() {
  return screen.getByTestId('roster-search').textContent
}

describe('Roster', () => {
  it('lists every member with their tier and current rate, retired ones included', async () => {
    const { fetchMock } = stubRoster([
      member(1, 'Warga Satu'),
      member(2, 'Warga Dua', { tier_id: null, tier_name: null, current_rate: null, inactive_on: '2026-08-01' }),
    ])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    const cardOne = await screen.findByRole('button', { name: text.editAria('Warga Satu') })
    expect(within(cardOne).getByText(text.tierRateLine('Pelaksana', 'Rp 50.000'))).toBeInTheDocument()

    const cardTwo = screen.getByRole('button', { name: text.editAria('Warga Dua') })
    // A member with no tier owes no dues - a real state, shown as such
    // rather than as a blank, and with no rate appended.
    expect(within(cardTwo).getByText(text.tierNone)).toBeInTheDocument()
    expect(within(cardTwo).getByText(text.inactiveBadge)).toBeInTheDocument()
  })

  it('shows the "tarif belum ditentukan" line for a tier with no rate effective yet', async () => {
    const { fetchMock } = stubRoster([member(1, 'Warga Satu', { current_rate: null })])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    const card = await screen.findByRole('button', { name: text.editAria('Warga Satu') })
    expect(within(card).getByText(text.tierRateLine('Pelaksana', text.noRateYet))).toBeInTheDocument()
  })

  it('shows the Tunggakan badge only on a member with arrears', async () => {
    const { fetchMock } = stubRoster([
      member(1, 'Ada Tunggakan', { arrears_months: 2 }),
      member(2, 'Lunas Semua', { arrears_months: 0 }),
    ])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    const withArrears = await screen.findByRole('button', { name: text.editAria('Ada Tunggakan') })
    expect(within(withArrears).getByText(text.arrearsBadge(2))).toBeInTheDocument()

    const withoutArrears = screen.getByRole('button', { name: text.editAria('Lunas Semua') })
    expect(within(withoutArrears).queryByText(/Tunggakan/)).not.toBeInTheDocument()

    // The badge never borrows the current period's own vocabulary - a row
    // with no arrears is simply quiet, never labelled "Lunas" here.
    expect(within(withoutArrears).queryByText(copy.dues.statuses.paid)).not.toBeInTheDocument()
  })

  it('searches on the server, not by filtering the loaded rows', async () => {
    const { fetchMock, getCalls } = stubRoster({
      first: { members: [member(1, 'Budi')], next_cursor: null },
    })
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Budi') })

    await userEvent.type(screen.getByLabelText(text.searchLabel), 'Bud')

    await waitFor(() => expect(getCalls.some((url) => url.includes('q=Bud'))).toBe(true))
    await waitFor(() => expect(currentSearch()).toBe('?q=Bud'))
  })

  it('appends the next page on "muat lebih banyak" without a client-side filter', async () => {
    const { fetchMock } = stubRoster({
      first: { members: [member(1, 'Ana')], next_cursor: 'cursor-2' },
      'cursor-2': { members: [member(2, 'Budi')], next_cursor: null },
    })
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Ana') })
    expect(screen.queryByRole('button', { name: text.editAria('Budi') })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: text.loadMore }))

    expect(await screen.findByRole('button', { name: text.editAria('Budi') })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.editAria('Ana') })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: text.loadMore })).not.toBeInTheDocument()
  })

  it('opens the edit dialog from the card, addressed by ?edit=member:<id>', async () => {
    const { fetchMock } = stubRoster([member(1, 'Warga Satu')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Warga Satu') })

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Warga Satu') }))

    expect(currentSearch()).toBe('?edit=member%3A1')
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    expect(within(dialog).getByLabelText(text.nameLabel)).toHaveValue('Warga Satu')
  })

  it('adds a member via ?edit=member:new with a tier and a joined-on date', async () => {
    const { fetchMock, calls } = stubRoster([])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByText(text.empty)

    await userEvent.click(screen.getByRole('button', { name: text.add }))
    expect(currentSearch()).toBe('?edit=member%3Anew')

    const dialog = screen.getByRole('dialog', { name: text.add })
    await userEvent.type(within(dialog).getByLabelText(text.nameLabel), 'Warga Baru')
    await chooseOption(text.tierLabel, 'Fungsional', within(dialog))
    await userEvent.click(within(dialog).getByRole('button', { name: text.add }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', body: { name: 'Warga Baru', tier_id: 2 } })
    // joined_on defaults to today (#187: a fund's history starts at
    // adoption, so a new member owes from now, not from the past).
    expect(calls[0].body?.joined_on).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    await waitFor(() => expect(currentSearch()).toBe(''))
  })

  it('sends only the fields that changed when editing', async () => {
    const { fetchMock, calls } = stubRoster([member(1, 'Warga Satu')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Warga Satu') })

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Warga Satu') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    const input = within(dialog).getByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Warga Pertama')
    await userEvent.click(within(dialog).getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', body: { name: 'Warga Pertama' } })
    // Untouched fields are absent, not null - null would clear them.
    expect(calls[0].body).not.toHaveProperty('tier_id')
    expect(calls[0].body).not.toHaveProperty('joined_on')
  })

  it('clears the tier with an explicit null when "tanpa golongan" is chosen', async () => {
    const { fetchMock, calls } = stubRoster([member(1, 'Warga Satu')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Warga Satu') })

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Warga Satu') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    await chooseOption(text.tierLabel, text.tierNone, within(dialog))
    await userEvent.click(within(dialog).getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    // Explicitly null, not absent: this is "drop the dues obligation".
    expect(calls[0].body).toHaveProperty('tier_id', null)
  })

  it('deactivates only after the inline confirm, showing the consequence copy first', async () => {
    const { fetchMock, calls } = stubRoster([member(1, 'Warga Satu')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Warga Satu') })

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Warga Satu') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })

    await userEvent.click(within(dialog).getByRole('button', { name: text.deactivate }))
    expect(calls).toHaveLength(0)
    expect(within(dialog).getByText(text.deactivateConfirm)).toBeInTheDocument()

    await userEvent.click(within(dialog).getByRole('button', { name: text.deactivateConfirmAction }))
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0].method).toBe('PATCH')
    expect((calls[0].body as { inactive_on: string }).inactive_on).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })

  it('reinstates a retired member immediately, no confirm needed', async () => {
    const { fetchMock, calls } = stubRoster([member(1, 'Warga Satu', { inactive_on: '2026-08-01' })])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Warga Satu') })

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Warga Satu') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    await userEvent.click(within(dialog).getByRole('button', { name: text.reinstate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', body: { inactive_on: null } })
  })

  it('deletes only after the inline confirm', async () => {
    const { fetchMock, calls } = stubRoster([member(1, 'Duplikat')], () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Duplikat') })

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Duplikat') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })

    await userEvent.click(within(dialog).getByRole('button', { name: text.delete }))
    expect(calls).toHaveLength(0)
    expect(within(dialog).getByText(text.deleteConfirm)).toBeInTheDocument()

    await userEvent.click(within(dialog).getByRole('button', { name: text.deleteConfirmAction }))
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/members/1') })
  })

  it('renders the 409 on a member with history as a message pointing at deactivate', async () => {
    const { fetchMock } = stubRoster([member(1, 'Warga Satu')], () =>
      jsonResponse({ error: { code: 'referenced_by_other_records', message: 'referenced' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('button', { name: text.editAria('Warga Satu') })

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Warga Satu') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    await userEvent.click(within(dialog).getByRole('button', { name: text.delete }))
    await userEvent.click(within(dialog).getByRole('button', { name: text.deleteConfirmAction }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent(text.deleteRefused)
  })
})
