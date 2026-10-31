import { ArrowsClockwise } from '@phosphor-icons/react'
import { useRegisterSW } from 'virtual:pwa-register/react'

import { Button } from '@/components/ui/button'
import { copy } from '@/copy/id'

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
 * Sits above the routes next to OfflineBanner in the same shape, but
 * secondary/Sage tokens rather than --attention: a waiting update is good
 * news, not a problem.
 */
export default function UpdateBanner() {
  const {
    needRefresh: [needRefresh],
    updateServiceWorker,
  } = useRegisterSW()

  if (!needRefresh) return null

  return (
    <div role="status" className="flex items-center justify-between gap-3 bg-secondary px-4 py-2 text-sm text-secondary-foreground">
      <span className="flex items-center gap-2">
        <ArrowsClockwise aria-hidden="true" className="size-4 shrink-0" />
        {copy.common.updateAvailable}
      </span>
      <Button variant="secondary" size="lg" onClick={() => void updateServiceWorker(true)}>
        {copy.common.updateReload}
      </Button>
    </div>
  )
}
