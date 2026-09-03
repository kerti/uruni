import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Locations from '@/screens/Settings/Locations'
import { copy } from '@/copy/id'
import { chooseOption, selectedOptionName } from '@/test/select'

const text = copy.settings.locations

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function account(id: number, name: string, kind = 'cash', inactiveOn: string | null = null) {
  return { id, kind, name, inactive_on: inactiveOn, created_at: 1 }
}

/** The rendered list of locations. Scoped, because the add form's kind
 * select now shows "Tunai" as its own default label - so a bare
 * getByText('Kotak kas') is ambiguous with a location of that name. */
function locationList() {
  return within(screen.getByRole('list'))
}

/** One stub for the whole section: GET answers the current list, and every
 * write is recorded so a test can assert the request the row actually made
 * (method, path, body) rather than only what re-rendered. */
function stubAccounts(initial: ReturnType<typeof account>[], writeResponse?: () => Response) {
  const calls: { method: string; url: string; body: unknown }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/accounts')) {
      if (method === 'GET') return Promise.resolve(jsonResponse(initial))
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(writeResponse ? writeResponse() : jsonResponse(account(9, 'Baru')))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

describe('Settings locations', () => {
  it('lists every location, retired ones included, with its kind', async () => {
    const { fetchMock } = stubAccounts([account(1, 'Kotak kas'), account(2, 'Bank Jago', 'bank', '2026-08-01')])
    vi.stubGlobal('fetch', fetchMock)
    render(<Locations />)

    expect(await screen.findByRole('list')).toBeInTheDocument()
    expect(locationList().getByText('Kotak kas')).toBeInTheDocument()
    expect(locationList().getByText('Bank Jago')).toBeInTheDocument()
    // Scoped to the row: the add form's kind select shows "Tunai" too, and
    // this assertion is about the row's own kind label.
    const bankRow = locationList().getByText('Bank Jago').closest('li') as HTMLElement
    expect(within(bankRow).getByText(text.kindBank)).toBeInTheDocument()
    // The retired one is listed and labelled, not hidden: it may still hold
    // a balance, and this screen is where it gets reinstated.
    expect(screen.getByText(text.inactiveBadge)).toBeInTheDocument()
  })

  it('adds a location with the kind that was chosen', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', fetchMock)
    render(<Locations />)
    await screen.findByRole('list')

    // Only one kind select is on screen while no row is being edited, so the
    // trigger's accessible name is unambiguous here.
    await chooseOption(text.kindLabel, text.kindBank)
    expect(selectedOptionName(text.kindLabel)).toBe(text.kindBank)

    const form = within(screen.getByRole('form', { name: text.add }))
    await userEvent.type(form.getByLabelText(text.nameLabel), 'Bank Jago')
    await userEvent.click(form.getByRole('button', { name: text.add }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', body: { kind: 'bank', name: 'Bank Jago' } })
  })

  it('renames a location, sending only the field that changed', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Tunia')])
    vi.stubGlobal('fetch', fetchMock)
    render(<Locations />)
    await screen.findByText('Tunia') // deliberately misspelt: this test fixes it

    await userEvent.click(screen.getByRole('button', { name: text.edit }))
    // The row's own field, not the add form's - both carry the same label,
    // which is why the add form is a named region.
    const input = within(screen.getByRole('listitem')).getByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Tunai')
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', url: expect.stringContaining('/api/accounts/1'), body: { name: 'Tunai' } })
  })

  // Kind is a label, not a rule about the money in it, so it is correctable
  // the same way a misspelt name is.
  it('changes a location\'s kind, sending only the field that changed', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', fetchMock)
    render(<Locations />)
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.edit }))
    const row = within(screen.getByRole('listitem'))
    await userEvent.click(row.getByRole('combobox', { name: text.kindLabel }))
    await userEvent.click(screen.getByRole('option', { name: text.kindBank }))
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', body: { kind: 'bank' } })
    // The name did not change, so it is not in the body - an absent key
    // means "leave alone" server-side.
    expect(calls[0].body).not.toHaveProperty('name')
  })

  it('deactivates an active location with today as the date, and reinstates a retired one with null', async () => {
    const active = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', active.fetchMock)
    const { unmount } = render(<Locations />)
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.deactivate }))
    await waitFor(() => expect(active.calls).toHaveLength(1))
    expect(active.calls[0].method).toBe('PATCH')
    // A local YYYY-MM-DD, never toISOString() - which is UTC and can read a
    // day early in WIB.
    expect((active.calls[0].body as { inactive_on: string }).inactive_on).toMatch(/^\d{4}-\d{2}-\d{2}$/)
    unmount()

    const retired = stubAccounts([account(1, 'Kotak kas', 'cash', '2026-08-01')])
    vi.stubGlobal('fetch', retired.fetchMock)
    render(<Locations />)
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.reinstate }))
    await waitFor(() => expect(retired.calls).toHaveLength(1))
    expect(retired.calls[0]).toMatchObject({ method: 'PATCH', body: { inactive_on: null } })
  })

  it('deletes an unreferenced location', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Duplikat')], () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    render(<Locations />)
    await screen.findByText('Duplikat')

    await userEvent.click(screen.getByRole('button', { name: text.delete }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/accounts/1') })
  })

  it('renders the 409 on a used location as a message pointing at deactivate', async () => {
    const { fetchMock } = stubAccounts([account(1, 'Kotak kas')], () =>
      jsonResponse({ error: { code: 'referenced_by_other_records', message: 'referenced' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    render(<Locations />)
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.delete }))

    // The specific sentence, not the shared error copy: a refusal that tells
    // her what to do instead is the whole point of the case.
    expect(await screen.findByRole('alert')).toHaveTextContent(text.deleteRefused)
  })
})
