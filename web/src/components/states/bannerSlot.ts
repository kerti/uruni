import { createContext, useContext } from 'react'

/** The element Shell's fixed header offers the app-wide banners, while Shell
 * is mounted (StatusBanners.tsx). Its own module so StatusBanners.tsx exports
 * only components, which fast refresh needs. */
export const BannerSlotContext = createContext<{ slot: HTMLElement | null; setSlot: (el: HTMLElement | null) => void }>({
  slot: null,
  setSlot: () => {},
})

/** Shell's ref callback for the slot inside its header. */
export function useBannerSlot() {
  return useContext(BannerSlotContext).setSlot
}
