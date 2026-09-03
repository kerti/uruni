import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import PassThrough from '@/screens/Settings/PassThrough'
import { copy } from '@/copy/id'

const text = copy.settings.passThrough

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function purpose(id: number, kind: string, name: string) {
  return { id, kind, name, created_at: 1 }
}

function stubPurposes(initial: ReturnType<typeof purpose>[]) {
  const calls: { method: string; url: string; body: unknown }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/pass-through-purposes')) {
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(jsonResponse(purpose(9, 'pass_through', 'Kas Bidang')))
    }
    if (url.includes('/api/purposes') && method === 'GET') return Promise.resolve(jsonResponse(initial))
    if (url.includes('/api/purposes')) {
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(jsonResponse(purpose(2, 'pass_through', 'Kas Bidang')))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

describe('Settings pass-through', () => {
  it('lists only pass-through purposes', async () => {
    const { fetchMock } = stubPurposes([
      purpose(1, 'main', 'Kas utama'),
      purpose(2, 'pass_through', 'Kas Bidang'),
      purpose(3, 'incidental', 'Kurban 2026'),
    ])
    vi.stubGlobal('fetch', fetchMock)
    render(<PassThrough />)

    expect(await screen.findByText('Kas Bidang')).toBeInTheDocument()
    // GET /api/purposes answers every tag the fund has; the fund's own money
    // and an open incidental are not this section's business.
    expect(screen.queryByText('Kas utama')).not.toBeInTheDocument()
    expect(screen.queryByText('Kurban 2026')).not.toBeInTheDocument()
  })

  it('shows the empty state when the fund has no pass-through purposes', async () => {
    const { fetchMock } = stubPurposes([purpose(1, 'main', 'Kas utama')])
    vi.stubGlobal('fetch', fetchMock)
    render(<PassThrough />)

    expect(await screen.findByText(text.empty)).toBeInTheDocument()
  })

  it('creates a pass-through purpose with a name only', async () => {
    const { fetchMock, calls } = stubPurposes([purpose(1, 'main', 'Kas utama')])
    vi.stubGlobal('fetch', fetchMock)
    render(<PassThrough />)
    await screen.findByText(text.empty)

    await userEvent.type(screen.getByLabelText(text.nameLabel), 'Kas Bidang')
    await userEvent.click(screen.getByRole('button', { name: text.add }))

    await waitFor(() => expect(calls).toHaveLength(1))
    // Name only - the kind is pinned server-side, and a caller that can name
    // the kind can ask for a second 'main'.
    expect(calls[0]).toMatchObject({ method: 'POST', body: { name: 'Kas Bidang' } })
    expect(calls[0].body).not.toHaveProperty('kind')
  })

  // The name is a label, so a typo is correctable - the row itself is not,
  // because money that passed through is not unsaid.
  it('renames a pass-through purpose', async () => {
    const { fetchMock, calls } = stubPurposes([purpose(2, 'pass_through', 'Kas Bidan')])
    vi.stubGlobal('fetch', fetchMock)
    render(<PassThrough />)
    await screen.findByText('Kas Bidan')

    await userEvent.click(screen.getByRole('button', { name: text.edit }))
    const row = within(screen.getByRole('listitem'))
    const input = row.getByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Kas Bidang')
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', url: expect.stringContaining('/api/purposes/2'), body: { name: 'Kas Bidang' } })
  })

  // No delete anywhere in this section, and none on the server either.
  it('offers no way to remove a pass-through purpose', async () => {
    const { fetchMock } = stubPurposes([purpose(2, 'pass_through', 'Kas Bidang')])
    vi.stubGlobal('fetch', fetchMock)
    render(<PassThrough />)
    await screen.findByText('Kas Bidang')

    expect(within(screen.getByRole('listitem')).getAllByRole('button')).toHaveLength(1)
  })
})
