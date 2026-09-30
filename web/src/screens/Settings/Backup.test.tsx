import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Backup from '@/screens/Settings/Backup'
import { copy } from '@/copy/id'
import { todayISODate } from '@/lib/dates'

const text = copy.settings.backup
const common = copy.common

afterEach(() => {
  vi.unstubAllGlobals()
})

function zipResponse() {
  // A string body, not a jsdom Blob: Node 22's own Response cannot read a
  // jsdom Blob back out (res.blob() throws), so a Blob body passed under
  // Node 24 and failed under 22 - the stub has to work on either.
  return new Response('stub zip bytes', {
    status: 200,
    headers: { 'Content-Type': 'application/zip', 'Content-Disposition': 'attachment; filename="uruni.zip"' },
  })
}

function errorResponse() {
  return new Response(JSON.stringify({ error: { code: 'internal_error', message: 'Something went wrong.' } }), {
    status: 500,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('Settings backup card', () => {
  it('shows the heading and the keep-it-safe line', () => {
    render(<Backup />)
    expect(screen.getByText(text.heading)).toBeInTheDocument()
    expect(screen.getByText(text.body)).toBeInTheDocument()
  })

  it('downloads the zip when the button is tapped, disabling itself meanwhile', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(zipResponse()))
    vi.stubGlobal('fetch', fetchMock)
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
      expect(screen.getByRole('button')).toBeEnabled()
    })
  })

  it('saves the file under her own local date, never the undated uruni.zip', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(zipResponse())),
    )
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
    const fetchMock = vi.fn(() => Promise.resolve(errorResponse()))
    vi.stubGlobal('fetch', fetchMock)

    const user = userEvent.setup()
    render(<Backup />)

    await user.click(screen.getByRole('button', { name: text.download }))

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)

    await user.click(screen.getByRole('button', { name: common.retry }))
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2)
    })
  })

  it('has a touch target at least 44px tall', () => {
    render(<Backup />)
    const button = screen.getByRole('button', { name: text.download })
    // h-11 is Tailwind's 44px (2.75rem @ 16px root) - the same class every
    // other primary action on this screen uses (FundName's save button,
    // Locations' add button).
    expect(button.className).toContain('h-11')
  })
})
