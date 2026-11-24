import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import { copy } from '@/copy/id'
import type { PagesAfter } from '@/lib/usePagesAfter'

/**
 * "Muat lebih banyak" under a keyset list (ADR-032, #225: a button, never
 * infinite scroll). Nothing once the last page is shown; the error in place of
 * the button when a page failed, whose retry loads that same page.
 */
export default function LoadMore({ pages }: { pages: PagesAfter<unknown> }) {
  if (pages.nextCursor === null) return null
  if (pages.error) return <ErrorState error={pages.error} onRetry={() => void pages.loadMore()} />
  return (
    <Button type="button" variant="outline" size="lg" className="w-full" disabled={pages.loading} onClick={() => void pages.loadMore()}>
      {pages.loading ? copy.common.loading : copy.common.loadMore}
    </Button>
  )
}
