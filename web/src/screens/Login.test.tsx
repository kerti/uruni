import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Login from '@/screens/Login'
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

  it('falls through to the shared error state on a network failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network down')))
    const onLoggedIn = vi.fn()
    render(<Login onLoggedIn={onLoggedIn} />)

    await fillAndSubmit('bendahara@example.test', 'some-password')

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.network_error)
  })

  it('logs in and hands the user back to the caller', async () => {
    const user = { id: 1, email: 'bendahara@example.test', created_at: 1234 }
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify(user), { status: 200, headers: { 'Content-Type': 'application/json' } })),
    )
    const onLoggedIn = vi.fn()
    render(<Login onLoggedIn={onLoggedIn} />)

    await fillAndSubmit('bendahara@example.test', 'super-secret-1')

    await vi.waitFor(() => expect(onLoggedIn).toHaveBeenCalledWith(user))
  })
})
