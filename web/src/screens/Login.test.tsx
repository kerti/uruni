import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Login from '@/screens/Login'
import { copy } from '@/copy/id'
import type { AuthUser } from '@/lib/auth'

afterEach(() => {
  vi.unstubAllGlobals()
})

function errorResponse(code: string, status: number) {
  return new Response(JSON.stringify({ error: { code, message: 'server says so' } }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

async function fillAndSubmit(email: string, password: string) {
  await userEvent.type(screen.getByLabelText(copy.auth.login.emailLabel), email)
  await userEvent.type(screen.getByLabelText(copy.auth.login.passwordLabel), password)
  await userEvent.click(screen.getByRole('button', { name: copy.auth.login.submit }))
}

describe('Login', () => {
  it('renders its own specific message for a 401 (wrong email or password)', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(errorResponse('invalid_credentials', 401)))
    const onLoggedIn = vi.fn()
    render(<Login onLoggedIn={onLoggedIn} />)

    await fillAndSubmit('bendahara@example.test', 'wrong-password')

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.auth.login.invalidCredentials)
    expect(onLoggedIn).not.toHaveBeenCalled()
  })

  it('renders its own specific message for a 429 (rate limited), distinct from the 401 message', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(errorResponse('too_many_requests', 429)))
    const onLoggedIn = vi.fn()
    render(<Login onLoggedIn={onLoggedIn} />)

    await fillAndSubmit('bendahara@example.test', 'wrong-password')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(copy.auth.login.tooManyRequests)
    expect(alert).not.toHaveTextContent(copy.auth.login.invalidCredentials)
  })

  it('renders a 503 (server busy) as the shared "try again in a moment" copy, not a lockout', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(errorResponse('server_busy', 503)))
    const onLoggedIn = vi.fn()
    render(<Login onLoggedIn={onLoggedIn} />)

    await fillAndSubmit('bendahara@example.test', 'some-password')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(copy.common.errors.server_busy)
    expect(alert).not.toHaveTextContent(copy.auth.login.tooManyRequests)
    expect(onLoggedIn).not.toHaveBeenCalled()
  })

  it('falls through to the shared error state on a network failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network down')))
    const onLoggedIn = vi.fn()
    render(<Login onLoggedIn={onLoggedIn} />)

    await fillAndSubmit('bendahara@example.test', 'some-password')

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.network_error)
  })

  it('logs in and hands the user back to the caller', async () => {
    const user = { id: 1, email: 'bendahara@example.test', created_at: 1234 } satisfies AuthUser
    // A fresh Response per call: the footer's /healthz read (AppVersion) would
    // otherwise consume the one body the login call needs.
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(JSON.stringify(user), { status: 200, headers: { 'Content-Type': 'application/json' } }))),
    )
    const onLoggedIn = vi.fn()
    render(<Login onLoggedIn={onLoggedIn} />)

    await fillAndSubmit('bendahara@example.test', 'super-secret-1')

    await vi.waitFor(() => expect(onLoggedIn).toHaveBeenCalledWith(user))
  })
})

describe('Login in the public demo (ADR-045)', () => {
  const demo = { email: 'demo@uruni.test', password: 'coba-uruni' }

  it('starts filled in with the demo login, under a notice that spells it out', () => {
    render(<Login onLoggedIn={vi.fn()} demo={demo} />)

    expect(screen.getByLabelText(copy.auth.login.emailLabel)).toHaveValue(demo.email)
    expect(screen.getByLabelText(copy.auth.login.passwordLabel)).toHaveValue(demo.password)

    const notice = screen.getByRole('note')
    expect(notice).toHaveTextContent(copy.auth.login.demo.title)
    expect(notice).toHaveTextContent(copy.auth.login.demo.body)
    // The credentials in the notice itself, for a visitor who clears the fields.
    expect(within(notice).getByText(demo.email)).toBeInTheDocument()
    expect(within(notice).getByText(demo.password)).toBeInTheDocument()
  })

  it('signs in with the pre-filled login as it stands', async () => {
    const user = { id: 1, email: demo.email, created_at: 1 } satisfies AuthUser
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) =>
      Promise.resolve(new Response(JSON.stringify(user), { status: 200, headers: { 'Content-Type': 'application/json' } })),
    )
    vi.stubGlobal('fetch', fetchMock)
    const onLoggedIn = vi.fn()
    render(<Login onLoggedIn={onLoggedIn} demo={demo} />)

    await userEvent.click(screen.getByRole('button', { name: copy.auth.login.submit }))

    await vi.waitFor(() => expect(onLoggedIn).toHaveBeenCalledWith(user))
    const login = fetchMock.mock.calls.find(([url]) => String(url) === '/api/login')
    expect(JSON.parse(String(login?.[1]?.body))).toEqual({ email: demo.email, password: demo.password })
  })

  it('is an empty form with no notice everywhere else', () => {
    render(<Login onLoggedIn={vi.fn()} />)

    expect(screen.getByLabelText(copy.auth.login.emailLabel)).toHaveValue('')
    expect(screen.getByLabelText(copy.auth.login.passwordLabel)).toHaveValue('')
    expect(screen.queryByRole('note')).not.toBeInTheDocument()
    expect(screen.queryByText(copy.auth.login.demo.title)).not.toBeInTheDocument()
  })
})

describe('Login footer', () => {
  it('shows the version and links the source and licence of the running build', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify({ status: 'ok', version: 'v0.6.0-alpha.9', commit: 'abcdef1234567' }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }),
        ),
      ),
    )
    render(<Login onLoggedIn={vi.fn()} />)

    expect(await screen.findByText(copy.settings.versionLine('v0.6.0-alpha.9'))).toBeInTheDocument()
    expect(screen.getByRole('link', { name: copy.auth.chrome.sourceCode })).toHaveAttribute(
      'href',
      'https://github.com/kerti/uruni/tree/v0.6.0-alpha.9',
    )
    expect(screen.getByRole('link', { name: copy.auth.chrome.license })).toHaveAttribute(
      'href',
      'https://github.com/kerti/uruni/blob/v0.6.0-alpha.9/LICENSE',
    )
    const notices = screen.getByRole('link', { name: copy.auth.chrome.thirdParty })
    expect(notices).toHaveAttribute('href', '/third-party-notices')
    expect(notices).toHaveAttribute('target', '_blank')
    expect(screen.getByRole('link', { name: copy.auth.chrome.maintainer })).toHaveAttribute('href', 'https://radityakertiyasa.com')
  })
})
