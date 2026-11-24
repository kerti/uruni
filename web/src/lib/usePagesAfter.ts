import { useState } from 'react'

import { type ApiError, toApiError } from '@/lib/api'

/** A keyset page as every lib client returns one (ADR-032). */
export interface CursorPage {
  nextCursor: string | null
}

export interface PagesAfter<Row> {
  /** The first page's rows, then each page loaded after it, in order. Empty
   * while there is no first page. */
  rows: Row[]
  /** The cursor "muat lebih banyak" would follow; null when there is no further page. */
  nextCursor: string | null
  /** A page after the first is on its way. */
  loading: boolean
  /** The last load-more failed; the rows already shown stay. */
  error: ApiError | null
  /** Loads the page after the last one shown. Clears `error` first, so it is also the retry. */
  loadMore: () => Promise<void>
}

interface Loaded<P> {
  /** The first page these pages follow. They count only while it is the one on screen. */
  after: P | undefined
  pages: P[]
  loading: boolean
  error: ApiError | null
}

/**
 * "Muat lebih banyak" for a keyset list whose first page the screen loads
 * itself (often alongside lookups it needs anyway, so the first page stays in
 * the screen's own useApi).
 *
 * Every page loaded here belongs to the `first` it was loaded after, compared
 * by identity. A new first page - a new search, a filter, a refresh, or the
 * screen dropping to Loading - starts the list over without the screen
 * resetting anything, and a page that arrives for an older first page is
 * dropped rather than appended to the new one.
 */
export function usePagesAfter<P extends CursorPage, Row>(
  first: P | undefined,
  fetchAfter: (cursor: string) => Promise<P>,
  rowsOf: (page: P) => Row[],
): PagesAfter<Row> {
  const [loaded, setLoaded] = useState<Loaded<P>>({ after: first, pages: [], loading: false, error: null })
  const current: Loaded<P> = loaded.after === first ? loaded : { after: first, pages: [], loading: false, error: null }

  const last = current.pages.length > 0 ? current.pages[current.pages.length - 1] : first
  const nextCursor = last?.nextCursor ?? null
  const rows = first ? [first, ...current.pages].flatMap(rowsOf) : []

  async function loadMore() {
    if (!first || nextCursor === null || current.loading) return
    const after = first
    const fresh = (s: Loaded<P>): Loaded<P> => (s.after === after ? s : { after, pages: [], loading: false, error: null })
    setLoaded((s) => ({ ...fresh(s), loading: true, error: null }))
    try {
      const page = await fetchAfter(nextCursor)
      setLoaded((s) => (s.after === after ? { ...s, pages: [...s.pages, page], loading: false } : s))
    } catch (err) {
      setLoaded((s) => (s.after === after ? { ...s, loading: false, error: toApiError(err) } : s))
    }
  }

  return { rows, nextCursor, loading: current.loading, error: current.error, loadMore }
}
