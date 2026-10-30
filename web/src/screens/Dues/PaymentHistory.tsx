import { ArrowDownLeft, ArrowUpRight, Camera, Search } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import DuesEntryDialogs from '@/screens/Dues/DuesEntryDialogs'
import { useDialogParam } from '@/lib/useDialogParam'

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
// reversalRow/reversedBadge name these two facts on the period-scoped panel
// (MemberPayments.tsx) - reused verbatim here, since the same two words mean
// the same thing in both places.
const rowText = copy.dues.history

/** Long enough that a word typed at normal speed is one request, short enough
 * that the list follows her without a visible pause - the value every paged,
 * searched list in this app uses. */
const SEARCH_DEBOUNCE_MS = 300

/**
 * The Iuran tab's payment history (PRD section 7.3): every posted dues payment
 * and every reversal (ADR-029), newest-first, 25 a page, searchable by member
 * name - what happened, as opposed to the status matrix, which answers "who
 * owes what for one period". A reversal is never edited away: both halves of
 * the pair stay listed, and a reversal names the original payment's date so
 * the link reads even when that payment has fallen off an earlier page.
 *
 * This is the whole Iuran tab. The status matrix is its own screen at /dues
 * (Status.tsx), opened by the row at the top (onOpenStatus) and never stacked
 * on this list (ADR-032: tabs hold lists). The period drives only that screen
 * - this component takes no period prop, only `refetchKey`, the "a write
 * happened elsewhere, reload" signal every tab reads.
 *
 * Search (?q=) lives in the URL, as on the other Riwayat tabs (ADR-032): back
 * and reload land on the same search.
 */
export default function PaymentHistory({ onOpenStatus, refetchKey }: { onOpenStatus: () => void; refetchKey?: unknown }) {
  const [searchParams, setSearchParams] = useSearchParams()
  const q = (searchParams.get('q') ?? '').trim()
  const [draft, setDraft] = useState(q)

  const [state, run] = useApi<DuesPaymentsPage>()
  const [more, setMore] = useState<DuesPaymentsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  const entry = useDialogParam('entry')

  const loadFirstPage = useCallback((): Promise<DuesPaymentsPage> => listDuesPayments({ q: q || undefined }), [q])

  // After a reversal or a nota: re-read the first page in place, so the card
  // and the dialog over it do not drop to Loading.
  const refreshFirstPage = useCallback(() => {
    generation.current += 1
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void run(loadFirstPage, { silent: true })
  }, [run, loadFirstPage])

  useEffect(() => {
    generation.current += 1
    // oxlint-disable-next-line react/set-state-in-effect -- reset the appended pages whenever the first page is refetched; the refetch is the external sync
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void run(loadFirstPage)
  }, [run, loadFirstPage, refetchKey])

  // The URL changed under the field (back, forward, a link): follow it. A draft
  // that already trims to the same search is left alone, so a trailing space
  // she is still typing is not snatched away.
  useEffect(() => {
    // oxlint-disable-next-line react/set-state-in-effect -- follow the URL back into the search field (back, forward, a link); the URL is the source of truth
    setDraft((current) => (current.trim() === q ? current : q))
  }, [q])

  useEffect(() => {
    const next = draft.trim()
    if (next === q) return
    const timer = window.setTimeout(() => {
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
            <PaymentHistoryRow key={row.id} row={row} onOpen={() => entry.open(String(row.id))} />
          ))}
        </ul>
        <DuesEntryDialogs rows={rows} onRefresh={refreshFirstPage} />
        {nextCursor && moreError && <ErrorState error={moreError} onRetry={() => void loadMore(nextCursor)} />}
        {nextCursor && !moreError && (
          <Button
            type="button"
            variant="outline"
            size="lg"
            className="w-full"
            disabled={moreLoading}
            onClick={() => void loadMore(nextCursor)}
          >
            {moreLoading ? copy.common.loading : text.loadMore}
          </Button>
        )}
      </>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {/* The way into the status matrix, which is its own screen (#228):
          one month's reading, one row away, never stacked on this list. */}
      <Button type="button" variant="outline" size="lg" className="w-full" onClick={onOpenStatus}>
        {copy.dues.entryLink}
      </Button>

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

function PaymentHistoryRow({ row, onOpen }: { row: DuesPaymentHistoryRow; onOpen: () => void }) {
  const reversed = row.reversed_by_transaction_id !== null
  const receiptCount = row.receipt_ids.length

  return (
    <li className="relative flex flex-col gap-2 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10">
      {/* The whole card is one tap target that opens the entry in full (#768),
          as on Transaksi: a full-card button underneath the content, which
          ignores pointer events so a tap anywhere falls through to it. Every
          action (Batalkan, the nota) lives in the detail. */}
      <button
        type="button"
        aria-label={
          copy.transactionDetail.openAria(
            `${row.member_name}, ${formatPeriod(row.dues_period)}`,
            formatIDR(row.amount),
            formatIsoDate(row.occurred_on),
          ) + (receiptCount > 0 ? `, ${copy.transactionDetail.receiptCountAria(receiptCount)}` : '')
        }
        onClick={onOpen}
        className="absolute inset-0 rounded-lg transition-colors outline-none hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50"
      />
      <div className="pointer-events-none relative flex items-start justify-between gap-3">
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
        {/* As a Transaksi card: the amount alone on its line, the camera and
            count under it once a nota exists (read-only; the count rides in
            the button's name). */}
        <span className="flex shrink-0 flex-col items-end gap-1.5">
          <span className="tabular font-medium">{formatIDR(row.amount)}</span>
          {receiptCount > 0 && (
            <span aria-hidden="true" className="flex items-center gap-1 text-primary">
              <Camera className="size-4" />
              <span className="tabular text-sm font-medium">{receiptCount}</span>
            </span>
          )}
        </span>
      </div>
    </li>
  )
}
