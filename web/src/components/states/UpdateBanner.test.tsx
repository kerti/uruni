import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import UpdateBanner from '@/components/states/UpdateBanner'
import { copy } from '@/copy/id'

const needRefresh = vi.fn(() => false)
const updateServiceWorker = vi.fn(async (_reloadPage?: boolean) => {})

vi.mock('virtual:pwa-register/react', () => ({
  useRegisterSW: () => ({
    needRefresh: [needRefresh(), () => {}],
    offlineReady: [false, () => {}],
    updateServiceWorker,
  }),
}))

beforeEach(() => {
  needRefresh.mockReturnValue(false)
  updateServiceWorker.mockClear()
})

describe('UpdateBanner', () => {
  it('renders nothing while no new build is waiting', () => {
    const { container } = render(<UpdateBanner />)
    expect(container).toBeEmptyDOMElement()
  })

  it('offers the reload once a new build is waiting', () => {
    needRefresh.mockReturnValue(true)
    render(<UpdateBanner />)

    expect(screen.getByRole('status')).toHaveTextContent(copy.common.updateAvailable)
    expect(screen.getByRole('button', { name: copy.common.updateReload })).toBeInTheDocument()
  })

  it('only activates the waiting service worker when the treasurer taps', async () => {
    needRefresh.mockReturnValue(true)
    render(<UpdateBanner />)

    // The whole point of registerType: 'prompt' - nothing reloads on its own,
    // so a half-typed transaction survives a deploy.
    expect(updateServiceWorker).not.toHaveBeenCalled()

    await userEvent.click(screen.getByRole('button', { name: copy.common.updateReload }))
    expect(updateServiceWorker).toHaveBeenCalledWith(true)
  })
})
