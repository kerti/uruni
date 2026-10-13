import { useEffect, useState } from 'react'

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
