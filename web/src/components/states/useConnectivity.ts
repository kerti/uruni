import { useEffect, useState } from 'react'

import { onConnectivityChange } from '@/lib/api'

/**
 * The connectivity watcher (ADR-008, connection-required half only - no
 * service worker, no manifest, no local store, no queue).
 *
 * navigator.onLine alone lies about captive portals and about a server that
 * is down. So this combines the browser's online/offline events with the API
 * client's failed-request signal (lib/api.ts's onConnectivityChange), which
 * fires false on a network failure and true on any response - letting the
 * banner clear the moment connectivity returns.
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
