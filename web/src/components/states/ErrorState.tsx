import { useEffect } from 'react'
import { WarningCircle } from '@phosphor-icons/react'

import { Button } from '@/components/ui/button'
import { copy } from '@/copy/id'
import type { ApiError } from '@/lib/api'
import { errorText, type ErrorMessages } from '@/lib/errorCopy'

/**
 * Renders an ApiError as warm Indonesian copy.
 *
 * Never renders the wire `message` - it is English by design (ADR-014: the
 * API is a code surface) and showing it breaks CLAUDE.md rule 8. The raw
 * message goes to console.error for the maintainer only. `messages` are a
 * screen's own words for some codes, checked before the shared map; an
 * unmapped code falls back to a warm generic sentence (lib/errorCopy.ts).
 *
 * Terracotta --attention, not --destructive: Design-System.md reserves
 * --destructive for destructive actions (delete), and a failed request is not
 * one. Same as OfflineBanner - no alarm over an ordinary failure.
 */
export default function ErrorState({ error, onRetry, messages }: { error: ApiError; onRetry?: () => void; messages?: ErrorMessages }) {
  useEffect(() => {
    console.error('API error', error.code, error.message)
  }, [error])

  const text = errorText(error, messages)

  return (
    <div role="alert" className="flex flex-col items-start gap-2 text-attention-text">
      <p className="flex items-center gap-2">
        <WarningCircle aria-hidden="true" />
        {text}
      </p>
      {/* Forest and full width (#313): retrying is the only way forward from
          here and harmless to tap, so it is the primary action - the
          terracotta line above carries the error, not the button. */}
      {onRetry && (
        <Button size="lg" className="self-stretch" onClick={onRetry}>
          {copy.common.retry}
        </Button>
      )}
    </div>
  )
}
