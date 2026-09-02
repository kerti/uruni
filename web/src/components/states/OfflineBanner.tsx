import { useEffect, useState } from 'react'
import { WifiOff } from 'lucide-react'

import { copy } from '@/copy/id'
import { onConnectivityChange } from '@/lib/api'

/**
 * The connectivity watcher (ADR-008, connection-required half only - no
 * service worker, no manifest, no local store, no queue; M6.7 owns the
 * installable half).
 *
 * navigator.onLine alone is not enough: it lies about captive portals and
 * about a server that is simply down. So this combines the browser's
 * online/offline events with the API client's own failed-request signal
 * (lib/api.ts's onConnectivityChange), which fires false on a network
 * failure and true on any response, success or not - that is what lets the
 * banner clear the moment connectivity actually returns.
 */
export function useConnectivity(): boolean {
  const [online, setOnline] = useState(() => (typeof navigator === 'undefined' ? true : navigator.onLine))

  useEffect(() => {
    const goOnline = () => setOnline(true)
    const goOffline = () => setOnline(false)

    window.addEventListener('online', goOnline)
    window.addEventListener('offline', goOffline)
    const unsubscribe = onConnectivityChange(setOnline)

    return () => {
      window.removeEventListener('online', goOnline)
      window.removeEventListener('offline', goOffline)
      unsubscribe()
    }
  }, [])

  return online
}

/**
 * Renders nothing while connected. When offline, a calm terracotta banner -
 * --attention tokens, never --destructive: a lost connection is a normal
 * condition for this app, not an alarm (Design-System.md, CLAUDE.md rule 9).
 */
export default function OfflineBanner() {
  const online = useConnectivity()

  if (online) return null

  return (
    <div role="status" className="flex items-center gap-2 bg-attention-soft px-4 py-2 text-sm text-attention">
      <WifiOff aria-hidden="true" className="size-4 shrink-0" />
      {copy.common.offlineBanner}
    </div>
  )
}
