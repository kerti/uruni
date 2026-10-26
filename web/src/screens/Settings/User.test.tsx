import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import User from '@/screens/Settings/User'
import { copy } from '@/copy/id'
import { DemoMode } from '@/lib/demoMode'
import type { AuthUser } from '@/lib/auth'

const text = copy.settings.user

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const user = { id: 1, email: 'bendahara@example.org', created_at: 1 } satisfies AuthUser

/** GET /api/user answers the signed-in login; each POST is recorded and
 * answered by `reply` (a success by default). */
function stubUser(reply?: (path: string) => Response) {
  const posts: { path: string; body: unknown }[] = []
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    const method = init?.method ?? 'GET'
    if (url === '/api/user' && method === 'GET') return Promise.resolve(jsonResponse(user))
    if (method === 'POST') {
      posts.push({ path: url, body: JSON.parse(String(init?.body)) })
      if (reply) return Promise.resolve(reply(url))
      if (url === '/api/user/email') return Promise.resolve(jsonResponse({ ...user, email: 'baru@example.org' }))
      if (url === '/api/user/password') return Promise.resolve(new Response(null, { status: 204 }))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${method} ${url}`))
  })
  return { fetchMock, posts }
}

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-search">{location.search}</output>
}

function renderAt(entry = '/settings', demo = false) {
  return render(
    <DemoMode.Provider value={demo}>
      <MemoryRouter initialEntries={[entry]}>
        <Routes>
          <Route
            path="/settings"
            element={
              <>
                <User />
                <LocationProbe />
              </>
            }
          />
        </Routes>
      </MemoryRouter>
    </DemoMode.Provider>,
  )
}

function currentSearch() {
  return screen.getByTestId('location-search').textContent
}

describe('Settings user in the public demo (ADR-045)', () => {
  it('shows the usual email form with its save disabled under the demo line', async () => {
    const { fetchMock, posts } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:email', true)

    await userEvent.type(await screen.findByLabelText(text.email.newLabel), 'baru@example.org')
    await userEvent.type(screen.getByLabelText(text.email.currentLabel), 'coba-uruni')

    expect(screen.getByText(text.email.demoLocked)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.email.save })).toBeDisabled()
    expect(posts).toHaveLength(0)
  })

  it('shows the usual password form with its save disabled under the demo line', async () => {
    const { fetchMock, posts } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:password', true)

    await userEvent.type(await screen.findByLabelText(text.password.currentLabel), 'coba-uruni')
    await userEvent.type(screen.getByLabelText(text.password.newLabel), 'kata-sandi-baru')
    await userEvent.type(screen.getByLabelText(text.password.repeatLabel), 'kata-sandi-baru')

    expect(screen.getByText(text.password.demoLocked)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.password.save })).toBeDisabled()
    expect(posts).toHaveLength(0)
  })

  it('says the same sentence for the server refusing, if a request ever gets through', () => {
    expect(copy.common.errors.demo_login_locked).toBe(text.email.demoLocked)
    expect(copy.common.errors.demo_login_locked).toBe(text.password.demoLocked)
    expect(text.email.demoLocked).toBe('Di demo, email dan kata sandi tidak bisa diubah supaya pengunjung lain tetap bisa masuk.')
  })

  it('leaves both buttons on the card, and the forms enabled, outside the demo', async () => {
    const { fetchMock } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:email')

    await userEvent.type(await screen.findByLabelText(text.email.newLabel), 'baru@example.org')
    await userEvent.type(screen.getByLabelText(text.email.currentLabel), 'kata-sandi-lama')

    expect(screen.queryByText(text.email.demoLocked)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.email.save })).toBeEnabled()
  })
})

describe('Settings user', () => {
  it('says who is signed in and offers the two edits, with no form until one is opened', async () => {
    const { fetchMock } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    expect(await screen.findByText(text.body('bendahara@example.org'))).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: text.heading })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.changeEmail })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: text.changePassword })).toBeInTheDocument()
    expect(screen.queryByLabelText(text.email.newLabel)).not.toBeInTheDocument()
  })

  it('opens each edit on the URL, and from a deep link', async () => {
    const { fetchMock } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt()

    await userEvent.click(await screen.findByRole('button', { name: text.changePassword }))
    expect(await screen.findByLabelText(text.password.repeatLabel)).toBeInTheDocument()
    expect(currentSearch()).toBe('?edit=user%3Apassword')
  })

  it('opens the email dialog straight from a deep link, with its note and no extra close button', async () => {
    const { fetchMock } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:email')

    expect(await screen.findByLabelText(text.email.newLabel)).toHaveValue('')
    expect(screen.getByLabelText(text.email.currentLabel)).toHaveValue('')
    expect(screen.getByText(text.email.note)).toBeInTheDocument()
    // The dialog's X is the one way out; the footer holds the save button alone.
    expect(screen.getAllByRole('button', { name: copy.common.close })).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Batal' })).not.toBeInTheDocument()
  })

  it('changes the email with the current password, then closes, shows the new address and says so', async () => {
    const { fetchMock, posts } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:email')

    await userEvent.type(await screen.findByLabelText(text.email.newLabel), 'baru@example.org')
    await userEvent.type(screen.getByLabelText(text.email.currentLabel), 'kata-sandi-lama')
    await userEvent.click(screen.getByRole('button', { name: text.email.save }))

    await waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({ path: '/api/user/email', body: { email: 'baru@example.org', current_password: 'kata-sandi-lama' } })
    await waitFor(() => expect(currentSearch()).toBe(''))
    expect(await screen.findByText(text.body('baru@example.org'))).toBeInTheDocument()
    expect(screen.getByText(text.email.success)).toHaveAttribute('role', 'status')
  })

  it('shows the wrong-current-password sentence and keeps the dialog open', async () => {
    const { fetchMock } = stubUser(() => jsonResponse({ error: { code: 'wrong_current_password', message: 'no' } }, 403))
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:email')

    await userEvent.type(await screen.findByLabelText(text.email.newLabel), 'baru@example.org')
    await userEvent.type(screen.getByLabelText(text.email.currentLabel), 'salah')
    await userEvent.click(screen.getByRole('button', { name: text.email.save }))

    expect(await screen.findByText(copy.common.errors.wrong_current_password)).toBeInTheDocument()
    expect(currentSearch()).toBe('?edit=user:email')
    expect(screen.queryByText(text.email.success)).not.toBeInTheDocument()
  })

  it('shows the too-common-password sentence and keeps the dialog open', async () => {
    const { fetchMock } = stubUser(() => jsonResponse({ error: { code: 'password_too_common', message: 'no' } }, 400))
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:password')

    await userEvent.type(await screen.findByLabelText(text.password.currentLabel), 'kata-sandi-lama')
    await userEvent.type(screen.getByLabelText(text.password.newLabel), 'password')
    await userEvent.type(screen.getByLabelText(text.password.repeatLabel), 'password')
    await userEvent.click(screen.getByRole('button', { name: text.password.save }))

    expect(await screen.findByText(copy.common.errors.password_too_common)).toBeInTheDocument()
    expect(currentSearch()).toBe('?edit=user:password')
  })

  it('will not submit an email dialog with an empty field', async () => {
    const { fetchMock, posts } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:email')

    await userEvent.type(await screen.findByLabelText(text.email.newLabel), 'baru@example.org')
    expect(screen.getByRole('button', { name: text.email.save })).toBeDisabled()
    expect(posts).toHaveLength(0)
  })

  it('changes the password, then closes and says so', async () => {
    const { fetchMock, posts } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:password')

    await userEvent.type(await screen.findByLabelText(text.password.currentLabel), 'kata-sandi-lama')
    await userEvent.type(screen.getByLabelText(text.password.newLabel), 'kata-sandi-baru')
    await userEvent.type(screen.getByLabelText(text.password.repeatLabel), 'kata-sandi-baru')
    await userEvent.click(screen.getByRole('button', { name: text.password.save }))

    await waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toEqual({
      path: '/api/user/password',
      body: { new_password: 'kata-sandi-baru', current_password: 'kata-sandi-lama' },
    })
    await waitFor(() => expect(currentSearch()).toBe(''))
    expect(screen.getByText(text.password.success)).toHaveAttribute('role', 'status')
  })

  // Checked in the browser: a typo in the new password must not cost an attempt
  // at the rate-limited current one.
  it('refuses mismatched new passwords without sending anything', async () => {
    const { fetchMock, posts } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:password')

    await userEvent.type(await screen.findByLabelText(text.password.currentLabel), 'kata-sandi-lama')
    await userEvent.type(screen.getByLabelText(text.password.newLabel), 'kata-sandi-baru')
    await userEvent.type(screen.getByLabelText(text.password.repeatLabel), 'kata-sandi-beda')
    await userEvent.click(screen.getByRole('button', { name: text.password.save }))

    expect(await screen.findByText(text.password.mismatch)).toBeInTheDocument()
    expect(posts).toHaveLength(0)
    expect(currentSearch()).toBe('?edit=user:password')

    // Typing again clears the message.
    await userEvent.type(screen.getByLabelText(text.password.repeatLabel), 'x')
    expect(screen.queryByText(text.password.mismatch)).not.toBeInTheDocument()
  })

  it('refuses a new password under eight characters without sending anything', async () => {
    const { fetchMock, posts } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:password')

    await userEvent.type(await screen.findByLabelText(text.password.currentLabel), 'kata-sandi-lama')
    await userEvent.type(screen.getByLabelText(text.password.newLabel), 'pendek')
    await userEvent.type(screen.getByLabelText(text.password.repeatLabel), 'pendek')
    await userEvent.click(screen.getByRole('button', { name: text.password.save }))

    expect(await screen.findByText(copy.auth.register.passwordTooShort)).toBeInTheDocument()
    expect(posts).toHaveLength(0)
  })

  it('starts each opening from empty fields', async () => {
    const { fetchMock } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    renderAt('/settings?edit=user:password')

    await userEvent.type(await screen.findByLabelText(text.password.currentLabel), 'kata-sandi-lama')
    await userEvent.click(screen.getByRole('button', { name: copy.common.close }))
    await waitFor(() => expect(currentSearch()).toBe(''))

    await userEvent.click(screen.getByRole('button', { name: text.changePassword }))
    expect(await screen.findByLabelText(text.password.currentLabel)).toHaveValue('')
  })

  it('strips an user: value it cannot use, and leaves a sibling section alone', async () => {
    const { fetchMock } = stubUser()
    vi.stubGlobal('fetch', fetchMock)
    const { unmount } = renderAt('/settings?edit=user:nonsense')
    await waitFor(() => expect(currentSearch()).toBe(''))
    unmount()

    renderAt('/settings?edit=location:3')
    await screen.findByRole('button', { name: text.changeEmail })
    expect(currentSearch()).toBe('?edit=location:3')
  })
})
