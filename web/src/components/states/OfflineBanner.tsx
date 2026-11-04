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
    <div role="status" className="bg-attention-soft text-sm text-attention-text">
      <div className="mx-auto flex w-full max-w-xl items-center gap-2 py-2 pl-[max(1rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))]">
        <WifiSlash aria-hidden="true" className="size-4 shrink-0" />
        {copy.common.offlineBanner}
      </div>
    </div>
  )
}
