import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import FundName from '@/screens/Settings/FundName'
import { copy } from '@/copy/id'

const text = copy.settings.fund

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const fund = { id: 1, name: 'Kas Ruang 3A', currency: 'IDR', report_slug: 'abcdefghijklmnopqrstuv', created_at: 1 }

function stubFund() {
  const calls: { method: string; body: unknown }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/fund')) {
      if (method === 'GET') return Promise.resolve(jsonResponse(fund))
      calls.push({ method, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      return Promise.resolve(jsonResponse({ ...fund, name: 'Kas Ruang 3B' }))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

/** Exposes the router's search string: this section's dialog IS the URL
 * (ADR-032), so `?edit=` is state worth asserting on directly. */
function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

function renderAt(entry = '/settings', onRenamed: () => void = vi.fn()) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/settings"
          element={
            <>
              <FundName onRenamed={onRenamed} />
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

describe('Settings fund name', () => {
  it('shows the fund\'s name as a card, with no form until the dialog opens', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByRole('button', { name: text.editAria('Kas Ruang 3A') })).toBeInTheDocument()
    // The inline form is gone (M6.30): Pengaturan has no form on it until
    // she asks for one.
    expect(screen.queryByLabelText(text.nameLabel)).not.toBeInTheDocument()
  })

  it('opens the rename dialog on the URL, seeded with the current name', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await userEvent.click(await screen.findByRole('button', { name: text.editAria('Kas Ruang 3A') }))

    expect(await screen.findByLabelText(text.nameLabel)).toHaveValue('Kas Ruang 3A')
    expect(currentSearch()).toBe('?edit=fund%3Aname')
  })

  it('opens straight from a deep link, without a tap', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:name')

    expect(await screen.findByLabelText(text.nameLabel)).toHaveValue('Kas Ruang 3A')
  })

  it('renames the fund, hands the new one back to the caller and closes', async () => {
    const { fetchMock, calls } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    const onRenamed = vi.fn()
    renderAt('/settings?edit=fund:name', onRenamed)

    const input = await screen.findByLabelText(text.nameLabel)
    await userEvent.clear(input)
    await userEvent.type(input, 'Kas Ruang 3B')
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toMatchObject({ method: 'PATCH', body: { name: 'Kas Ruang 3B' } })
    // Shell's header is the same name, read once when the app booted - the
    // callback is what keeps it from showing the old one until a reload.
    await waitFor(() => expect(onRenamed).toHaveBeenCalledWith(expect.objectContaining({ name: 'Kas Ruang 3B' })))
    // The dialog closing IS the confirmation now - no inline "saved" line.
    await waitFor(() => expect(currentSearch()).toBe(''))
  })

  it('will not submit a name that has not changed, or an empty one', async () => {
    const { fetchMock, calls } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:name')

    const input = await screen.findByLabelText(text.nameLabel)
    expect(screen.getByRole('button', { name: text.save })).toBeDisabled()

    await userEvent.clear(input)
    expect(screen.getByRole('button', { name: text.save })).toBeDisabled()
    expect(calls).toHaveLength(0)
  })

  // A `fund:` value with nothing behind it is this section's own dead link.
  it('strips a fund: value it cannot use, and opens no dialog for it', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:nonsense')

    await waitFor(() => expect(currentSearch()).toBe(''))
    expect(screen.queryByLabelText(text.nameLabel)).not.toBeInTheDocument()
  })

  // The case that keeps sibling sections from closing each other's dialogs:
  // another section's param is foreign and must be left exactly where it is.
  it('leaves another section\'s ?edit= alone', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=location:3')

    await screen.findByRole('button', { name: text.editAria('Kas Ruang 3A') })
    // Byte for byte as it arrived: this section never rewrote it, so it is
    // not even re-encoded the way a value this section set would be.
    expect(currentSearch()).toBe('?edit=location:3')
  })

  // Radix's FocusScope focuses the first tabbable with `select: true`, which
  // on a pre-filled field means her whole kas name arrives highlighted and
  // one keystroke from gone. dialog.tsx takes that focus step over.
  it('opens with the caret in the name, not the whole name selected', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:name')

    const input = (await screen.findByLabelText(text.nameLabel)) as HTMLInputElement
    await waitFor(() => expect(input).toHaveFocus())
    expect(input.selectionStart).toBe(input.value.length)
    expect(input.selectionEnd).toBe(input.value.length)
  })
})
