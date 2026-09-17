import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Members from '@/screens/Members'
import { copy } from '@/copy/id'
import { openSelect } from '@/test/select'

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/**
 * A stub that behaves like the server, with a handle on the tier's name so a
 * test can change it the way Pengaturan would - out of band, while this
 * screen is not looking.
 */
function stubMembersScreen() {
  let tierName = 'Pelaksana'
  const members = [{ id: 1, name: 'Warga Satu', tier_id: 1, joined_on: '2026-01-05', inactive_on: null, created_at: 1 }]

  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'

    if (url.includes('/rates')) return Promise.resolve(jsonResponse([]))
    if (url.includes('/api/dues-tiers')) {
      if (method === 'GET') return Promise.resolve(jsonResponse([{ id: 1, name: tierName, created_at: 1 }]))
      tierName = JSON.parse(String(init?.body)).name as string
      return Promise.resolve(jsonResponse({ id: 1, name: tierName, created_at: 1 }))
    }
    if (url.includes('/api/members')) return Promise.resolve(jsonResponse(members))
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, renameTierElsewhere: (name: string) => { tierName = name } }
}

describe('Members screen', () => {
  // #232 moved the tiers section to Pengaturan, and `tiersVersion` went with
  // it: the counter existed only because the two shared one screen. What
  // replaces it is this - the roster reads members AND tiers on mount, and
  // reaching Anggota from Pengaturan is a navigation, so the picker cannot
  // show a name the tier no longer has.
  it('re-reads the tiers on mount, so a tier renamed in Pengaturan is current here', async () => {
    const { fetchMock, renameTierElsewhere } = stubMembersScreen()
    vi.stubGlobal('fetch', fetchMock)

    const first = render(<Members />)
    await waitFor(() => expect(screen.getAllByText('Pelaksana').length).toBeGreaterThan(0))

    // Renamed in Pengaturan, which this screen never saw happen.
    first.unmount()
    renameTierElsewhere('Pelaksana Muda')

    render(<Members />)

    await waitFor(() => expect(screen.getAllByText('Pelaksana Muda').length).toBeGreaterThan(0))
    expect(screen.queryByText('Pelaksana')).not.toBeInTheDocument()

    // And the picker offers the new name, not the old one.
    const memberRow = within(screen.getAllByRole('listitem')[0])
    await userEvent.click(memberRow.getByRole('button', { name: copy.members.roster.edit }))
    const options = await openSelect(copy.members.roster.tierLabel, memberRow)
    expect(within(options).getByRole('option', { name: 'Pelaksana Muda' })).toBeInTheDocument()
  })

  // The screen is the roster alone now - the tiers section is not on it.
  it('does not render the tiers section', async () => {
    const { fetchMock } = stubMembersScreen()
    vi.stubGlobal('fetch', fetchMock)
    render(<Members />)

    await waitFor(() => expect(screen.getAllByText('Pelaksana').length).toBeGreaterThan(0))
    expect(screen.queryByText(copy.settings.tiers.heading)).not.toBeInTheDocument()
    expect(screen.queryByText(copy.settings.tiers.ratesHeading)).not.toBeInTheDocument()
  })
})
