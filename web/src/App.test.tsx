import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import App from '@/App'
import { copy } from '@/copy/id'

afterEach(() => {
  vi.unstubAllGlobals()
})

/** A promise this test resolves by hand, to hold the session probe in flight. */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

function sessionResponse(body: { authenticated: boolean; has_account: boolean }) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

const fund = { id: 1, name: 'Kas RT 04', currency: 'IDR', report_slug: 'kas-rt-04', created_at: 1234 }

function fundFoundResponse() {
  return new Response(JSON.stringify(fund), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

function fundNotFoundResponse() {
  return new Response(JSON.stringify({ error: { code: 'not_found', message: 'no fund' } }), {
    status: 404,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** An authenticated session, wired to answer GET /api/fund with 200 (fund exists). */
function authenticatedWithFund() {
  return routedFetch({
    '/api/session': () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
    '/api/fund': () => Promise.resolve(fundFoundResponse()),
  })
}

/** Routes a stubbed fetch by path, so a single mock can answer both /api/session and /healthz. */
function routedFetch(handlers: Record<string, () => Promise<Response>>) {
  return vi.fn((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString()
    for (const [path, handler] of Object.entries(handlers)) {
      if (url.includes(path)) return handler()
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

describe('App (session probe)', () => {
  it('shows Loading while the session probe is in flight', async () => {
    const probe = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(probe.promise))
    render(<App />)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })

  it('renders an error with retry when the session probe fails, and retry re-probes', async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce(sessionResponse({ authenticated: false, has_account: false }))
    vi.stubGlobal('fetch', fetchMock)

    render(<App />)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(copy.common.errors.network_error)

    await userEvent.click(screen.getByRole('button', { name: copy.common.retry }))
    expect(await screen.findByText(copy.auth.register.heading)).toBeInTheDocument()
  })

  it('renders Register when the instance has no account yet', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(sessionResponse({ authenticated: false, has_account: false })),
    )
    render(<App />)
    expect(await screen.findByText(copy.auth.register.heading)).toBeInTheDocument()
  })

  it('renders Login when an account exists but the caller has no session', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(sessionResponse({ authenticated: false, has_account: true })),
    )
    render(<App />)
    expect(await screen.findByText(copy.auth.login.heading)).toBeInTheDocument()
  })

  it('renders the authenticated placeholder (smoke page) once authenticated and GET /api/fund answers 200', async () => {
    vi.stubGlobal('fetch', authenticatedWithFund())
    render(<App />)
    expect(await screen.findByText(copy.smoke.heading)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.smoke.check })).toBeInTheDocument()
  })
})

describe('App (fund probe)', () => {
  // The wizard-skipped-on-reload behaviour (#138's own DoD): a GET /api/fund
  // that answers 200 on any later load skips the wizard entirely, including
  // a mid-wizard reload - there is no client-side "setup done" flag, the
  // fund's own existence is the only source of truth.
  it('skips the wizard and renders the placeholder home when GET /api/fund answers 200', async () => {
    vi.stubGlobal('fetch', authenticatedWithFund())
    render(<App />)
    expect(await screen.findByText(copy.smoke.heading)).toBeInTheDocument()
  })

  it('renders the setup wizard when GET /api/fund answers 404 (not_found)', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch({
        '/api/session': () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        '/api/fund': () => Promise.resolve(fundNotFoundResponse()),
      }),
    )
    render(<App />)
    expect(await screen.findByText(copy.setup.fund.heading)).toBeInTheDocument()
  })
})

describe('App (app shell chrome)', () => {
  it('titles the shell with the fund name and puts the authed screen inside it', async () => {
    vi.stubGlobal('fetch', authenticatedWithFund())
    render(<App />)

    expect(await screen.findByRole('heading', { name: fund.name })).toBeInTheDocument()
    expect(screen.getByRole('main')).toHaveTextContent(copy.smoke.heading)
  })

  it('logs out from the shell and lands back on Login, not Register', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch({
        '/api/session': () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        '/api/fund': () => Promise.resolve(fundFoundResponse()),
        '/api/logout': () => Promise.resolve(new Response(null, { status: 204 })),
      }),
    )
    render(<App />)
    await screen.findByText(copy.smoke.heading)

    await userEvent.click(screen.getByRole('button', { name: copy.shell.logout }))

    // has_account stays true through a logout - the account still exists, it
    // is only the session that is gone.
    expect(await screen.findByText(copy.auth.login.heading)).toBeInTheDocument()
    expect(screen.queryByText(copy.auth.register.heading)).not.toBeInTheDocument()
  })

  // Neither auth nor the wizard renders inside the shell: there is no fund to
  // name in the header yet, and no session worth offering a logout for.
  it('does not render the shell around Login or the setup wizard', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch({
        '/api/session': () => Promise.resolve(sessionResponse({ authenticated: false, has_account: true })),
      }),
    )
    const { unmount } = render(<App />)
    await screen.findByText(copy.auth.login.heading)
    expect(screen.queryByRole('button', { name: copy.shell.logout })).not.toBeInTheDocument()
    unmount()

    vi.stubGlobal(
      'fetch',
      routedFetch({
        '/api/session': () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        '/api/fund': () => Promise.resolve(fundNotFoundResponse()),
      }),
    )
    render(<App />)
    await screen.findByText(copy.setup.fund.heading)
    expect(screen.queryByRole('button', { name: copy.shell.logout })).not.toBeInTheDocument()
  })
})

