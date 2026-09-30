import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Backup from '@/screens/Settings/Backup'
import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { todayISODate } from '@/lib/dates'

const text = copy.settings.backup
const common = copy.common

afterEach(() => {
  vi.unstubAllGlobals()
})

function zipResponse(filename = 'uruni.zip') {
  // A string body, not a jsdom Blob: Node 22's own Response cannot read a
  // jsdom Blob back out (res.blob() throws), so a Blob body passed under
  // Node 24 and failed under 22 - the stub has to work on either.
  return new Response('stub zip bytes', {
    status: 200,
    headers: { 'Content-Type': 'application/zip', 'Content-Disposition': `attachment; filename="${filename}"` },
  })
}

function errorResponse() {
  return new Response(JSON.stringify({ error: { code: 'internal_error', message: 'Something went wrong.' } }), {
    status: 500,
    headers: { 'Content-Type': 'application/json' },
  })
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function emptyListResponse() {
  return jsonResponse([])
}

function requestURL(input: RequestInfo | URL): string {
  if (typeof input === 'string') return input
  if (input instanceof URL) return input.toString()
  return input.url
}

/**
 * Every test in this file needs GET /api/backups to answer with *something*
 * - the auto-list fetches it on mount regardless of what the test is
 * actually about - so this is the one fetch stub every case builds on:
 * route by URL, default the list to empty unless a test asks otherwise, and
 * fail loudly on an unexpected path rather than silently returning
 * undefined (which would surface as a confusing "res.ok of undefined").
 */
function stubFetch(overrides: { download?: () => Response; list?: () => Response }) {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const url = requestURL(input)
    if (url === '/api/backup') return Promise.resolve((overrides.download ?? zipResponse)())
    if (url === '/api/backups') return Promise.resolve((overrides.list ?? emptyListResponse)())
    if (url.startsWith('/api/backups/')) {
      // The row-download route: not used unless a test names one.
      throw new Error(`stubFetch: unexpected download of ${url} - pass it its own response`)
    }
    throw new Error(`stubFetch: unexpected fetch to ${url}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('Settings backup card', () => {
  it('shows the heading and the keep-it-safe line', () => {
    stubFetch({})
    render(<Backup />)
    expect(screen.getByText(text.heading)).toBeInTheDocument()
    expect(screen.getByText(text.body)).toBeInTheDocument()
  })

  it('downloads the zip when the button is tapped, disabling itself meanwhile', async () => {
    const fetchMock = stubFetch({})
    // jsdom has no object URL support - stubbed so saveBlob's own calls
    // don't throw, not asserted on further: this test is about the request
    // and the button's own state, not the browser's save mechanics.
    vi.stubGlobal('URL', { ...URL, createObjectURL: vi.fn(() => 'blob:stub'), revokeObjectURL: vi.fn() })

    const user = userEvent.setup()
    render(<Backup />)

    const button = screen.getByRole('button', { name: text.download })
    await user.click(button)

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith('/api/backup')
    })
    await waitFor(() => {
      expect(screen.getByRole('button', { name: text.download })).toBeEnabled()
    })
  })

  it('saves the file under her own local date, never the undated uruni.zip', async () => {
    stubFetch({})
    vi.stubGlobal('URL', { ...URL, createObjectURL: vi.fn(() => 'blob:stub'), revokeObjectURL: vi.fn() })
    const clicked: string[] = []
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.download)
    })

    const user = userEvent.setup()
    render(<Backup />)
    await user.click(screen.getByRole('button', { name: text.download }))

    await waitFor(() => expect(clicked).toEqual([`uruni-${todayISODate()}.zip`]))
    click.mockRestore()
  })

  it('renders the shared error state on failure, with a working retry', async () => {
    const fetchMock = stubFetch({ download: errorResponse })

    const user = userEvent.setup()
    render(<Backup />)

    await user.click(screen.getByRole('button', { name: text.download }))

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledWith('/api/backup')
    const callsToBackup = fetchMock.mock.calls.filter((c) => requestURL(c[0] as RequestInfo | URL) === '/api/backup')
    expect(callsToBackup).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: common.retry }))
    await waitFor(() => {
      const callsAfterRetry = fetchMock.mock.calls.filter((c) => requestURL(c[0] as RequestInfo | URL) === '/api/backup')
      expect(callsAfterRetry).toHaveLength(2)
    })
  })

  it('has a touch target at least 44px tall', () => {
    stubFetch({})
    render(<Backup />)
    const button = screen.getByRole('button', { name: text.download })
    // h-11 is Tailwind's 44px (2.75rem @ 16px root) - the same class every
    // other primary action on this screen uses (FundName's save button,
    // Locations' add button).
    expect(button.className).toContain('h-11')
  })
})

describe('Settings backup card - automatic backup list', () => {
  it('shows the empty state when the server has no dumps yet', async () => {
    stubFetch({ list: emptyListResponse })
    render(<Backup />)

    expect(await screen.findByText(text.empty)).toBeInTheDocument()
  })

  it('lists dumps newest first, with date, kind and size', async () => {
    stubFetch({
      list: () =>
        jsonResponse([
          {
            name: 'uruni-20260930-010000-daily-fv1-aaaaaaaaaaaa.zip',
            date: '2026-09-30',
            kind: 'daily',
            format_version: 1,
            is_current_format: true,
            size_bytes: 51200,
          },
          {
            name: 'uruni-20260929-010000-pre-restore-fv1-bbbbbbbbbbbb.zip',
            date: '2026-09-29',
            kind: 'pre-restore',
            format_version: 1,
            is_current_format: true,
            size_bytes: 2048,
          },
        ]),
    })
    render(<Backup />)

    const rows = await screen.findAllByRole('listitem')
    expect(rows).toHaveLength(2)

    expect(within(rows[0]).getByText(formatIsoDate('2026-09-30'))).toBeInTheDocument()
    expect(within(rows[0]).getByText(text.kindDaily)).toBeInTheDocument()
    expect(within(rows[0]).getByText('50 KB')).toBeInTheDocument()

    expect(within(rows[1]).getByText(formatIsoDate('2026-09-29'))).toBeInTheDocument()
    expect(within(rows[1]).getByText(text.kindPreRestore)).toBeInTheDocument()
    expect(within(rows[1]).getByText('2 KB')).toBeInTheDocument()
  })

  it('labels an older-format dump and offers it no restore affordance', async () => {
    const name = 'uruni-20260101-010000-daily-fv0-cccccccccccc.zip'
    stubFetch({
      list: () =>
        jsonResponse([{ name, date: '2026-01-01', kind: 'daily', format_version: 0, is_current_format: false, size_bytes: 1024 }]),
    })
    render(<Backup />)

    const row = (await screen.findAllByRole('listitem'))[0]
    expect(within(row).getByText(new RegExp(text.olderFormat))).toBeInTheDocument()
    // No restore control anywhere on an older-format row - #326 builds one,
    // this slice never does, for any row.
    expect(within(row).queryByRole('button', { name: /pulih/i })).not.toBeInTheDocument()
    // The download control is still there - an older-format dump stays
    // downloadable (ADR-012), only restore is withheld.
    expect(within(row).getByRole('button', { name: text.downloadRowAria(text.kindDaily, formatIsoDate('2026-01-01')) })).toBeInTheDocument()
  })

  it('downloads a listed dump under its own name when its row control is tapped', async () => {
    const name = 'uruni-20260930-010000-daily-fv1-aaaaaaaaaaaa.zip'
    const fetchMock = stubFetch({
      list: () => jsonResponse([{ name, date: '2026-09-30', kind: 'daily', format_version: 1, is_current_format: true, size_bytes: 1024 }]),
    })
    fetchMock.mockImplementation((input: RequestInfo | URL) => {
      const url = requestURL(input)
      if (url === '/api/backups')
        return Promise.resolve(
          jsonResponse([{ name, date: '2026-09-30', kind: 'daily', format_version: 1, is_current_format: true, size_bytes: 1024 }]),
        )
      if (url === `/api/backups/${name}`) return Promise.resolve(zipResponse(name))
      throw new Error(`unexpected fetch to ${url}`)
    })
    vi.stubGlobal('URL', { ...URL, createObjectURL: vi.fn(() => 'blob:stub'), revokeObjectURL: vi.fn() })
    const clicked: string[] = []
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.download)
    })

    const user = userEvent.setup()
    render(<Backup />)

    const rowButton = await screen.findByRole('button', { name: text.downloadRowAria(text.kindDaily, formatIsoDate('2026-09-30')) })
    await user.click(rowButton)

    await waitFor(() => expect(clicked).toEqual([name]))
    click.mockRestore()
  })

  it('renders the shared error state when the list fails to load, with a working retry', async () => {
    const fetchMock = stubFetch({ list: errorResponse })
    render(<Backup />)

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    const callsToList = () => fetchMock.mock.calls.filter((c) => requestURL(c[0] as RequestInfo | URL) === '/api/backups')
    expect(callsToList()).toHaveLength(1)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: common.retry }))
    await waitFor(() => expect(callsToList()).toHaveLength(2))
  })

  it('gives every row download control a touch target at least 44px', async () => {
    const name = 'uruni-20260930-010000-daily-fv1-aaaaaaaaaaaa.zip'
    stubFetch({
      list: () => jsonResponse([{ name, date: '2026-09-30', kind: 'daily', format_version: 1, is_current_format: true, size_bytes: 1024 }]),
    })
    render(<Backup />)

    const rowButton = await screen.findByRole('button', { name: text.downloadRowAria(text.kindDaily, formatIsoDate('2026-09-30')) })
    // size-11 is Tailwind's 44px, the same scale h-11 uses elsewhere on this
    // screen - a row control this small still needs the full touch target.
    expect(rowButton.className).toContain('size-11')
  })
})
