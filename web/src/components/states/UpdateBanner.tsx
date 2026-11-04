import { createContext, useContext, type ReactNode } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useRegisterSW } from 'virtual:pwa-register/react'

import { Button } from '@/components/ui/button'
import { copy } from '@/copy/id'

const UpdateContext = createContext<{ needRefresh: boolean; reload: () => void }>({ needRefresh: false, reload: () => {} })

/**
 * The only thing that registers the service worker (ADR-008), and the only
 * thing that reloads the app for a new deploy.
 *
 * vite-plugin-pwa runs in `registerType: 'prompt'`: a new build is installed
 * in the background, then waits. Nothing on screen changes and nothing is
 * discarded until the treasurer taps "muat ulang". Deliberate - Uruni is
 * form-heavy, and an autoUpdate reload mid-way through a half-typed
 * transaction would throw the entry away.
 *
 * Mounted once at the app's root, apart from the banner: the banner moves
 * between the page and Shell's fixed header (StatusBanners.tsx), and moving
 * remounts it - the registration and its "a build is waiting" state must not
 * go with it.
 */
export function UpdateProvider({ children }: { children: ReactNode }) {
  const {
    needRefresh: [needRefresh],
    updateServiceWorker,
  } = useRegisterSW()

  return <UpdateContext.Provider value={{ needRefresh, reload: () => void updateServiceWorker(true) }}>{children}</UpdateContext.Provider>
}

/**
 * Sits next to OfflineBanner in the same shape, but secondary/Sage tokens
 * rather than --attention: a waiting update is good news, not a problem.
 */
export default function UpdateBanner() {
  const { needRefresh, reload } = useContext(UpdateContext)

  if (!needRefresh) return null

  return (
    <div role="status" className="bg-secondary text-sm text-secondary-foreground">
      <div className="mx-auto flex w-full max-w-xl items-center justify-between gap-3 py-2 pl-[max(1rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))]">
        <span className="flex items-center gap-2">
          <ArrowsClockwise aria-hidden="true" className="size-4 shrink-0" />
          {copy.common.updateAvailable}
        </span>
        <Button variant="secondary" size="lg" onClick={reload}>
          {copy.common.updateReload}
        </Button>
      </div>
    </div>
  )
}
