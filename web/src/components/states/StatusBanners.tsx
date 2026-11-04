import { useContext, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

import { BannerSlotContext } from '@/components/states/bannerSlot'
import OfflineBanner from '@/components/states/OfflineBanner'
import UpdateBanner from '@/components/states/UpdateBanner'

/** Holds the element Shell's fixed header offers the banners, if Shell is up. */
export function BannerSlotProvider({ children }: { children: ReactNode }) {
  const [slot, setSlot] = useState<HTMLElement | null>(null)
  return <BannerSlotContext.Provider value={{ slot, setSlot }}>{children}</BannerSlotContext.Provider>
}

/**
 * The app-wide banners - offline, then a waiting update - mounted once, above
 * the routes. Where they paint depends on the screen: Shell's header is fixed,
 * so a banner in the page's flow would sit beneath it, hidden. While Shell is
 * mounted the banners portal into its header and stay pinned with it, and
 * Shell measures the header so its content still starts below them. On the
 * screens without Shell (login, register, setup) they render here, in flow.
 */
export default function StatusBanners() {
  const { slot } = useContext(BannerSlotContext)
  const banners = (
    <>
      <OfflineBanner />
      <UpdateBanner />
    </>
  )
  return slot ? createPortal(banners, slot) : banners
}
