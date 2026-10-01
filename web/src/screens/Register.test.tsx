import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Register from '@/screens/Register'
import { copy } from '@/copy/id'

afterEach(() => {
  vi.unstubAllGlobals()
})

function errorResponse(code: string, status: number) {
  return new Response(JSON.stringify({ error: { code, message: 'server says so' } }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** Answers the footer's own /healthz read (AuthChrome, #364) with a 404 -
 * a footnote that fails quietly - and hands every other request to
 * `api`, so a test's stubbed response is never consumed by the footer. */
function stubApi(api: (input: RequestInfo | URL) => Promise<Response> = () => Promise.reject(new Error('unstubbed'))) {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    if (String(input).includes('/healthz')) return Promise.resolve(new Response(null, { status: 404 }))
    return api(input)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

/** The calls that were not the footer's /healthz read. */
function apiCalls(fetchMock: ReturnType<typeof stubApi>) {
  return fetchMock.mock.calls.filter(([input]) => !String(input).includes('/healthz'))
}

async function fillAndSubmit(email: string, password: string) {
  await userEvent.type(screen.getByLabelText(copy.auth.register.emailLabel), email)
  await userEvent.type(screen.getByLabelText(copy.auth.register.passwordLabel), password)
  await userEvent.click(screen.getByRole('button', { name: copy.auth.register.submit }))
}

describe('Register', () => {
  it('registers and hands the new user back to the caller', async () => {
    const user = { id: 1, email: 'bendahara@example.test', created_at: 1234 }
    stubApi(() => Promise.resolve(new Response(JSON.stringify(user), { status: 201, headers: { 'Content-Type': 'application/json' } })))
    const onRegistered = vi.fn()
    render(<Register onRegistered={onRegistered} />)

    await fillAndSubmit('bendahara@example.test', 'super-secret-1')

    await vi.waitFor(() => expect(onRegistered).toHaveBeenCalledWith(user))
  })

  it('renders a 409 (already registered) as an ordinary error, not a crash', async () => {
    stubApi(() => Promise.resolve(errorResponse('already_registered', 409)))
    const onRegistered = vi.fn()
    render(<Register onRegistered={onRegistered} />)

    await fillAndSubmit('bendahara@example.test', 'super-secret-1')

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.already_registered)
    expect(onRegistered).not.toHaveBeenCalled()
  })

  it('guards the 8-character password floor client-side without sending a request', async () => {
    const fetchMock = stubApi()
    const onRegistered = vi.fn()
    render(<Register onRegistered={onRegistered} />)

    await fillAndSubmit('bendahara@example.test', 'short')

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.auth.register.passwordTooShort)
    expect(apiCalls(fetchMock)).toHaveLength(0)
    expect(onRegistered).not.toHaveBeenCalled()
  })
})

describe('Register frame (#364)', () => {
  it('shows the tagline, the version, and links the source and licence of the running build', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(
          new Response(JSON.stringify({ status: 'ok', version: 'v0.6.0-alpha.10', commit: 'abcdef1234567' }), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          }),
        ),
      ),
    )
    render(<Register onRegistered={vi.fn()} />)

    expect(screen.getByText(copy.auth.chrome.tagline)).toBeInTheDocument()
    expect(await screen.findByText(copy.settings.versionLine('v0.6.0-alpha.10'))).toBeInTheDocument()
    expect(screen.getByRole('link', { name: copy.auth.chrome.sourceCode })).toHaveAttribute(
      'href',
      'https://github.com/kerti/uruni/tree/v0.6.0-alpha.10',
    )
    expect(screen.getByRole('link', { name: copy.auth.chrome.license })).toHaveAttribute(
      'href',
      'https://github.com/kerti/uruni/blob/v0.6.0-alpha.10/LICENSE',
    )
  })
})
