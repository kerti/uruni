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
 * A stub that behaves like the server: renaming a tier changes what the
 * *next* GET /api/dues-tiers answers. That is what makes the test below a
 * real regression rather than a re-statement of the component's props - the
 * roster only shows the new name if it actually re-reads.
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
  return fetchMock
}

describe('Members screen', () => {
  // The bug this covers: the roster's tier picker and the tiers section load
  // the same rows separately, so renaming a tier below left the picker above
  // showing the old name until the screen was left and re-entered.
  it('refreshes the roster tier picker when a tier is renamed below it', async () => {
    vi.stubGlobal('fetch', stubMembersScreen())
    render(<Members />)

    // Both sections have loaded, and both render the tier's name - the
    // roster row shows its member's tier, the tiers section lists the tier
    // itself. Counted loosely on purpose: the exact number depends on how
    // many places render the label, which is not what this test is about.
    await waitFor(() => expect(screen.getAllByText('Pelaksana').length).toBeGreaterThan(1))

    // Rename it in the section below - the tier's row is the one carrying
    // the rates sub-list.
    const tierRow = within(screen.getAllByRole('listitem').find((item) => item.textContent?.includes(copy.members.tiers.ratesHeading))!)
    await userEvent.click(tierRow.getByRole('button', { name: copy.members.tiers.edit }))
    const input = tierRow.getByLabelText(copy.members.tiers.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Pelaksana Muda')
    await userEvent.click(tierRow.getByRole('button', { name: copy.members.tiers.save }))

    // The roster re-read: its member row now shows the new name, without the
    // screen having been left and re-entered.
    await waitFor(() => expect(screen.getAllByText('Pelaksana Muda').length).toBeGreaterThan(1))

    // And the picker itself offers it.
    const memberRow = within(screen.getAllByRole('listitem')[0])
    await userEvent.click(memberRow.getByRole('button', { name: copy.members.roster.edit }))
    const options = await openSelect(copy.members.roster.tierLabel, memberRow)
    expect(within(options).getByRole('option', { name: 'Pelaksana Muda' })).toBeInTheDocument()
  })
})
