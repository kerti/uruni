import { WifiSlash } from '@phosphor-icons/react'

import { copy } from '@/copy/id'
import { useConnectivity } from '@/components/states/useConnectivity'

/**
 * Renders nothing while connected. When offline, a calm terracotta banner -
 * --attention tokens, never --destructive: a lost connection is a normal
 * condition for this app, not an alarm (Design-System.md, CLAUDE.md rule 9).
 */
export default function OfflineBanner() {
  const online = useConnectivity()

  if (online) return null

  return (
    <div role="status" className="flex items-center gap-2 bg-attention-soft px-4 py-2 text-sm text-attention-text">
      <WifiSlash aria-hidden="true" className="size-4 shrink-0" />
      {copy.common.offlineBanner}
    </div>
  )
}
