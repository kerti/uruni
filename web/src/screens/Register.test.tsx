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

async function fillAndSubmit(email: string, password: string) {
  await userEvent.type(screen.getByLabelText(copy.auth.register.emailLabel), email)
  await userEvent.type(screen.getByLabelText(copy.auth.register.passwordLabel), password)
  await userEvent.click(screen.getByRole('button', { name: copy.auth.register.submit }))
}

describe('Register', () => {
  it('registers and hands the new user back to the caller', async () => {
    const user = { id: 1, email: 'bendahara@example.test', created_at: 1234 }
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify(user), { status: 201, headers: { 'Content-Type': 'application/json' } })),
    )
    const onRegistered = vi.fn()
    render(<Register onRegistered={onRegistered} />)

    await fillAndSubmit('bendahara@example.test', 'super-secret-1')

    await vi.waitFor(() => expect(onRegistered).toHaveBeenCalledWith(user))
  })

  it('renders a 409 (already registered) as an ordinary error, not a crash', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(errorResponse('already_registered', 409)))
    const onRegistered = vi.fn()
    render(<Register onRegistered={onRegistered} />)

    await fillAndSubmit('bendahara@example.test', 'super-secret-1')

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.common.errors.already_registered)
    expect(onRegistered).not.toHaveBeenCalled()
  })

  it('guards the 8-character password floor client-side without sending a request', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const onRegistered = vi.fn()
    render(<Register onRegistered={onRegistered} />)

    await fillAndSubmit('bendahara@example.test', 'short')

    expect(await screen.findByRole('alert')).toHaveTextContent(copy.auth.register.passwordTooShort)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(onRegistered).not.toHaveBeenCalled()
  })
})
