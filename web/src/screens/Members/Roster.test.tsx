import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

function member(id: number, name: string, tierId: number | null = 1, inactiveOn: string | null = null) {
  return { id, name, tier_id: tierId, joined_on: '2026-01-05', inactive_on: inactiveOn, created_at: 1 }
}

const tiers = [
  { id: 1, name: 'Pelaksana', created_at: 1 },
  { id: 2, name: 'Fungsional', created_at: 1 },
]

/** One stub for the section: GET answers the roster and the tiers, and every
 * write is recorded so a test can assert the exact body - which matters here
 * more than anywhere else in the app, because PATCH /api/members/{id} reads
 * an absent key and an explicit null as two different instructions. */
function stubRoster(members: ReturnType<typeof member>[], writeResponse?: () => Response) {
  const calls: { method: string; url: string; body: Record<string, unknown> | undefined }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/dues-tiers')) return Promise.resolve(jsonResponse(tiers))
    if (url.includes('/api/members')) {
      if (method === 'GET') return Promise.resolve(jsonResponse(members))
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(writeResponse ? writeResponse() : jsonResponse(member(9, 'Baru')))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

describe('Roster', () => {
  it('lists every member with their tier, retired ones included', async () => {
    const { fetchMock } = stubRoster([member(1, 'Warga Satu'), member(2, 'Warga Dua', null, '2026-08-01')])
    vi.stubGlobal('fetch', fetchMock)
    render(<Roster tiersVersion={0} />)

    expect(await screen.findByText('Warga Satu')).toBeInTheDocument()
    expect(screen.getByText('Warga Dua')).toBeInTheDocument()
    // A member with no tier owes no dues - a real state, shown as such
    // rather than as a blank.
    expect(screen.getAllByText(text.tierNone).length).toBeGreaterThan(0)
    expect(screen.getByText(text.inactiveBadge)).toBeInTheDocument()
  })

  it('adds a member with a tier and a joined-on date', async () => {
    const { fetchMock, calls } = stubRoster([])
    vi.stubGlobal('fetch', fetchMock)
    render(<Roster tiersVersion={0} />)
    await screen.findByText(text.empty)

    const form = within(screen.getByRole('form', { name: text.add }))
    await userEvent.type(form.getByLabelText(text.nameLabel), 'Warga Baru')
    await chooseOption(text.tierLabel, 'Fungsional', form)
    await userEvent.click(form.getByRole('button', { name: text.add }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', body: { name: 'Warga Baru', tier_id: 2 } })
    // joined_on defaults to today (#187: a fund's history starts at
    // adoption, so a new member owes from now, not from the past).
    expect(calls[0].body?.joined_on).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })

  // The partial-update semantics are the whole point of this route: an
  // absent key means "leave alone", an explicit null means "clear it".
  it('sends only the fields that changed when editing', async () => {
    const { fetchMock, calls } = stubRoster([member(1, 'Warga Satu')])
    vi.stubGlobal('fetch', fetchMock)
    render(<Roster tiersVersion={0} />)
    await screen.findByText('Warga Satu')

    await userEvent.click(screen.getByRole('button', { name: text.edit }))
    const row = within(screen.getByRole('listitem'))
    const input = row.getByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Warga Pertama')
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', body: { name: 'Warga Pertama' } })
    // Untouched fields are absent, not null - null would clear them.
    expect(calls[0].body).not.toHaveProperty('tier_id')
    expect(calls[0].body).not.toHaveProperty('joined_on')
  })

  it('clears the tier with an explicit null when "tanpa golongan" is chosen', async () => {
    const { fetchMock, calls } = stubRoster([member(1, 'Warga Satu', 1)])
    vi.stubGlobal('fetch', fetchMock)
    render(<Roster tiersVersion={0} />)
    await screen.findByText('Warga Satu')

    await userEvent.click(screen.getByRole('button', { name: text.edit }))
    // Scoped to the row: the add form below carries the same label.
    await chooseOption(text.tierLabel, text.tierNone, within(screen.getByRole('listitem')))
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    // Explicitly null, not absent: this is "drop the dues obligation".
    expect(calls[0].body).toHaveProperty('tier_id', null)
  })

  it('deactivates with today, and reinstates with an explicit null', async () => {
    const active = stubRoster([member(1, 'Warga Satu')])
    vi.stubGlobal('fetch', active.fetchMock)
    const { unmount } = render(<Roster tiersVersion={0} />)
    await screen.findByText('Warga Satu')

    await userEvent.click(screen.getByRole('button', { name: text.deactivate }))
    await waitFor(() => expect(active.calls).toHaveLength(1))
    expect(active.calls[0].body?.inactive_on).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    unmount()

    const retired = stubRoster([member(1, 'Warga Satu', 1, '2026-08-01')])
    vi.stubGlobal('fetch', retired.fetchMock)
    render(<Roster tiersVersion={0} />)
    await screen.findByText('Warga Satu')

    await userEvent.click(screen.getByRole('button', { name: text.reinstate }))
    await waitFor(() => expect(retired.calls).toHaveLength(1))
    expect(retired.calls[0].body).toHaveProperty('inactive_on', null)
  })

  it('deletes a duplicate with no history', async () => {
    const { fetchMock, calls } = stubRoster([member(1, 'Duplikat')], () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    render(<Roster tiersVersion={0} />)
    await screen.findByText('Duplikat')

    await userEvent.click(screen.getByRole('button', { name: text.delete }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/members/1') })
  })

  it('renders the 409 on a member with history as a message pointing at deactivate', async () => {
    const { fetchMock } = stubRoster([member(1, 'Warga Satu')], () =>
      jsonResponse({ error: { code: 'referenced_by_other_records', message: 'referenced' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    render(<Roster tiersVersion={0} />)
    await screen.findByText('Warga Satu')

    await userEvent.click(screen.getByRole('button', { name: text.delete }))

    expect(await screen.findByRole('alert')).toHaveTextContent(text.deleteRefused)
  })
})
