import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Locations from '@/screens/Settings/Locations'
import { copy } from '@/copy/id'
import { chooseOption } from '@/test/select'

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

/** Exposes the router's current search string, so a test can assert on
 * `?edit=` the same way it asserts on what re-rendered - the whole point of
 * this screen's dialogs is that the URL is the state. */
function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

function renderAt(entry = '/settings') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/settings"
          element={
            <>
              <Locations />
              <LocationProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

function currentSearch() {
  return screen.getByTestId('location-search').textContent
}

describe('Settings locations', () => {
  it('lists every location, retired ones included, with its kind', async () => {
    const { fetchMock } = stubAccounts([account(1, 'Kotak kas'), account(2, 'Bank Jago', 'bank', '2026-08-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByRole('list')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.editAria('Kotak kas') })).toBeInTheDocument()
    const bankCard = screen.getByRole('button', { name: text.editAria('Bank Jago') })
    expect(within(bankCard).getByText(text.kindBank)).toBeInTheDocument()
    // The retired one is listed and labelled, not hidden: it may still hold
    // a balance, and this screen is where it gets reinstated.
    expect(within(bankCard).getByText(text.inactiveBadge)).toBeInTheDocument()
  })

  it('opens the edit dialog from the card, with ?edit=location:<id> and the name shown', async () => {
    const { fetchMock } = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Kotak kas') }))

    expect(currentSearch()).toBe('?edit=location%3A1')
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    expect(within(dialog).getByLabelText(text.nameLabel)).toHaveValue('Kotak kas')
  })

  it('adds a location via ?edit=location:new, sending the chosen kind', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.add }))
    expect(currentSearch()).toBe('?edit=location%3Anew')

    const dialog = screen.getByRole('dialog', { name: text.add })
    await chooseOption(text.kindLabel, text.kindBank, within(dialog))
    await userEvent.type(within(dialog).getByLabelText(text.nameLabel), 'Bank Jago')
    await userEvent.click(within(dialog).getByRole('button', { name: text.add }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'POST', body: { kind: 'bank', name: 'Bank Jago' } })
    // A successful add closes the dialog and drops the param.
    await waitFor(() => expect(currentSearch()).toBe(''))
  })

  it('renames a location, sending only the field that changed', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Tunia')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Tunia') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    const input = within(dialog).getByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Tunai')
    await userEvent.click(within(dialog).getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', url: expect.stringContaining('/api/accounts/1'), body: { name: 'Tunai' } })
    expect(calls[0].body).not.toHaveProperty('kind')
  })

  it('deactivates only after the inline confirm, showing the consequence copy first', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Kotak kas') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })

    await userEvent.click(within(dialog).getByRole('button', { name: text.deactivate }))
    // No request yet - tapping the button only swaps the footer to the
    // inline confirm, it never posts by itself.
    expect(calls).toHaveLength(0)
    expect(within(dialog).getByText(text.deactivateConfirm)).toBeInTheDocument()

    await userEvent.click(within(dialog).getByRole('button', { name: text.deactivateConfirmAction }))
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0].method).toBe('PATCH')
    // A local YYYY-MM-DD, never toISOString() - which is UTC and can read a
    // day early in WIB.
    expect((calls[0].body as { inactive_on: string }).inactive_on).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  })

  it('reinstates a retired location immediately, no confirm needed', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Kotak kas', 'cash', '2026-08-01')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Kotak kas') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    await userEvent.click(within(dialog).getByRole('button', { name: text.reinstate }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', body: { inactive_on: null } })
  })

  it('deletes only after the inline confirm', async () => {
    const { fetchMock, calls } = stubAccounts([account(1, 'Duplikat')], () => new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Duplikat') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })

    await userEvent.click(within(dialog).getByRole('button', { name: text.delete }))
    expect(calls).toHaveLength(0)
    expect(within(dialog).getByText(text.deleteConfirm)).toBeInTheDocument()

    await userEvent.click(within(dialog).getByRole('button', { name: text.deleteConfirmAction }))
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'DELETE', url: expect.stringContaining('/api/accounts/1') })
  })

  it('renders the 409 on a used location inside the dialog, pointing at deactivate', async () => {
    const { fetchMock } = stubAccounts([account(1, 'Kotak kas')], () =>
      jsonResponse({ error: { code: 'referenced_by_other_records', message: 'referenced' } }, 409),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Kotak kas') }))
    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    await userEvent.click(within(dialog).getByRole('button', { name: text.delete }))
    await userEvent.click(within(dialog).getByRole('button', { name: text.deleteConfirmAction }))

    // The specific sentence, not the shared error copy: a refusal that
    // tells her what to do instead is the whole point of the case - and it
    // stays inside the dialog that raised it.
    const alert = await within(dialog).findByRole('alert')
    expect(alert).toHaveTextContent(text.deleteRefused)
    // A refusal is not a success: the dialog stays open. Asserted on the
    // URL, because jsdom never finishes Radix's exit animation and a
    // closing dialog would still be in the DOM for the alert query above.
    expect(currentSearch()).toContain('edit=location')
  })

  it('opens a dialog directly from a deep link to ?edit=location:<id>', async () => {
    const { fetchMock } = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=location:1')

    const dialog = await screen.findByRole('dialog', { name: text.editTitle })
    expect(within(dialog).getByLabelText(text.nameLabel)).toHaveValue('Kotak kas')
  })

  it('strips an unknown id from ?edit= once the list has loaded, without flashing a dialog', async () => {
    const { fetchMock } = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=location:999')
    await screen.findByRole('list')

    await waitFor(() => expect(currentSearch()).toBe(''))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('closing the dialog removes ?edit= from the URL', async () => {
    const { fetchMock } = stubAccounts([account(1, 'Kotak kas')])
    vi.stubGlobal('fetch', fetchMock)
    renderAt()
    await screen.findByRole('list')

    await userEvent.click(screen.getByRole('button', { name: text.editAria('Kotak kas') }))
    expect(currentSearch()).toBe('?edit=location%3A1')

    const dialog = screen.getByRole('dialog', { name: text.editTitle })
    await userEvent.click(within(dialog).getByRole('button', { name: text.cancel }))

    await waitFor(() => expect(currentSearch()).toBe(''))
  })
})
