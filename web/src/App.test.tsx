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

  it('renders the authenticated placeholder (smoke page) once authenticated', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(sessionResponse({ authenticated: true, has_account: true })),
    )
    render(<App />)
    expect(await screen.findByText(copy.smoke.heading)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.smoke.check })).toBeInTheDocument()
  })
})

describe('App (router shell)', () => {
  it('still answers the /healthz smoke check through the router', async () => {
    vi.stubGlobal(
      'fetch',
      routedFetch({
        '/api/session': () => Promise.resolve(sessionResponse({ authenticated: true, has_account: true })),
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
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(sessionResponse({ authenticated: true, has_account: true })),
    )
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
