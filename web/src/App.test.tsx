import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import App from '@/App'
import { copy } from '@/copy/id'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('App (router shell)', () => {
  it('resolves the placeholder route to the smoke page', () => {
    render(<App />)
    expect(screen.getByText(copy.smoke.heading)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.smoke.check })).toBeInTheDocument()
  })

  it('still answers the /healthz smoke check through the router', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 200 })))
    render(<App />)
    await userEvent.click(screen.getByRole('button', { name: copy.smoke.check }))
    expect(await screen.findByText(copy.smoke.online)).toBeInTheDocument()
  })

  it('says the app needs a connection when the server is unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('network down')))
    render(<App />)
    await userEvent.click(screen.getByRole('button', { name: copy.smoke.check }))
    expect(await screen.findByText(copy.smoke.offline)).toBeInTheDocument()
  })
})

describe('App (connectivity watcher)', () => {
  it('shows the offline banner when the browser goes offline, and clears it when connectivity returns', async () => {
    render(<App />)
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
