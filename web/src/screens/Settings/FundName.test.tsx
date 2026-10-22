import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import FundName from '@/screens/Settings/FundName'
import { chooseZoneOption } from '@/test/timeZone'
import { copy } from '@/copy/id'
import { getFundTimeZone, resetFundTimeZone } from '@/lib/dates'
import type { Fund } from '@/lib/setup'

const text = copy.settings.fund
const zones = copy.timeZone

afterEach(() => {
  vi.unstubAllGlobals()
  resetFundTimeZone()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const fund: Fund = {
  id: 1,
  name: 'Kas Ruang 3A',
  currency: 'IDR',
  time_zone: 'Asia/Jakarta',
  report_slug: 'abcdefghijklmnopqrstuv',
  report_url: '/report/abcdefghijklmnopqrstuv',
  created_at: 1,
}

function stubFund() {
  const calls: { method: string; body: unknown }[] = []
  // The server's fund: a PATCH changes it and answers with the result, as the
  // real one does. No GET: the section reads the fund it is handed.
  let current = fund
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url.includes('/api/fund') && method === 'PATCH') {
      const body = init?.body ? (JSON.parse(String(init.body)) as { name?: string; time_zone?: string }) : undefined
      calls.push({ method, body })
      current = { ...current, name: body?.name ?? current.name, time_zone: body?.time_zone ?? current.time_zone }
      return Promise.resolve(jsonResponse(current))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
  return { fetchMock, calls }
}

/** Exposes the router's search string: this section's dialog IS the URL
 * (ADR-032), so `?edit=` is state worth asserting on. */
function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

/** Holds the fund the way App.tsx does: the section reads it from here, and a
 * saved fund handed back through `onChanged` is what the section shows next. */
function AppFund({ onChanged }: { onChanged: (fund: Fund) => void }) {
  const [current, setCurrent] = useState(fund)
  return (
    <FundName
      fund={current}
      onChanged={(updated) => {
        setCurrent(updated)
        onChanged(updated)
      }}
      refresh={() => Promise.resolve()}
    />
  )
}

function renderAt(entry = '/settings', onRenamed: (fund: Fund) => void = vi.fn()) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/settings"
          element={
            <>
              <AppFund onChanged={onRenamed} />
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

describe('Settings fund (Kas ini)', () => {
  it("shows the fund's name as a card, with no form until the dialog opens", async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByRole('button', { name: text.editAria('Kas Ruang 3A') })).toBeInTheDocument()
    // The inline form is gone: Pengaturan has no form until she asks for one.
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
    // callback keeps it from showing the old one until a reload.
    await waitFor(() => expect(onRenamed).toHaveBeenCalledWith(expect.objectContaining({ name: 'Kas Ruang 3B' })))
    // The dialog closing IS the confirmation now - no inline "saved" line.
    await waitFor(() => expect(currentSearch()).toBe(''))
    // The card shows the fund handed back, with no GET to read it again.
    expect(await screen.findByRole('button', { name: text.editAria('Kas Ruang 3B') })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  // The app already holds the fund; fetching it here again was Pengaturan's
  // duplicate GET /api/fund.
  it('reads the fund it is handed, with no request of its own', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(screen.getByRole('button', { name: text.editAria('Kas Ruang 3A') })).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
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

  // A `fund:` value with nothing behind it is this section's dead link.
  it('strips a fund: value it cannot use, and opens no dialog for it', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:nonsense')

    await waitFor(() => expect(currentSearch()).toBe(''))
    expect(screen.queryByLabelText(text.nameLabel)).not.toBeInTheDocument()
  })

  // What keeps sibling sections from closing each other's dialogs: another
  // section's param is foreign and must be left where it is.
  it("leaves another section's ?edit= alone", async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=location:3')

    await screen.findByRole('button', { name: text.editAria('Kas Ruang 3A') })
    // Byte for byte as it arrived: this section never rewrote it, so it is not
    // re-encoded as a value this section set would be.
    expect(currentSearch()).toBe('?edit=location:3')
  })

  // Radix's FocusScope focuses the first tabbable with `select: true`, which on
  // a pre-filled field means her whole kas name arrives highlighted and one
  // keystroke from gone. dialog.tsx takes that focus step over.
  it('opens with the caret in the name, not the whole name selected', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:name')

    const input = (await screen.findByLabelText(text.nameLabel)) as HTMLInputElement
    await waitFor(() => expect(input).toHaveFocus())
    expect(input.selectionStart).toBe(input.value.length)
    expect(input.selectionEnd).toBe(input.value.length)
  })

  it('is headed "Kas ini", with an intro for both settings', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await screen.findByRole('button', { name: text.editAria('Kas Ruang 3A') })
    expect(screen.getByRole('heading', { name: 'Kas ini' })).toBeInTheDocument()
    expect(screen.getByText(text.body)).toBeInTheDocument()
  })

  it("shows the zone as a second card, in the chosen option's own words", async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    const card = await screen.findByRole('button', { name: text.zone.editAria(zones.options['Asia/Jakarta'].label) })
    expect(card).toHaveTextContent(zones.options['Asia/Jakarta'].label)
    expect(card).toHaveTextContent(zones.label)
    expect(screen.queryByRole('combobox', { name: zones.label })).not.toBeInTheDocument()
  })

  it('opens the zone dialog on the URL, seeded with the current zone', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await userEvent.click(await screen.findByRole('button', { name: text.zone.editAria(zones.options['Asia/Jakarta'].label) }))

    expect(await screen.findByRole('dialog', { name: text.zone.editTitle })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: zones.label })).toHaveTextContent(zones.options['Asia/Jakarta'].label)
    expect(screen.getByText(text.zone.body)).toBeInTheDocument()
    expect(currentSearch()).toBe('?edit=fund%3Atime_zone')
  })

  it('opens the zone dialog straight from a deep link', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:time_zone')

    expect(await screen.findByRole('dialog', { name: text.zone.editTitle })).toBeInTheDocument()
    // And it is not mistaken for the name's dead link and stripped.
    expect(currentSearch()).toBe('?edit=fund:time_zone')
  })

  it('changes the zone with PATCH /api/fund, sends only the zone, hands the fund back and closes', async () => {
    const { fetchMock, calls } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    const onChanged = vi.fn()
    renderAt('/settings?edit=fund:time_zone', onChanged)

    await screen.findByRole('dialog', { name: text.zone.editTitle })
    expect(screen.getByRole('button', { name: text.save })).toBeDisabled()
    await chooseZoneOption('Asia/Jayapura')
    await userEvent.click(screen.getByRole('button', { name: text.save }))

    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toEqual({ method: 'PATCH', body: { time_zone: 'Asia/Jayapura' } })
    await waitFor(() =>
      expect(onChanged).toHaveBeenCalledWith(expect.objectContaining({ time_zone: 'Asia/Jayapura', name: 'Kas Ruang 3A' })),
    )
    await waitFor(() => expect(currentSearch()).toBe(''))
    // The app is on the new zone the moment the PATCH answered: the date helpers
    // hold it, with no reload.
    expect(getFundTimeZone()).toBe('Asia/Jayapura')
  })

  it("leaves the date helpers' zone alone when she cancels", async () => {
    const { fetchMock, calls } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:time_zone')

    await screen.findByRole('dialog', { name: text.zone.editTitle })
    await chooseZoneOption('Asia/Makassar')
    await userEvent.click(screen.getByRole('button', { name: text.cancel }))

    await waitFor(() => expect(currentSearch()).toBe(''))
    expect(calls).toHaveLength(0)
    expect(getFundTimeZone()).toBe('Asia/Jakarta')
  })

  it('keeps a half-made choice from one opening to the next', async () => {
    const { fetchMock } = stubFund()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=fund:time_zone')

    await screen.findByRole('dialog', { name: text.zone.editTitle })
    await chooseZoneOption('Asia/Makassar')
    await userEvent.click(screen.getByRole('button', { name: text.cancel }))
    await waitFor(() => expect(currentSearch()).toBe(''))

    await userEvent.click(await screen.findByRole('button', { name: text.zone.editAria(zones.options['Asia/Jakarta'].label) }))
    expect(await screen.findByRole('combobox', { name: zones.label })).toHaveTextContent(zones.options['Asia/Jakarta'].label)
  })
})
