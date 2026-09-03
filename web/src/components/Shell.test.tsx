import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'

import Shell from '@/components/Shell'
import { copy } from '@/copy/id'

/** Shell's footer nav uses NavLink, so every render needs a router. The
 * initial entry is what decides which tab reads as current. */
function renderShell(ui: React.ReactElement, initialPath = '/') {
  return render(<MemoryRouter initialEntries={[initialPath]}>{ui}</MemoryRouter>)
}

afterEach(() => {
  vi.unstubAllGlobals()
})

function okResponse() {
  return new Response(null, { status: 204 })
}

describe('Shell', () => {
  it('renders the fund name as the heading and the children inside the main landmark', () => {
    renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi layar</p>
      </Shell>,
    )

    expect(screen.getByRole('heading', { name: 'Kas RT 04' })).toBeInTheDocument()
    expect(screen.getByRole('main')).toHaveTextContent('isi layar')
  })

  // The footer nav (M6.15) replaced the add-FAB: "Catat" is a tab now, and
  // no screen has two entry points.
  it('renders the five destinations in the footer nav', () => {
    renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
    )

    const nav = within(screen.getByRole('navigation', { name: copy.shell.nav.label }))
    expect(nav.getByRole('link', { name: copy.shell.nav.home })).toHaveAttribute('href', '/')
    expect(nav.getByRole('link', { name: copy.shell.nav.record })).toHaveAttribute('href', '/record')
    expect(nav.getByRole('link', { name: copy.shell.nav.dues })).toHaveAttribute('href', '/dues')
    expect(nav.getByRole('link', { name: copy.shell.nav.members })).toHaveAttribute('href', '/members')
    expect(nav.getByRole('link', { name: copy.shell.nav.settings })).toHaveAttribute('href', '/settings')
  })

  // A second way home, next to the Beranda tab - never instead of it: a
  // top-corner control is the hardest thing on a phone to reach one-handed.
  it('makes the fund name a link home without renaming the heading', () => {
    renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
      '/settings',
    )

    // The heading still announces the fund's name and nothing else - an
    // aria-label on the link would have become the heading's name too.
    expect(screen.getByRole('heading', { name: 'Kas RT 04' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Kas RT 04' })).toHaveAttribute('href', '/')
    // And the Beranda tab is still there.
    expect(within(screen.getByRole('navigation', { name: copy.shell.nav.label })).getByRole('link', { name: copy.shell.nav.home })).toBeInTheDocument()
  })

  // aria-current comes from the URL, not from anything Shell remembers, so
  // a deep link and a back button both land on the right tab.
  it('marks the tab matching the current route, and only that one', () => {
    renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
      '/dues/payment',
    )

    const nav = within(screen.getByRole('navigation', { name: copy.shell.nav.label }))
    expect(nav.getByRole('link', { name: copy.shell.nav.dues })).toHaveAttribute('aria-current', 'page')
    // `end` on "/" alone: home must not read as current on every route.
    expect(nav.getByRole('link', { name: copy.shell.nav.home })).not.toHaveAttribute('aria-current')
  })

  it('posts to /api/logout and calls onLoggedOut once it succeeds', async () => {
    const fetchMock = vi.fn().mockResolvedValue(okResponse())
    vi.stubGlobal('fetch', fetchMock)
    const onLoggedOut = vi.fn()

    renderShell(
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

    renderShell(
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
