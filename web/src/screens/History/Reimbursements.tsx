import { MoreHorizontal, Search } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'

import ReceiptDialog from '@/components/ReceiptDialog'
import ReceiptRowButton from '@/components/ReceiptRowButton'
import { segmentedItemClass, segmentedTrackClass } from '@/components/segmented'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { cn } from '@/lib/utils'
import { listPurposes } from '@/lib/purposes'
import { listReimbursements, updateReimbursement, deleteReimbursement } from '@/lib/reimbursements'
import { useApi } from '@/lib/useApi'
import type { Member } from '@/lib/setup'
import type { Purpose } from '@/lib/purposes'
import type { Reimbursement, ReimbursementsPage } from '@/lib/reimbursements'
import { listAllMembers } from '@/lib/setup'
import { doneText, errorText, todayISODate, type TalanganState } from '@/screens/Reimbursements/shared'

const text = copy.reimbursements
const searchText = copy.history.reimbursements

/** Long enough that a word typed at normal speed is one request, short
 * enough that the list follows her without a visible pause - same value as
 * History/Transactions.tsx's own debounce. */
const SEARCH_DEBOUNCE_MS = 300

/**
 * One speaking-string feedback for a finished action: a success, or a
 * failure with a reason. Stale either way is worse than none, so every
 * action starts by clearing it.
 */
type Feedback = { kind: 'success' | 'error'; text: string }

interface FormData {
  members: Member[]
  purposes: Purpose[]
}

/**
 * Riwayat's Talangan tab (M6.18, moved under Riwayat and made paged and
 * searchable by #226, ADR-032): the list of claims a member fronted, with
 * waive ("putihkan"), un-waive and delete in place. The money forms - record
 * a claim, settle it, correct it - are screens of their own (#368, ADR-032:
 * a form is a route, never an inline expander); this tab only navigates to
 * them and shows the confirmation they hand back through router state.
 *
 * Two views: outstanding (default) vs all (`?show=all`). Both the view and
 * the search (?q=) live in the URL (ADR-032: list state is a route, never
 * component state), so back and reload land on the same list, the same
 * reasoning History/Transactions.tsx gives. Search covers member name and
 * note only (narrower than Transaksi's own surface).
 *
 * No heading, no back button and no body copy of its own - Riwayat's own
 * `<h1>` already names the page, the tab strip is the way back to Transaksi
 * or Iuran, and Transactions.tsx (the in-tab precedent) carries none of the
 * three either.
 *
 * `refetchKey` is App.tsx's `useRefetchKey()` (location.key, minus dialog entries), the same mechanism the other
 * two tabs already use to pick up a write just made elsewhere.
 */
