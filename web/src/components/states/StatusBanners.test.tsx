import { act, render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MemoryRouter } from 'react-router-dom'

import Shell from '@/components/Shell'
import StatusBanners, { BannerSlotProvider } from '@/components/states/StatusBanners'
import { copy } from '@/copy/id'

function goOffline() {
  act(() => {
    window.dispatchEvent(new Event('offline'))
  })
}

function goOnline() {
  act(() => {
    window.dispatchEvent(new Event('online'))
  })
}

describe('StatusBanners', () => {
  it('renders in the page flow on a screen without Shell', async () => {
    render(
      <BannerSlotProvider>
        <StatusBanners />
        <main>login</main>
      </BannerSlotProvider>,
    )
    goOffline()

    expect(await screen.findByText(copy.common.offlineBanner)).toBeInTheDocument()
    goOnline()
  })

  // Shell's header is fixed: a banner left in the page's flow sits beneath it,
  // hidden. Inside the header it is pinned with it.
  it("moves into Shell's fixed header while Shell is mounted", async () => {
    render(
      <MemoryRouter>
        <BannerSlotProvider>
          <StatusBanners />
          <Shell title="Kas RT 04" onLoggedOut={() => {}}>
            <p>isi</p>
          </Shell>
        </BannerSlotProvider>
      </MemoryRouter>,
    )
    goOffline()

    const banner = await screen.findByText(copy.common.offlineBanner)
    expect(within(screen.getByRole('banner')).getByText(copy.common.offlineBanner)).toBe(banner)
    goOnline()
  })

  it('sets --shell-header while Shell is mounted and clears it after', () => {
    const { unmount } = render(
      <MemoryRouter>
        <BannerSlotProvider>
          <Shell title="Kas RT 04" onLoggedOut={() => {}}>
            <p>isi</p>
          </Shell>
        </BannerSlotProvider>
      </MemoryRouter>,
    )
    expect(document.documentElement.style.getPropertyValue('--shell-header')).not.toBe('')

    unmount()
    expect(document.documentElement.style.getPropertyValue('--shell-header')).toBe('')
  })
})
