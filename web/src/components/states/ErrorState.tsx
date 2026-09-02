import { useEffect } from 'react'
import { CircleAlert } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { copy } from '@/copy/id'
import type { ApiError } from '@/lib/api'

const errorCopy: Record<string, string> = copy.common.errors

/**
 * Renders an ApiError as warm Indonesian copy.
 *
 * Never renders the wire `message` - it is English by design (ADR-014: the
 * API is a code surface) and putting it in front of the treasurer breaks
 * CLAUDE.md rule 8. The raw message goes to console.error for the maintainer
 * only. An unmapped code falls back to a warm generic sentence.
 *
 * Terracotta --attention, not --destructive: Design-System.md reserves
 * --destructive for destructive actions (delete), and a request that failed
 * is not one. Same reasoning as OfflineBanner - this app does not alarm the
 * treasurer over an ordinary failure.
 */
export default function ErrorState({ error, onRetry }: { error: ApiError; onRetry?: () => void }) {
  useEffect(() => {
    // eslint-disable-next-line no-console
    console.error('API error', error.code, error.message)
  }, [error])

  const text = errorCopy[error.code] ?? copy.common.unknownError

  return (
    <div role="alert" className="flex flex-col items-start gap-2 text-attention">
      <p className="flex items-center gap-2">
        <CircleAlert aria-hidden="true" />
        {text}
      </p>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          {copy.common.retry}
        </Button>
      )}
    </div>
  )
}
