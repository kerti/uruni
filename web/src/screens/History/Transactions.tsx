import { Funnel, MagnifyingGlass, X } from '@phosphor-icons/react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import ErrorState from '@/components/states/ErrorState'
import Loading from '@/components/states/Loading'
import TransactionList from '@/components/TransactionList'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { formatIsoDate, formatPeriod } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { listPurposes } from '@/lib/purposes'
import { listAllMembers } from '@/lib/setup'
import { activeFilterCount, readFilters, writeFilters, type TransactionFilters } from '@/lib/transactionFilters'
import { getTransactionSummary, listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import TransactionFilterDialog from '@/screens/History/TransactionFilterDialog'
import type { Purpose } from '@/lib/purposes'
import type { Member } from '@/lib/setup'
import type { TransactionSummary, TransactionsPage } from '@/lib/transactions'

const text = copy.history.transactions

/** Long enough that a word typed at normal speed is one request, short enough
 * that the list follows her without a visible pause. */
const SEARCH_DEBOUNCE_MS = 300

/** What the list is read against, fetched once per visit: a search or a
 * filter change asks for the page alone. */
interface Lookups {
  /** Every purpose, a closed envelope's too: the rows' pos names, the filter
   * dialog and its chip. */
  purposes: Purpose[]
  /** Every member, an inactive one too, for the filter dialog. */
  members: Member[]
}

interface FirstPage {
  page: TransactionsPage
  lookups: Lookups
  /** The month's walk (#751), read only under a month filter and no search:
   * a summary of a month says nothing about the rows a search kept. */
  summary: TransactionSummary | null
}

/** The list request for the URL's search and filters; `cursor` asks for the page after it. */
function pageInput(
  q: string,
  { purposeId, memberId, month, direction }: Pick<TransactionFilters, 'purposeId' | 'memberId' | 'month' | 'direction'>,
  cursor?: string,
) {
  return {
    q: q || undefined,
    purposeId: purposeId ?? undefined,
    memberId: memberId ?? undefined,
    month: month ?? undefined,
    direction: direction ?? undefined,
    cursor,
  }
}

/**
 * Riwayat's Transaksi tab (paged and searchable, ADR-032 "Lists: paging and
 * search"): newest-first, 25 a page, "muat lebih banyak" rather than infinite
 * scroll.
 *
 * The search lives in the URL (`?q=`), for the reason the tab is a route: back
 * and reload land on the same view. The field is local state only while she
 * types; after SEARCH_DEBOUNCE_MS it is written to the URL with `replace` -
 * one history entry for the search, not one per keystroke - and the URL
 * drives the fetch.
 *
 * Search always goes to the server - filtering the rows already loaded would
 * answer for 25 rows while looking like an answer for all of them. A search
 * that cannot reach the server shows ErrorState's connection copy, never the
 * no-results line.
 *
 * The filters are the public report's four under its params - `?month=`,
 * `?purpose=`, `?member=`, `?dir=` - picked in TransactionFilterDialog
 * (`?edit=filter`) and shown as chips she can clear one at a time.
 * `?purpose=` also arrives as a link: it is how ADR-032's only route to a
 * CLOSED envelope's record works. They live in the URL like `q`, compose with
 * it, go to the server, and reset the paging.
 *
 * `refetchKey` is App.tsx's `useRefetchKey()` (location.key, minus dialog
 * entries), the mechanism Home uses to pick up a transaction recorded
 * elsewhere.
 */
export default function Transactions({ refetchKey }: { refetchKey?: unknown }) {
  const [searchParams, setSearchParams] = useSearchParams()
  const q = (searchParams.get('q') ?? '').trim()
  const [draft, setDraft] = useState(q)

  const filters = useMemo(() => readFilters(searchParams), [searchParams])
  const { month, purposeId, memberId, direction } = filters
  const filterCount = activeFilterCount(filters)

  // The filter dialog is a search parameter, never component state (ADR-032).
  // The correction dialog shares `?edit=` and lives in TransactionList.
  const { value: dialogValue, open: openDialog, close: closeDialog } = useDialogParam()
  const filterOpen = dialogValue === 'filter'

  const [state, run] = useApi<FirstPage>()
  // Pages after the first, appended in order. Reset whenever the first page is
  // refetched, and `generation` stops a slow "load more" for the previous
  // search from appending its rows to the new one.
  const [more, setMore] = useState<TransactionsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  // The lookups, requested once and shared by every first-page load after
  // it, so a search costs one request rather than four. A failed request is
  // forgotten, so the retry asks again. `options` is the same data for the
  // filter dialog, which stays open across loads and must not blank while the
  // page refetches.
  const lookups = useRef<Promise<Lookups> | null>(null)
  const [options, setOptions] = useState<Lookups>({ purposes: [], members: [] })
  const loadLookups = useCallback(() => {
    if (lookups.current === null) {
      const pending = Promise.all([listPurposes(), listAllMembers()]).then(([purposes, members]) => {
        const loaded = { purposes, members }
        setOptions(loaded)
        return loaded
      })
      pending.catch(() => {
        if (lookups.current === pending) lookups.current = null
      })
      lookups.current = pending
    }
    return lookups.current
  }, [])

  const loadFirstPage = useCallback(async (): Promise<FirstPage> => {
    const input = pageInput(q, { purposeId, memberId, month, direction })
    const summarised = month !== null && !q
    const [page, loaded, summary] = await Promise.all([
      listTransactions(input),
      loadLookups(),
      summarised
        ? getTransactionSummary({
            month,
            purposeId: purposeId ?? undefined,
            memberId: memberId ?? undefined,
            direction: direction ?? undefined,
          })
        : Promise.resolve(null),
    ])
    return { page, lookups: loaded, summary }
  }, [q, purposeId, memberId, month, direction, loadLookups])

  useEffect(() => {
    generation.current += 1
    // oxlint-disable-next-line react/set-state-in-effect -- reset the appended pages whenever the first page is refetched; the refetch is the external sync
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void run(loadFirstPage)
  }, [run, loadFirstPage, refetchKey])

  // The URL changed under the field (back, forward, a link): follow it. A
  // draft that already trims to the same search is left alone, so a trailing
  // space she is still typing is not snatched away.
  useEffect(() => {
    // oxlint-disable-next-line react/set-state-in-effect -- follow the URL back into the search field (back, forward, a link); the URL is the source of truth
    setDraft((current) => (current.trim() === q ? current : q))
  }, [q])

  useEffect(() => {
    const next = draft.trim()
    if (next === q) return
    const timer = window.setTimeout(() => {
      // Rewrites `q` alone - `setSearchParams` replaces the whole query, so
      // building off the current params keeps the filters alive through a
      // search typed inside them.
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

  /** Clears one filter and keeps the rest and whatever she searched. Not
   * `replace`: arriving from an envelope was a navigation, so back should
   * still lead to that envelope. */
  function clearFilter(change: Partial<TransactionFilters>) {
    setSearchParams((current) => writeFilters(current, { ...readFilters(current), ...change }))
  }

  /** Applies the dialog's filters in place of the dialog's history entry, so
   * back from the filtered list returns to the list as it was before the
   * dialog opened. */
  function applyFilters(next: TransactionFilters) {
    setSearchParams(
      (current) => {
        const params = writeFilters(current, next)
        params.delete('edit')
        return params
      },
      { replace: true },
    )
  }

  async function loadMore(cursor: string) {
    const startedIn = generation.current
    setMoreLoading(true)
    setMoreError(null)
    try {
      const page = await listTransactions(pageInput(q, { purposeId, memberId, month, direction }, cursor))
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

    const { page, lookups: loaded, summary } = state.data
    const purposeNames = new Map(loaded.purposes.map((purpose) => [purpose.id, purpose.name]))
    const transactions = more ? [...page.transactions, ...more.transactions] : page.transactions
    const nextCursor = more ? more.nextCursor : page.nextCursor

    // An empty filtered list is not a failed search: nothing matched the
    // filters, not something she typed. A search inside the filters is still a
    // search, so `q` wins when both are set. A pos alone is an envelope's
    // record, which simply has no rows yet.
    const emptyMessage = q
      ? text.noResults(q)
      : filterCount === 1 && purposeId !== null
        ? text.purposeFilterEmpty
        : filterCount > 0
          ? text.filterEmpty
          : copy.home.recentActivityEmpty

    const purposeName = (id: number) => purposeNames.get(id) ?? ''
    const chips: { label: string; clear: Partial<TransactionFilters> }[] = []
    if (month !== null) chips.push({ label: text.monthFilterLabel(formatPeriod(month)), clear: { month: null } })
    if (purposeId !== null) chips.push({ label: text.purposeFilterLabel(purposeName(purposeId)), clear: { purposeId: null } })
    if (memberId !== null) {
      const name = loaded.members.find((m) => m.id === memberId)?.name ?? ''
      chips.push({ label: text.memberFilterLabel(name), clear: { memberId: null } })
    }
    if (direction !== null) {
      chips.push({ label: direction === 'in' ? copy.record.directionIn : copy.record.directionOut, clear: { direction: null } })
    }

    return (
      <>
        {/* Each filter names itself and can always be cleared on its own,
            so she is never stuck inside a filtered list wondering where the
            rest went. */}
        {chips.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {chips.map((chip) => (
              <div key={chip.label} className="flex items-center gap-2 rounded-full bg-muted py-1 pr-1 pl-3 text-sm">
                <span className="font-medium">{chip.label}</span>
                <button
                  type="button"
                  aria-label={text.filterClear(chip.label)}
                  onClick={() => clearFilter(chip.clear)}
                  className="grid size-7 place-items-center rounded-full text-muted-foreground hover:bg-foreground/10 hover:text-foreground"
                >
                  <X aria-hidden="true" className="size-4" />
                </button>
              </div>
            ))}
          </div>
        )}
        {summary && <MonthSummary summary={summary} />}
        <TransactionList
          transactions={transactions}
          purposeNames={purposeNames}
          emptyMessage={emptyMessage}
          // One month is already its own heading; across months, each gets one.
          monthHeaders={month === null}
          onRefresh={() => run(loadFirstPage, { silent: true })}
        />
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
      <div className="flex gap-2">
        <div className="relative flex-1">
          <Label htmlFor="transaction-search" className="sr-only">
            {text.searchLabel}
          </Label>
          <MagnifyingGlass
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          />
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
        <Button
          type="button"
          variant={filterCount > 0 ? 'default' : 'outline'}
          size="lg"
          aria-label={filterCount > 0 ? text.filterButtonActive(filterCount) : text.filterButton}
          onClick={() => openDialog('filter')}
          className="relative shrink-0 px-3"
        >
          <Funnel aria-hidden="true" className="size-4" />
          {filterCount > 0 && <span className="tabular">{filterCount}</span>}
        </Button>
      </div>
      {renderList()}
      <TransactionFilterDialog
        open={filterOpen}
        filters={filters}
        purposes={options.purposes}
        members={options.members}
        onApply={applyFilters}
        onClose={closeDialog}
      />
    </div>
  )
}

/** The month as the public report reads it (#751, ADR-038): the balance it
 * started on, what came in and went out, and the balance it ended on, every
 * figure the server's. Saldo awal, Penyesuaian and Dipindah only when not
 * zero, as on the report. Under a member or direction filter a balance means
 * nothing, so only the two totals show. */
function MonthSummary({ summary }: { summary: TransactionSummary }) {
  const lines: { label: string; amount: number; last?: boolean }[] = []
  if (summary.full && summary.start_on) lines.push({ label: text.summaryBalance(formatIsoDate(summary.start_on)), amount: summary.start })
  if (summary.full && summary.openings !== 0) lines.push({ label: text.summaryOpenings, amount: summary.openings })
  lines.push({ label: text.summaryIn, amount: summary.in })
  lines.push({ label: text.summaryOut, amount: summary.out })
  if (summary.full && summary.adjustments !== 0) lines.push({ label: text.summaryAdjustments, amount: summary.adjustments })
  if (summary.full && summary.moved !== 0) lines.push({ label: text.summaryMoved, amount: summary.moved })
  if (summary.full && summary.end_on)
    lines.push({ label: text.summaryBalance(formatIsoDate(summary.end_on)), amount: summary.end, last: true })

  return (
    <dl className="flex flex-col gap-1.5 rounded-lg bg-card px-4 py-3 text-sm shadow-card ring-1 ring-foreground/10">
      {lines.map((line) => (
        <div
          key={line.label}
          className={`flex items-baseline justify-between gap-3 ${line.last ? 'mt-1 border-t border-border pt-2 font-semibold' : ''}`}
        >
          <dt className={line.last ? '' : 'text-muted-foreground'}>{line.label}</dt>
          <dd className="tabular font-medium">{formatIDR(line.amount)}</dd>
        </div>
      ))}
    </dl>
  )
}
