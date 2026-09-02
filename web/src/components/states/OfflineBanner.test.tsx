import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import OfflineBanner from '@/components/states/OfflineBanner'
import { copy } from '@/copy/id'
import { apiFetch } from '@/lib/api'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('OfflineBanner', () => {
  it('renders nothing while connected', () => {
    render(<OfflineBanner />)
    expect(screen.queryByText(copy.common.offlineBanner)).not.toBeInTheDocument()
  })

  it('appears on a browser offline event and clears on a browser online event', async () => {
    render(<OfflineBanner />)

    act(() => {
      window.dispatchEvent(new Event('offline'))
    })
    expect(await screen.findByText(copy.common.offlineBanner)).toBeInTheDocument()

    act(() => {
      window.dispatchEvent(new Event('online'))
    })
    await waitFor(() => expect(screen.queryByText(copy.common.offlineBanner)).not.toBeInTheDocument())
  })

  it('appears when the API client sees a network failure, since navigator.onLine alone can lie', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    render(<OfflineBanner />)

    await expect(apiFetch('/api/session')).rejects.toThrow()
    expect(await screen.findByText(copy.common.offlineBanner)).toBeInTheDocument()
  })

  it('clears once the API client sees a successful response again', async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: false, has_account: false }), { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    render(<OfflineBanner />)

    await expect(apiFetch('/api/session')).rejects.toThrow()
    expect(await screen.findByText(copy.common.offlineBanner)).toBeInTheDocument()

    await apiFetch('/api/session')
    await waitFor(() => expect(screen.queryByText(copy.common.offlineBanner)).not.toBeInTheDocument())
  })
})
