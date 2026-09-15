import { ArrowDownLeft, ArrowUpRight, Search } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { formatIsoDate, formatPeriod } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { listDuesPayments } from '@/lib/dues'
import { useApi } from '@/lib/useApi'
import type { DuesPaymentHistoryRow, DuesPaymentsPage } from '@/lib/dues'

const text = copy.history.dues
// reversalRow/reversedBadge already name these two facts on the
// period-scoped panel (MemberPayments.tsx) - reused verbatim here rather
// than restated, since the same two words mean the same thing in both
// places.
const rowText = copy.dues.history

/** Long enough that a word typed at normal speed is one request, short
 * enough that the list follows her without a visible pause - same value
 * every other paged, searched list in this app uses. */
const SEARCH_DEBOUNCE_MS = 300

/**
 * The Iuran tab's payment history (#228, PRD section 7.3): every posted dues
 * payment and every reversal (ADR-029), newest-first, 25 a page, searchable
 * by member name - what actually happened, as opposed to the status matrix
 * above it, which only ever answers "who owes what for one period". A
 * reversal is never edited away: both halves of the pair stay listed, and a
 * reversal names the original payment's own date so the link reads even
 * when that payment has fallen off an earlier page.
 *
 * Deliberately its own fetch, independent of Status.tsx's period selector:
 * the period drives the matrix above, never this list (the issue's own
 * explicit rule) - so this component takes no period prop at all, only
 * `refetchKey`, the same "a write happened elsewhere, reload" signal every
 * other tab already reads (Status.tsx's own doc comment).
 *
 * Search is local state, not the URL: unlike History/Transactions.tsx and
 * History/Reimbursements.tsx, this section is not itself a routed tab (it
 * lives inside the Iuran tab, under the matrix), so there is no deep link
 * into a search here for the URL to carry.
 */
export default function PaymentHistory({ refetchKey, reversalCount }: { refetchKey?: unknown; reversalCount?: number }) {
  const [draft, setDraft] = useState('')
  const [q, setQ] = useState('')

  const [state, run] = useApi<DuesPaymentsPage>()
  const [more, setMore] = useState<DuesPaymentsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  async function loadFirstPage(): Promise<DuesPaymentsPage> {
    return listDuesPayments({ q: q || undefined })
  }

  useEffect(() => {
    generation.current += 1
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void run(loadFirstPage)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, q, refetchKey, reversalCount])

  useEffect(() => {
    const next = draft.trim()
    if (next === q) return
    const timer = window.setTimeout(() => setQ(next), SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [draft, q])

  async function loadMore(cursor: string) {
    const startedIn = generation.current
    setMoreLoading(true)
    setMoreError(null)
    try {
      const page = await listDuesPayments({ q: q || undefined, cursor })
      if (startedIn !== generation.current) return
      setMore((prev) => ({
        duesPayments: [...(prev?.duesPayments ?? []), ...page.duesPayments],
        nextCursor: page.nextCursor,
      }))
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

    const page = state.data
    const rows = more ? [...page.duesPayments, ...more.duesPayments] : page.duesPayments
    const nextCursor = more ? more.nextCursor : page.nextCursor

    if (rows.length === 0) {
      return <p className="text-muted-foreground">{q ? text.noResults(q) : text.empty}</p>
    }

    return (
      <>
        <ul className="flex flex-col gap-2">
          {rows.map((row) => (
            <PaymentHistoryRow key={row.id} row={row} />
          ))}
        </ul>
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
      <h2 className="text-lg font-semibold">{text.heading}</h2>

      <div className="relative">
        <Label htmlFor="dues-payment-search" className="sr-only">
          {text.searchLabel}
        </Label>
        <Search aria-hidden="true" className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          id="dues-payment-search"
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

function PaymentHistoryRow({ row }: { row: DuesPaymentHistoryRow }) {
  const reversed = row.reversed_by_transaction_id !== null

  return (
    <li className="flex items-start justify-between gap-3 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
      <span className="flex min-w-0 items-start gap-2">
        {row.is_reversal ? (
          <ArrowUpRight aria-hidden="true" className="mt-0.5 shrink-0 text-attention" />
        ) : (
          <ArrowDownLeft aria-hidden="true" className="mt-0.5 shrink-0 text-success" />
        )}
        <span className="flex min-w-0 flex-col">
          <span className="truncate">
            {row.member_name}
            {row.is_reversal && ` \u00b7 ${rowText.reversalRow}`}
          </span>
          <span className="text-sm text-muted-foreground">{formatPeriod(row.dues_period)}</span>
          <span className="text-sm text-muted-foreground">{formatIsoDate(row.occurred_on)}</span>
          {row.is_reversal && row.reverses_occurred_on && (
            <span className="text-sm text-muted-foreground">{text.reversesLabel(formatIsoDate(row.reverses_occurred_on))}</span>
          )}
          {row.note && <span className="truncate text-sm text-muted-foreground">{row.note}</span>}
          {!row.is_reversal && reversed && <span className="text-sm text-muted-foreground">{rowText.reversedBadge}</span>}
        </span>
      </span>
      <span className="tabular shrink-0 font-medium">{formatIDR(row.amount)}</span>
    </li>
  )
}