export default function Reimbursements({ refetchKey }: { refetchKey?: unknown }) {
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()
  const location = useLocation()
  const q = (searchParams.get('q') ?? '').trim()
  const [draft, setDraft] = useState(q)

  // Anything but `all` reads as the default view, so a stale or hand-typed
  // value lands on the outstanding list rather than an error.
  const tab: 'outstanding' | 'all' = searchParams.get('show') === 'all' ? 'all' : 'outstanding'
  const [listState, listRun] = useApi<ReimbursementsPage>()
  // Pages after the first, appended in order - reset whenever the first
  // page is refetched (a tab switch, a search, a write, or refetchKey).
  // `generation` stops a slow "load more" from a stale tab/search from
  // appending its rows to the new one, the same guard
  // History/Transactions.tsx uses.
  const [more, setMore] = useState<ReimbursementsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  const [formDataState, formDataRun] = useApi<FormData>()
  const [submitState, submitRun] = useApi<unknown>()

  const [deleteId, setDeleteId] = useState<number | null>(null)
  // The claim id whose photo dialog is open (#154) - never a search param
  // (unlike CorrectPurposeDialog's own `?edit=`): this tab already owns `q`
  // and no other dialog here is a deep link, so component state is enough.
  const [receiptsForId, setReceiptsForId] = useState<number | null>(null)

  // The confirmation a record/settle/correct screen handed back: read once,
  // from the history entry that navigation created, so it never outlives it.
  const [feedback, setFeedback] = useState<Feedback | null>(() => {
    const done = (location.state as TalanganState | null)?.reimbursementDone
    return done ? { kind: 'success', text: doneText(done) } : null
  })

  async function loadFirstPage(): Promise<ReimbursementsPage> {
    return listReimbursements({ outstanding: tab === 'outstanding', q: q || undefined })
  }

  function refetchFirstPage() {
    generation.current += 1
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void listRun(loadFirstPage)
  }

  useEffect(() => {
    refetchFirstPage()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, q, refetchKey])

  // The URL changed under the field (back, forward, a link): follow it. A
  // draft that already trims to the same search is left alone, so a
  // trailing space she is still typing is not snatched away.
  useEffect(() => {
    setDraft((current) => (current.trim() === q ? current : q))
  }, [q])

  useEffect(() => {
    const next = draft.trim()
    if (next === q) return
    const timer = window.setTimeout(() => {
      // Rewrites `q` alone, keeping `show` - setSearchParams replaces the
      // whole query otherwise.
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

  // A pushed entry, not a replace: switching view is a navigation, so back
  // returns to the view she came from.
  function showView(next: 'outstanding' | 'all') {
    if (next === tab) return
    setSearchParams((current) => {
      const params = new URLSearchParams(current)
      if (next === 'all') params.set('show', 'all')
      else params.delete('show')
      return params
    })
  }

  function chooseTab(next: 'outstanding' | 'all') {
    showView(next)
    setDeleteId(null)
    setFeedback(null)
  }

  async function loadMore(cursor: string) {
    const startedIn = generation.current
    setMoreLoading(true)
    setMoreError(null)
    try {
      const page = await listReimbursements({ outstanding: tab === 'outstanding', q: q || undefined, cursor })
      if (startedIn !== generation.current) return
      setMore((prev) => ({
        reimbursements: [...(prev?.reimbursements ?? []), ...page.reimbursements],
        nextCursor: page.nextCursor,
      }))
    } catch (err) {
      if (startedIn !== generation.current) return
      setMoreError(err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err)))
    } finally {
      if (startedIn === generation.current) setMoreLoading(false)
    }
  }

  function fetchFormData() {
    return formDataRun(async () => {
      const [members, purposes] = await Promise.all([listAllMembers(), listPurposes()])
      return { members, purposes }
    })
  }

  useEffect(() => {
    void fetchFormData()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formDataRun])

  // Close the delete confirmation when a successful action's list refresh
  // lands; an error keeps it open so she can read why before deciding again.
  useEffect(() => {
    if (listState.status === 'success' && feedback?.kind === 'success') {
      setDeleteId(null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [listState.status])

  const submitting = submitState.status === 'loading'

  /**
   * Every write on this tab - waive, un-waive, delete - follows the same shape: clear any stale feedback, run the call,
   * then say what happened. A named 409 (already settled, already waived)
   * reaches the treasurer through copy.reimbursements.errors; anything else
   * falls back to the shared map, never the English wire message (ADR-014).
   * The list reloads from the first page after both outcomes: on success it
   * shows the new state, on a 409 the claim row unmounts with the message
   * explaining why.
   */
  function runWrite(api: () => Promise<unknown>, successText: string) {
    void submitRun(async () => {
      setFeedback(null)
      try {
        await api()
        setFeedback({ kind: 'success', text: successText })
      } catch (err) {
        const apiErr = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
        setFeedback({ kind: 'error', text: errorText(apiErr) })
        // eslint-disable-next-line no-console
        console.error('API error', apiErr.code, apiErr.message)
      }
      refetchFirstPage()
    })
  }

  function handleWaive(id: number) {
    runWrite(() => updateReimbursement(id, { waived_on: todayISODate() }), text.waive.success)
  }

  function handleUnwaive(id: number) {
    runWrite(() => updateReimbursement(id, { waived_on: null }), text.unwaive.success)
  }

  function handleDelete(id: number) {
    runWrite(() => deleteReimbursement(id), text.delete.success)
  }

  function renderList() {
    if (listState.status === 'idle' || listState.status === 'loading') {
      return <Loading />
    }

    if (listState.status === 'error' || !listState.data) {
      return listState.error ? <ErrorState error={listState.error} onRetry={refetchFirstPage} /> : null
    }

    const page = listState.data
    const claims = more ? [...page.reimbursements, ...more.reimbursements] : page.reimbursements
    const nextCursor = more ? more.nextCursor : page.nextCursor

    const fd = formDataState.data
    const memberNames = fd ? new Map(fd.members.map((m) => [m.id, m.name])) : new Map()
    const purposeNames = fd ? new Map(fd.purposes.map((p) => [p.id, p.name])) : new Map()

    return (
      <>
        {claims.length === 0 ? (
          <p className="text-muted-foreground">
            {q ? searchText.noResults(q) : tab === 'outstanding' ? text.emptyOutstanding : text.emptyAll}
          </p>
        ) : (
          <ul className="flex flex-col gap-3">
            {claims.map((claim) => (
              <li key={claim.id} className="flex flex-col gap-2 rounded-2xl bg-card p-4 shadow-card ring-1 ring-foreground/10">
                <div className="flex items-start justify-between gap-3">
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate font-medium">{memberNames.get(claim.member_id) ?? '-'}</span>
                    <span className="truncate text-sm text-muted-foreground">{purposeNames.get(claim.purpose_id) ?? '-'}</span>
                  </span>
                  <span className="tabular shrink-0 font-medium">{formatIDR(claim.amount)}</span>
                </div>

                <div className="flex items-center justify-between text-sm text-muted-foreground">
                  <span>{formatIsoDate(claim.incurred_on)}</span>
                  <StatusBadge claim={claim} />
                </div>

                {claim.note && <p className="text-sm text-muted-foreground">{claim.note}</p>}

                {/* The photo affordance (#154) - the same shared control as
                    TransactionList.tsx's own row control: quiet "Tambah
                    foto nota" with no photo yet, a Forest camera with the
                    count once one exists. Shown on every tab, not only
                    outstanding - a settled claim's nota is exactly as
                    worth keeping. */}
                {/* One line of row controls, never a row of buttons (#314):
                    four full-size buttons made each claim read like a form.
                    The photo affordance (#154) on the left - the same shared
                    control as TransactionList.tsx's, shown on every tab,
                    since a settled claim's nota is exactly as worth keeping.
                    On the right, only on the outstanding tab: Bayar in plain
                    ink, the everyday action, one tap; the rarer three behind
                    a "more" menu, two. */}
                <div className="flex items-center justify-between gap-3">
                  <ReceiptRowButton receiptIds={claim.receipt_ids ?? []} onClick={() => setReceiptsForId(claim.id)} />
                  {tab === 'outstanding' && !claim.waived_on && (
                    <div className="flex items-center gap-5">
                      <button
                        type="button"
                        onClick={() => navigate(`/reimbursements/settle?id=${claim.id}`)}
                        className={rowControlClass('text-sm font-medium text-primary hover:text-primary/80')}
                      >
                        {text.actions.settle}
                      </button>
                      <DropdownMenu modal={false}>
                        <DropdownMenuTrigger asChild>
                          <button
                            type="button"
                            aria-label={text.actions.menuAria}
                            disabled={submitting}
                            className={rowControlClass('text-muted-foreground/70 hover:text-foreground disabled:opacity-50')}
                          >
                            <MoreHorizontal aria-hidden="true" className="size-5" />
                          </button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onSelect={() => navigate(`/reimbursements/correct?id=${claim.id}`)}>
                            {text.actions.correct}
                          </DropdownMenuItem>
                          <DropdownMenuItem onSelect={() => handleWaive(claim.id)}>{text.actions.waive}</DropdownMenuItem>
                          <DropdownMenuItem
                            variant="destructive"
                            onSelect={() => {
                              setDeleteId(claim.id)
                              setFeedback(null)
                            }}
                          >
                            {text.actions.delete}
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  )}
                  {/* Un-waive for a waived claim - reachable on the "all" tab
                      only: the outstanding list filters waived claims out, so
                      this branch exists precisely where the claim can appear.
                      The same plain ink as Bayar, on the same line. */}
                  {claim.waived_on && (
                    <button
                      type="button"
                      onClick={() => handleUnwaive(claim.id)}
                      disabled={submitting}
                      className={rowControlClass('text-sm font-medium text-primary hover:text-primary/80 disabled:opacity-50')}
                    >
                      {text.actions.unwaive}
                    </button>
                  )}
                </div>

                {/* Delete confirmation */}
                {deleteId === claim.id && (
                  <div className="flex flex-col gap-2 rounded-lg bg-attention-soft p-3">
                    <p className="text-sm">{text.actions.delete}?</p>
                    <div className="grid grid-cols-2 gap-2">
                      <Button type="button" size="lg" variant="outline" onClick={() => setDeleteId(null)} disabled={submitting}>
                        {text.settle.cancel}
                      </Button>
                      <Button type="button" size="lg" variant="destructive" onClick={() => handleDelete(claim.id)} disabled={submitting}>
                        {submitting ? text.actions.deleting : text.actions.delete}
                      </Button>
                    </div>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
        <ReceiptDialog
          kind="reimbursements"
          parentId={receiptsForId}
          receiptIds={claims.find((c) => c.id === receiptsForId)?.receipt_ids ?? []}
          open={receiptsForId !== null}
          onClose={() => setReceiptsForId(null)}
          onChanged={refetchFirstPage}
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
            {moreLoading ? copy.common.loading : searchText.loadMore}
          </Button>
        )}
      </>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {feedback && (
        <p
          role={feedback.kind === 'error' ? 'alert' : 'status'}
          className={
            feedback.kind === 'error'
              ? 'rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention'
              : 'rounded-lg bg-success-soft px-3 py-2 text-sm text-success'
          }
        >
          {feedback.text}
        </p>
      )}

      {/* View toggle - a segmented control, not tabs: the Riwayat tab strip
          above is the only tablist on this screen. */}
      <div role="group" aria-label={text.heading} className={segmentedTrackClass(2)}>
        <Button
          type="button"
          variant={tab === 'outstanding' ? 'default' : 'ghost'}
          aria-pressed={tab === 'outstanding'}
          className={segmentedItemClass(tab === 'outstanding')}
          onClick={() => chooseTab('outstanding')}
        >
          {text.outstandingTab}
        </Button>
        <Button
          type="button"
          variant={tab === 'all' ? 'default' : 'ghost'}
          aria-pressed={tab === 'all'}
          className={segmentedItemClass(tab === 'all')}
          onClick={() => chooseTab('all')}
        >
          {text.allTab}
        </Button>
      </div>

      <div className="relative">
        <Label htmlFor="reimbursement-search" className="sr-only">
          {searchText.searchLabel}
        </Label>
        <Search aria-hidden="true" className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          id="reimbursement-search"
          type="search"
          autoComplete="off"
          placeholder={searchText.searchPlaceholder}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          className="pl-9"
        />
      </div>

      <Button type="button" size="lg" onClick={() => navigate('/reimbursements/new')}>
        {text.record.heading}
      </Button>

      {renderList()}
    </div>
  )
}

function StatusBadge({ claim }: { claim: Reimbursement }) {
  if (claim.waived_on) {
    return <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{text.status.waived}</span>
  }
  // `settled` travels on the wire: the list queries compute whether a
  // kind='reimbursement' payout references the claim, so this badge is
  // honest on both tabs - an outstanding list only ever says "Belum
  // dibayar", and the all list says "Dibayar" only for a row a payout
  // actually settled.
  if (claim.settled) {
    return <span className="rounded-full bg-success-soft px-2 py-0.5 text-xs font-medium text-success">{text.status.settled}</span>
  }
  return <span className="rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{text.status.outstanding}</span>
}

/** A row control's box (#314): sized to its content so the row stays one
 * quiet line, with an invisible 44px hit area centred on it - the same
 * technique ReceiptRowButton uses - so the target still meets
 * Design-System.md's minimum. */
function rowControlClass(className: string): string {
  return cn(
    "relative flex shrink-0 items-center transition-colors after:absolute after:top-1/2 after:left-1/2 after:size-11 after:-translate-x-1/2 after:-translate-y-1/2 after:content-['']",
    className,
  )
}
