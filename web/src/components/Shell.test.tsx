import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Shell from '@/components/Shell'
import { copy } from '@/copy/id'

afterEach(() => {
  vi.unstubAllGlobals()
})

function okResponse() {
  return new Response(null, { status: 204 })
}

describe('Shell', () => {
  it('renders the fund name as the heading and the children inside the main landmark', () => {
    render(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi layar</p>
      </Shell>,
    )

    expect(screen.getByRole('heading', { name: 'Kas RT 04' })).toBeInTheDocument()
    expect(screen.getByRole('main')).toHaveTextContent('isi layar')
  })

  it('renders no bottom action slot when none is given, and renders one when it is', () => {
    const { rerender } = render(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
    )
    expect(screen.queryByRole('button', { name: 'Catat' })).not.toBeInTheDocument()

    rerender(
      <Shell title="Kas RT 04" onLoggedOut={() => {}} action={<button type="button">Catat</button>}>
        <p>isi</p>
      </Shell>,
    )
    expect(screen.getByRole('button', { name: 'Catat' })).toBeInTheDocument()
  })

  it('posts to /api/logout and calls onLoggedOut once it succeeds', async () => {
    const fetchMock = vi.fn().mockResolvedValue(okResponse())
    vi.stubGlobal('fetch', fetchMock)
    const onLoggedOut = vi.fn()

    render(
      <Shell title="Kas RT 04" onLoggedOut={onLoggedOut}>
        <p>isi</p>
      </Shell>,
    )

    await userEvent.click(screen.getByRole('button', { name: copy.shell.logout }))

    await waitFor(() => expect(onLoggedOut).toHaveBeenCalledTimes(1))

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/logout')
    expect(init.method).toBe('POST')
  })

  it('keeps the session and shows warm Indonesian copy when logout fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('failed to fetch')))
    const onLoggedOut = vi.fn()

    render(
      <Shell title="Kas RT 04" onLoggedOut={onLoggedOut}>
        <p>isi</p>
      </Shell>,
    )

    await userEvent.click(screen.getByRole('button', { name: copy.shell.logout }))

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.network_error)
    // A failed logout must not pretend to have worked - the server session is
    // still live, so the treasurer stays where she is.
    expect(onLoggedOut).not.toHaveBeenCalled()
  })
})
