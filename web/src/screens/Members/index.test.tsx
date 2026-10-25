import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Members from '@/screens/Members'
import { copy } from '@/copy/id'
import { openSelect } from '@/test/select'
import type { DuesTier, DuesTierWithRates, Member } from '@/lib/setup'
import { cardName } from '@/test/card'

// Roster.tsx reads and writes `?q=`/`?edit=` through useSearchParams (ADR-032),
// so this screen needs a Router in its tree even where a test never looks at
// the URL.
function renderMembers() {
  return render(
    <MemoryRouter initialEntries={['/members']}>
      <Members />
    </MemoryRouter>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/**
 * A stub that behaves like the server, with a handle on the tier's name so a
 * test can change it as Pengaturan would - out of band, while this screen is
 * not looking.
 */
function stubMembersScreen() {
  let tierName = 'Pelaksana'
  const members = [
    {
      id: 1,
      name: 'Warga Satu',
      tier_id: 1,
      joined_on: '2026-01-05',
      inactive_on: null,
      created_at: 1,
      tier_name: tierName,
      current_rate: 50_000,
      arrears_months: 0,
    },
  ] satisfies Member[]

  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'

    if (url.includes('/rates')) return Promise.resolve(jsonResponse([]))
    if (url.includes('/api/dues-tiers')) {
      if (method === 'GET')
        return Promise.resolve(jsonResponse([{ id: 1, name: tierName, created_at: 1, rates: [] }] satisfies DuesTierWithRates[]))
      tierName = JSON.parse(String(init?.body)).name as string
      return Promise.resolve(jsonResponse({ id: 1, name: tierName, created_at: 1 } satisfies DuesTier))
    }
    if (url.includes('/api/members')) {
      // The roster card renders the tier_name the row carries, not a second
      // lookup against the tiers list - so the stub echoes the name current at
      // the GET, as the server would after a rename elsewhere.
      return Promise.resolve(jsonResponse({ members: members.map((m) => ({ ...m, tier_name: tierName })), next_cursor: null }))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return {
    fetchMock,
    renameTierElsewhere: (name: string) => {
      tierName = name
    },
  }
}

describe('Members screen', () => {
  // The roster reads members AND tiers on mount, and reaching Anggota from
  // Pengaturan is a navigation, so the picker cannot show a name the tier no
  // longer has.
  it('re-reads the tiers on mount, so a tier renamed in Pengaturan is current here', async () => {
    const { fetchMock, renameTierElsewhere } = stubMembersScreen()
    vi.stubGlobal('fetch', fetchMock)

    const first = renderMembers()
    await waitFor(() => expect(screen.getAllByText(/Pelaksana/).length).toBeGreaterThan(0))

    // Renamed in Pengaturan, which this screen never saw happen.
    first.unmount()
    renameTierElsewhere('Pelaksana Muda')

    renderMembers()

    const card = await screen.findByRole('button', { name: cardName(copy.members.roster.editVerb, 'Warga Satu') })
    const renamedLine = copy.members.roster.tierRateLine('Pelaksana Muda', 'Rp 50.000')
    const staleLine = copy.members.roster.tierRateLine('Pelaksana', 'Rp 50.000')
    await waitFor(() => expect(within(card).getByText(renamedLine)).toBeInTheDocument())
    expect(within(card).queryByText(staleLine)).not.toBeInTheDocument()

    // And the picker offers the new name, not the old one.
    await userEvent.click(card)
    const dialog = screen.getByRole('dialog', { name: copy.members.roster.editTitle })
    const options = await openSelect(copy.members.roster.tierLabel, within(dialog))
    expect(within(options).getByRole('option', { name: 'Pelaksana Muda' })).toBeInTheDocument()
  })

  // The screen is the roster alone - the tiers section is not on it.
  it('does not render the tiers section', async () => {
    const { fetchMock } = stubMembersScreen()
    vi.stubGlobal('fetch', fetchMock)
    renderMembers()

    await waitFor(() => expect(screen.getAllByText(/Pelaksana/).length).toBeGreaterThan(0))
    expect(screen.queryByText(copy.settings.tiers.heading)).not.toBeInTheDocument()
    expect(screen.queryByText(copy.settings.tiers.ratesHeading)).not.toBeInTheDocument()
  })
})
