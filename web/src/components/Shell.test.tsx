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

  // The footer nav (M6.15, reshaped by M6.23/ADR-032) replaced the add-FAB:
  // "Catat" is a tab now, and no screen has two entry points. Iuran no
  // longer holds a slot - it moved to a Riwayat tab (Shell.test.tsx below
  // covers Catat's own active/resting distinction).
  it('renders the five destinations in the footer nav, in order', () => {
    renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
    )

    const nav = within(screen.getByRole('navigation', { name: copy.shell.nav.label }))
    const links = nav.getAllByRole('link')
    expect(links.map((link) => link.textContent)).toEqual([
      copy.shell.nav.home,
      copy.shell.nav.history,
      copy.shell.nav.record,
      copy.shell.nav.members,
      copy.shell.nav.settings,
    ])
    expect(nav.getByRole('link', { name: copy.shell.nav.home })).toHaveAttribute('href', '/')
    expect(nav.getByRole('link', { name: copy.shell.nav.history })).toHaveAttribute('href', '/history')
    expect(nav.getByRole('link', { name: copy.shell.nav.record })).toHaveAttribute('href', '/record')
    expect(nav.getByRole('link', { name: copy.shell.nav.members })).toHaveAttribute('href', '/members')
    expect(nav.getByRole('link', { name: copy.shell.nav.settings })).toHaveAttribute('href', '/settings')
  })

  // Every footer target clears the 44px minimum (Design-System.md), Catat's
  // slot included - its circle adds to the target, never replaces it.
  it('gives every footer target at least a 44px touch height', () => {
    renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
    )

    const nav = within(screen.getByRole('navigation', { name: copy.shell.nav.label }))
    for (const link of nav.getAllByRole('link')) {
      expect(link.className).toMatch(/min-h-15/)
    }
  })

  // The active marker is a line above the slot, never color alone, and the
  // same for all five - so Catat's permanently Forest circle does not change
  // with the route and cannot read as "you are here" on every screen
  // (ADR-032's "one consequence must be paid in the same PR").
  it('draws the active line above the current slot only, Catat included', () => {
    const { unmount } = renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
      '/settings',
    )
    const nav = within(screen.getByRole('navigation', { name: copy.shell.nav.label }))
    const marked = nav.getAllByRole('link').filter((link) => link.querySelector('[data-nav-marker]'))
    expect(marked).toEqual([nav.getByRole('link', { name: copy.shell.nav.settings })])
    const restingCircle = nav.getByRole('link', { name: copy.shell.nav.record }).querySelector('[data-nav-raised]')?.className
    expect(restingCircle).toBeTruthy()
    unmount()

    renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
      '/record',
    )
    const record = within(screen.getByRole('navigation', { name: copy.shell.nav.label })).getByRole('link', { name: copy.shell.nav.record })
    expect(record).toHaveAttribute('aria-current', 'page')
    expect(record.querySelector('[data-nav-marker]')).not.toBeNull()
    expect(record.querySelector('[data-nav-raised]')?.className).toBe(restingCircle)
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
  // a deep link and a back button both land on the right tab. Riwayat has
  // no `end`, so it stays current across every /history/* tab.
  it('marks the tab matching the current route, and only that one', () => {
    renderShell(
      <Shell title="Kas RT 04" onLoggedOut={() => {}}>
        <p>isi</p>
      </Shell>,
      '/history/dues',
    )

    const nav = within(screen.getByRole('navigation', { name: copy.shell.nav.label }))
    expect(nav.getByRole('link', { name: copy.shell.nav.history })).toHaveAttribute('aria-current', 'page')
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