describe('App (record loop)', () => {
  const accounts = [{ id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 }]
  const purposes = [{ id: 11, kind: 'main', name: 'Kas utama', created_at: 1 }]

  function authenticatedWithRecordRoutes() {
    return routedFetch({
      '/api/session': () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
      '/api/fund': () => Promise.resolve(fundFoundResponse()),
      '/api/accounts': () =>
        Promise.resolve(new Response(JSON.stringify(accounts), { headers: { 'Content-Type': 'application/json' } })),
      '/api/purposes': () =>
        Promise.resolve(new Response(JSON.stringify(purposes), { headers: { 'Content-Type': 'application/json' } })),
      '/api/transactions': () => Promise.resolve(new Response(JSON.stringify({ id: 1 }), { status: 201 })),
    })
  }

  it('reaches the form from the add-FAB, posts, and confirms on home', async () => {
    vi.stubGlobal('fetch', authenticatedWithRecordRoutes())
    render(<App />)
    await screen.findByText(copy.smoke.heading)

    await userEvent.click(screen.getByRole('link', { name: copy.record.addAction }))
    await screen.findByRole('heading', { name: copy.record.heading })

    await userEvent.type(screen.getByLabelText(copy.record.amountLabel), '50000')
    await userEvent.click(screen.getByRole('button', { name: copy.record.submit }))

    expect(await screen.findByText(copy.record.successOut)).toBeInTheDocument()
    expect(screen.getByText(copy.smoke.heading)).toBeInTheDocument()
  })

  // The confirmation belongs to the history entry the successful post
  // created, not to the session: opening the form again and coming back must
  // not re-show a message about a transaction recorded minutes ago.
  it('does not re-show the confirmation on a later visit to home', async () => {
    vi.stubGlobal('fetch', authenticatedWithRecordRoutes())
    render(<App />)
    await screen.findByText(copy.smoke.heading)

    await userEvent.click(screen.getByRole('link', { name: copy.record.addAction }))
    await userEvent.type(await screen.findByLabelText(copy.record.amountLabel), '50000')
    await userEvent.click(screen.getByRole('button', { name: copy.record.submit }))
    await screen.findByText(copy.record.successOut)

    await userEvent.click(screen.getByRole('link', { name: copy.record.addAction }))
    await screen.findByRole('heading', { name: copy.record.heading })
    await userEvent.click(screen.getByRole('button', { name: copy.record.cancel }))

    expect(await screen.findByText(copy.smoke.heading)).toBeInTheDocument()
    expect(screen.queryByText(copy.record.successOut)).not.toBeInTheDocument()
  })
})

describe('App (router shell)', () => {
  it('still answers the /healthz smoke check through the router', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch({
        '/api/session': () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        '/api/fund': () => Promise.resolve(fundFoundResponse()),
        '/healthz': () => Promise.resolve(new Response(null, { status: 200 })),
      }),
    )
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: copy.smoke.check }))
    expect(await screen.findByText(copy.smoke.online)).toBeInTheDocument()
  })

  it('says the app needs a connection when the server is unreachable', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch({
        '/api/session': () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
        '/api/fund': () => Promise.resolve(fundFoundResponse()),
        '/healthz': () => Promise.reject(new Error('network down')),
      }),
    )
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: copy.smoke.check }))
    expect(await screen.findByText(copy.smoke.offline)).toBeInTheDocument()
  })
})

describe('App (connectivity watcher)', () => {
  it('shows the offline banner when the browser goes offline, and clears it when connectivity returns', async () => {
    vi.stubGlobal('fetch', authenticatedWithFund())
    render(<App />)
    // Let the session probe's own successful response settle first - it
    // calls the same connectivity watcher (a 2xx counts as "online") and
    // would otherwise race the offline event dispatched below and win,
    // masking it.
    await screen.findByText(copy.smoke.heading)
    expect(screen.queryByText(copy.common.offlineBanner)).not.toBeInTheDocument()

    act(() => {
      window.dispatchEvent(new Event('offline'))
    })
    expect(await screen.findByText(copy.common.offlineBanner)).toBeInTheDocument()

    act(() => {
      window.dispatchEvent(new Event('online'))
    })
    await waitFor(() => expect(screen.queryByText(copy.common.offlineBanner)).not.toBeInTheDocument())
  })
})
