import { Search } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import ErrorState from '@/components/states/ErrorState'
import Loading from '@/components/states/Loading'
import TransactionList from '@/components/TransactionList'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { getBalances } from '@/lib/balances'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Balances } from '@/lib/balances'
import type { TransactionsPage } from '@/lib/transactions'

const text = copy.history.transactions

/** Long enough that a word typed at normal speed is one request, short
 * enough that the list follows her without a visible pause. */
const SEARCH_DEBOUNCE_MS = 300

interface FirstPage {
  balances: Balances
  page: TransactionsPage
}

/**
 * Riwayat's Transaksi tab (M6.23, paged and searchable as of #225, ADR-032
 * "Lists: paging and search"): newest-first, 25 a page, "muat lebih banyak"
 * rather than infinite scroll.
 *
 * The search lives in the URL (`?q=`), for the same reason the tab itself is
 * a route: back and reload land on the same view. The field is local state
 * only while she types; after SEARCH_DEBOUNCE_MS it is written to the URL
 * with `replace` - one history entry for the search, not one per keystroke -
 * and the URL is what drives the fetch.
 *
 * Search always goes to the server - filtering the rows already loaded would
 * answer for 25 rows while looking like an answer for all of them. A search
 * that cannot reach the server shows ErrorState's connection copy, never
 * the no-results line.
 *
 * `refetchKey` is App.tsx's `location.key`, the same mechanism Home already
 * uses to pick up a transaction just recorded elsewhere.
 */
export default function Transactions({ refetchKey }: { refetchKey?: unknown }) {
  const [searchParams, setSearchParams] = useSearchParams()
  const q = (searchParams.get('q') ?? '').trim()
  const [draft, setDraft] = useState(q)

  const [state, run] = useApi<FirstPage>()
  // Pages after the first, appended in order. Reset whenever the first page
  // is refetched, and `generation` stops a slow "load more" for the previous
  // search from appending its rows to the new one.
  const [more, setMore] = useState<TransactionsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  async function loadFirstPage(): Promise<FirstPage> {
    const [balances, page] = await Promise.all([getBalances(), listTransactions({ q: q || undefined })])
    return { balances, page }
  }

  useEffect(() => {
    generation.current += 1
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void run(loadFirstPage)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, q, refetchKey])

  // The URL changed under the field (back, forward, a link): follow it. A
  // draft that already trims to the same search is left alone, so a trailing
  // space she is still typing is not snatched away.
  useEffect(() => {
    setDraft((current) => (current.trim() === q ? current : q))
  }, [q])

  useEffect(() => {
    const next = draft.trim()
    if (next === q) return
    const timer = window.setTimeout(() => {
      setSearchParams(next ? { q: next } : {}, { replace: true })
    }, SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [draft, q, setSearchParams])

  async function loadMore(cursor: string) {
    const startedIn = generation.current
    setMoreLoading(true)
    setMoreError(null)
    try {
      const page = await listTransactions({ q: q || undefined, cursor })
      if (startedIn !== generation.current) return
      setMore((prev) => ({ transactions: [...(prev?.transactions ?? []), ...page.transactions], nextCursor: page.nextCursor }))
    } catch (err) {
      if (startedIn !== generation.current) return
      setMoreError(err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err)))
    } finally {
      if (startedIn === generation.current) setMoreLoading(false)
    }
  }

  function renderList() {
    if (state.status === 'idle' || state.status === 'loading') {
      return <Loading />
    }

    if (state.status === 'error' || !state.data) {
      return state.error ? <ErrorState error={state.error} onRetry={() => void run(loadFirstPage)} /> : null
    }

    const { balances, page } = state.data
    const purposeNames = new Map(balances.purposes.map((purpose) => [purpose.id, purpose.name]))
    const transactions = more ? [...page.transactions, ...more.transactions] : page.transactions
    const nextCursor = more ? more.nextCursor : page.nextCursor

    return (
      <>
        <TransactionList
          transactions={transactions}
          purposeNames={purposeNames}
          emptyMessage={q ? text.noResults(q) : copy.home.recentActivityEmpty}
        />
        {nextCursor && moreError && <ErrorState error={moreError} onRetry={() => void loadMore(nextCursor)} />}
        {nextCursor && !moreError && (
          <Button type="button" variant="outline" size="lg" className="w-full" disabled={moreLoading} onClick={() => void loadMore(nextCursor)}>
            {moreLoading ? copy.common.loading : text.loadMore}
          </Button>
        )}
      </>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="relative">
        <Label htmlFor="transaction-search" className="sr-only">
          {text.searchLabel}
        </Label>
        <Search aria-hidden="true" className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          id="transaction-search"
          type="search"
          autoComplete="off"
          placeholder={text.searchPlaceholder}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          className="pl-9"
        />
      </div>
      {renderList()}
    </div>
  )
}
