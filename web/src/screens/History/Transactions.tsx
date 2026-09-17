import { Search, X } from 'lucide-react'
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
import { parseDialogTarget } from '@/lib/dialogTarget'
import { listPurposes } from '@/lib/purposes'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import CorrectPurposeDialog from '@/screens/History/CorrectPurposeDialog'
import type { Balances } from '@/lib/balances'
import type { Purpose } from '@/lib/purposes'
import type { TransactionsPage } from '@/lib/transactions'

const text = copy.history.transactions

/** Long enough that a word typed at normal speed is one request, short
 * enough that the list follows her without a visible pause. */
const SEARCH_DEBOUNCE_MS = 300

interface FirstPage {
  balances: Balances
  page: TransactionsPage
  /** For the correction dialog's picker (#276). `selectable` excludes a
   * closed envelope's purpose, which is exactly what this picker must not
   * offer: correcting INTO a closed amplop is one of ADR-033's two named
   * refusals. Fetched with the page rather than when the dialog opens, so
   * tapping a peruntukan shows a filled picker instead of a spinner. */
  purposes: Purpose[]
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
 * `?purpose=<id>` (#262) narrows the same list to one peruntukan, and is
 * how ADR-032's only route to a CLOSED envelope's record works: an
 * envelope's detail screen links here. It lives in the URL for the same
 * reason `q` does, and composes with it rather than replacing it - both go
 * to the server, both reset the paging. There is deliberately no chooser
 * for it: the filter can only arrive as a link and can always be cleared,
 * which keeps M7's filter set (ADR-032) from being built here early.
 *
 * `refetchKey` is App.tsx's `location.key`, the same mechanism Home already
 * uses to pick up a transaction just recorded elsewhere.
 */
export default function Transactions({ refetchKey }: { refetchKey?: unknown }) {
  const [searchParams, setSearchParams] = useSearchParams()
  const q = (searchParams.get('q') ?? '').trim()
  const [draft, setDraft] = useState(q)

  // A `?purpose=` that is not a positive integer is treated as absent
  // rather than sent on to be rejected: the same forgiving read App.tsx
  // already gives the param on `/record` and `/incidentals`.
  const rawPurpose = Number(searchParams.get('purpose'))
  const purposeId = Number.isInteger(rawPurpose) && rawPurpose > 0 ? rawPurpose : null

  // The correction dialog (#276) is a search parameter, never component
  // state (ADR-032), so back and Esc close it the same way and a deep link
  // opens it. It shares `?edit=` with nothing else on this screen, but
  // parseDialogTarget is still what reads it - a bare Number() here would
  // treat a foreign value as this screen's own.
  const { value: dialogValue, open: openDialog, close: closeDialog, clear: clearDialog } = useDialogParam()
  const correctionTarget = parseDialogTarget('purpose-correction', dialogValue)
  const [corrected, setCorrected] = useState(false)

  const [state, run] = useApi<FirstPage>()
  // Pages after the first, appended in order. Reset whenever the first page
  // is refetched, and `generation` stops a slow "load more" for the previous
  // search from appending its rows to the new one.
  const [more, setMore] = useState<TransactionsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  async function loadFirstPage(): Promise<FirstPage> {
    const [balances, page, purposes] = await Promise.all([
      getBalances(),
      listTransactions({ q: q || undefined, purposeId: purposeId ?? undefined }),
      listPurposes(true),
    ])
    return { balances, page, purposes }
  }

  useEffect(() => {
    generation.current += 1
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void run(loadFirstPage)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, q, purposeId, refetchKey])

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
      // Rewrites `q` alone - `setSearchParams` replaces the whole query, so
      // building the next one off the current params is what keeps a
      // `?purpose=` filter alive through a search she types inside it.
      setSearchParams(
        (current) => {
          const params = new URLSearchParams(current)
          if (next) params.set('q', next)
          else params.delete('q')
          return params
        },
        { replace: true },
      )
    }, SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [draft, q, setSearchParams])

  // A `purpose-correction:` value naming a row this page does not hold, or
  // a malformed one: strip it once the page has loaded rather than leave an
  // inert param sitting in the URL. clear(), never close() - a dead link is
  // not a navigation to undo, the same reasoning Locations.tsx documents. A
  // `foreign` value belongs to nothing on this screen and is left alone.
  useEffect(() => {
    if (correctionTarget.kind === 'foreign' || correctionTarget.kind === 'new') return
    if (state.status !== 'success' || !state.data) return
    const rows = more ? [...state.data.page.transactions, ...more.transactions] : state.data.page.transactions
    if (correctionTarget.kind === 'edit' && rows.some((t) => t.id === correctionTarget.id)) return
    clearDialog()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dialogValue, correctionTarget.kind, state.status, more])

  /** Clears the purpose filter and keeps whatever she has searched. Not
   * `replace`: arriving here from an envelope was a navigation, so the back
   * button should still lead back to that envelope. */
  function clearPurposeFilter() {
    setSearchParams((current) => {
      const params = new URLSearchParams(current)
      params.delete('purpose')
      return params
    })
  }

  async function loadMore(cursor: string) {
    const startedIn = generation.current
    setMoreLoading(true)
    setMoreError(null)
    try {
      const page = await listTransactions({ q: q || undefined, purposeId: purposeId ?? undefined, cursor })
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

    // The row the dialog is about, found among the rows actually loaded. A
    // `purpose-correction:` value naming a row on a page she has not loaded
    // (a deep link, a stale link) therefore opens nothing - and the effect
    // below strips it rather than leaving an inert param behind.
    const correcting = transactions.find((t) => correctionTarget.kind === 'edit' && t.id === correctionTarget.id) ?? null

    // An empty filtered list is not a failed search: it says the envelope
    // has no rows, not that nothing matched something she typed. A search
    // inside the filter is still a search, so `q` wins when both are set.
    const emptyMessage = q ? text.noResults(q) : purposeId !== null ? text.purposeFilterEmpty : copy.home.recentActivityEmpty

    return (
      <>
        {/* The filter names itself and can always be cleared - the name
            comes off the balances this screen already fetches, so a filtered
            list never costs an extra request to label. */}
        {purposeId !== null && (
          <div className="flex items-center gap-2 self-start rounded-full bg-muted py-1 pr-1 pl-3 text-sm">
            <span className="font-medium">{text.purposeFilterLabel(purposeNames.get(purposeId) ?? '')}</span>
            <button
              type="button"
              aria-label={text.purposeFilterClear}
              onClick={clearPurposeFilter}
              className="grid size-7 place-items-center rounded-full text-muted-foreground hover:bg-foreground/10 hover:text-foreground"
            >
              <X aria-hidden="true" className="size-4" />
            </button>
          </div>
        )}
        {corrected && (
          <p role="status" className="rounded-lg bg-success-soft px-3 py-2 text-sm text-success">
            {copy.purposeCorrection.success}
          </p>
        )}
        <TransactionList
          transactions={transactions}
          purposeNames={purposeNames}
          emptyMessage={emptyMessage}
          onCorrectPurpose={(transaction) => {
            setCorrected(false)
            openDialog(`purpose-correction:${transaction.id}`)
          }}
        />
        <CorrectPurposeDialog
          transaction={correcting}
          purposes={state.data.purposes}
          purposeNames={purposeNames}
          open={correcting !== null}
          onClose={closeDialog}
          onCorrected={() => {
            closeDialog()
            setCorrected(true)
            void run(loadFirstPage)
          }}
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
