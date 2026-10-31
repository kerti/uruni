import { Camera, Search } from 'lucide-react'
import { startTransition, useCallback, useEffect, useOptimistic, useRef, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'

import ReceiptDialog from '@/components/ReceiptDialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useToast } from '@/components/toast/useToast'
import { useArrivalToast } from '@/components/toast/useArrivalToast'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError, settleUncertainWrite } from '@/lib/api'
import { formatIsoDate, todayISODate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { listPurposes } from '@/lib/purposes'
import { listReimbursements, updateReimbursement, deleteReimbursement } from '@/lib/reimbursements'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import { recordOriginFrom } from '@/lib/recordOrigin'
import type { Member } from '@/lib/setup'
import type { Purpose } from '@/lib/purposes'
import type { Reimbursement, ReimbursementsPage } from '@/lib/reimbursements'
import { listAllMembers } from '@/lib/setup'
import SettleDialog from '@/screens/Reimbursements/SettleDialog'
import ClaimSummary from '@/screens/Reimbursements/ClaimSummary'
import ClaimDetailDialog from '@/screens/Reimbursements/ClaimDetailDialog'
import { errorText, pickTalanganArrival } from '@/screens/Reimbursements/helpers'

const text = copy.reimbursements
const searchText = copy.history.reimbursements

/** Long enough that a word typed at normal speed is one request, short enough
 * that the list follows her without a visible pause - the value of
 * History/Transactions.tsx's debounce. */
const SEARCH_DEBOUNCE_MS = 300

/**
 * One speaking-string feedback for a failed action, with its reason. Stale is
 * worse than none, so every action starts by clearing it. A success is a toast.
 */
type Feedback = { kind: 'error'; text: string }

interface FormData {
  members: Member[]
  purposes: Purpose[]
}

/**
 * Riwayat's Talangan tab (paged and searchable, ADR-032): the list of claims
 * a member fronted. Each card is one tap target that opens the claim's detail
 * (`?entry=<id>`, #768), and every action - Bayar, Ubah, Putihkan, Batalkan
 * pemutihan, Hapus, the nota - lives there. Settle and delete are dialogs on
 * this tab the detail hands off to, addressed by
 * `?edit=settle:<id>` and `?edit=delete-claim:<id>` (ADR-046: a confirm on one
 * record, where the record fixes the amount). The forms where she types an
 * amount - record a claim, correct it - are screens of their own (ADR-032: a
 * form is a route, never an inline expander); this tab only navigates to them
 * and shows the confirmation the correct screen hands back through router
 * state.
 *
 * Two views: outstanding (default) vs all (`?show=all`). The view and the
 * search (?q=) live in the URL (ADR-032: list state is a route), so back and
 * reload land on the same list, as in History/Transactions.tsx. Search covers
 * member name and note only (narrower than Transaksi's).
 *
 * No heading, no back button and no body copy of its own - Riwayat's `<h1>`
 * names the page, the tab strip is the way back to Transaksi or Iuran, and
 * Transactions.tsx (the in-tab precedent) carries none of the three either.
 *
 * `refetchKey` is App.tsx's `useRefetchKey()` (location.key, minus dialog
 * entries), the mechanism the other two tabs use to pick up a write made
 * elsewhere.
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
  // What the filter checkbox shows. The router commits a URL change inside
  // startTransition, so a box bound to `tab` alone would snap back for a
  // render after the click; the optimistic value holds the click until the
  // new URL lands, then gives way to it.
  const [outstandingShown, showOutstanding] = useOptimistic(tab === 'outstanding')
  const [listState, listRun] = useApi<ReimbursementsPage>()
  // Pages after the first, appended in order - reset whenever the first page
  // is refetched (a tab switch, a search, a write, or refetchKey).
  // `generation` stops a slow "load more" from a stale tab/search from
  // appending its rows to the new one, as in History/Transactions.tsx.
  const [more, setMore] = useState<ReimbursementsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  const [formDataState, formDataRun] = useApi<FormData>()
  const [submitState, submitRun] = useApi<unknown>()

  // The two dialogs on this tab share the one `?edit=` param; each parses only
  // its own prefix, so neither clears the other's value (ADR-032).
  const dialog = useDialogParam()
  const settleTarget = parseDialogTarget('settle', dialog.value)
  const settleId = settleTarget.kind === 'edit' ? settleTarget.id : null
  const deleteTarget = parseDialogTarget('delete-claim', dialog.value)
  const deleteId = deleteTarget.kind === 'edit' ? deleteTarget.id : null
  // The claim id whose photo dialog is open - not a search param (unlike
  // CorrectPurposeDialog's `?edit=`): this tab owns `q` and no other dialog
  // here is a deep link, so component state is enough.
  const [receiptsForId, setReceiptsForId] = useState<number | null>(null)
  // The claim read in full, `?entry=<id>` (ADR-032: an open dialog is a search
  // param). A different param from `?edit=`, so it coexists with the settle and
  // delete dialogs the detail hands off to.
  const entry = useDialogParam('entry')
  const entryId = entry.value !== null && /^\d+$/.test(entry.value) ? Number(entry.value) : null
  // What the detail handed off to when it closed. Run from the dialog's own
  // onCloseAutoFocus, i.e. once it is gone, so two dialogs are never open at
  // once (ADR-032).
  const handoff = useRef<{ kind: 'settle' | 'delete' | 'correct' | 'receipts'; id: number } | null>(null)

  const [feedback, setFeedback] = useState<Feedback | null>(null)
  const toast = useToast()

  // The confirmation the correct screen handed back, toasted once.
  useArrivalToast(pickTalanganArrival)

  const loadFirstPage = useCallback(
    (): Promise<ReimbursementsPage> => listReimbursements({ outstanding: tab === 'outstanding', q: q || undefined }),
    [tab, q],
  )

  const refetchFirstPage = useCallback(() => {
    generation.current += 1
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void listRun(loadFirstPage)
  }, [listRun, loadFirstPage])

  useEffect(() => {
    // oxlint-disable-next-line react/set-state-in-effect -- a refetch on navigation resets the paging state it replaces
    refetchFirstPage()
  }, [refetchFirstPage, refetchKey])

  // A dialog value this tab owns but cannot show - malformed, or a delete
  // naming a claim the loaded list does not hold - is stripped rather than left
  // inert in the URL. clear(), never close(): a dead link is not a navigation
  // to undo. A value with another prefix is left where it is. (The settle
  // dialog checks its own claim: it reads it from the server, not this list.)
  const { clear: clearDialog } = dialog
  const listLoaded = listState.status === 'success' && listState.data !== undefined
  const deleteClaim =
    deleteId !== null && listState.data !== undefined
      ? ([...listState.data.reimbursements, ...(more?.reimbursements ?? [])].find((c) => c.id === deleteId) ?? null)
      : null
  const listHasDeleteClaim = deleteClaim !== null
  const entryClaim =
    entryId !== null && listState.data !== undefined
      ? ([...listState.data.reimbursements, ...(more?.reimbursements ?? [])].find((c) => c.id === entryId) ?? null)
      : null
  const listHasEntryClaim = entryClaim !== null
  // A malformed `?entry=`, or one naming a claim the loaded list does not
  // hold, is stripped (clear(), never close(): a dead link is not a navigation
  // to undo).
  const { clear: clearEntry } = entry
  const entryMalformed = entry.value !== null && entryId === null
  useEffect(() => {
    if (entryMalformed || (entryId !== null && listLoaded && !listHasEntryClaim)) clearEntry()
  }, [entryMalformed, entryId, listLoaded, listHasEntryClaim, clearEntry])
  useEffect(() => {
    if (settleTarget.kind === 'invalid' || deleteTarget.kind === 'invalid' || settleTarget.kind === 'new' || deleteTarget.kind === 'new') {
      clearDialog()
      return
    }
    if (deleteId !== null && listLoaded && !listHasDeleteClaim) clearDialog()
  }, [settleTarget.kind, deleteTarget.kind, deleteId, listLoaded, listHasDeleteClaim, clearDialog])

  // The URL changed under the field (back, forward, a link): follow it. A
  // draft that already trims to the same search is left alone, so a trailing
  // space she is still typing is not snatched away.
  useEffect(() => {
    // oxlint-disable-next-line react/set-state-in-effect -- the URL is the source of truth for the field; follow it when it moves
    setDraft((current) => (current.trim() === q ? current : q))
  }, [q])

  useEffect(() => {
    const next = draft.trim()
    if (next === q) return
    const timer = window.setTimeout(() => {
      // Rewrites `q` alone, keeping `show` - setSearchParams otherwise replaces
      // the whole query.
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

  const fetchFormData = useCallback(
    () =>
      formDataRun(async () => {
        const [members, purposes] = await Promise.all([listAllMembers(), listPurposes()])
        return { members, purposes }
      }),
    [formDataRun],
  )

  useEffect(() => {
    void fetchFormData()
  }, [fetchFormData])

  const submitting = submitState.status === 'loading'

  /**
   * Every in-place write on this tab - waive, un-waive - follows one shape:
   * clear stale feedback, run the call, say what happened. A named 409
   * (already settled, already waived) reaches the treasurer through
   * copy.reimbursements.errors; anything else falls back to the shared map,
   * never the English wire message (ADR-014). The list reloads from the first
   * page after both outcomes: on success it shows the new state, on a 409 the
   * claim row unmounts with the message explaining why.
   */
  function runWrite(api: () => Promise<unknown>, successText: string, onSuccess?: () => void) {
    void submitRun(async () => {
      setFeedback(null)
      try {
        await api()
        onSuccess?.()
        toast.success(successText)
      } catch (err) {
        // A lost response may still have saved; re-read before she can retry.
        await settleUncertainWrite(err, () => listRun(loadFirstPage, { silent: true }))
        const apiErr = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
        setFeedback({ kind: 'error', text: errorText(apiErr) })
        console.error('API error', apiErr.code, apiErr.message)
      }
      refetchFirstPage()
    })
  }

  function handleSettled() {
    dialog.close()
    toast.success(text.settle.success)
    refetchFirstPage()
  }

  function handleDeleted() {
    dialog.close()
    toast.success(text.delete.success)
    refetchFirstPage()
  }

  // Both run from the detail, which closes on success; a refusal stays in it.
  function handleWaive(id: number) {
    runWrite(() => updateReimbursement(id, { waived_on: todayISODate() }), text.waive.success, entry.close)
  }

  function handleUnwaive(id: number) {
    runWrite(() => updateReimbursement(id, { waived_on: null }), text.unwaive.success, entry.close)
  }

  function handOff(kind: 'settle' | 'delete' | 'correct' | 'receipts', claim: Reimbursement) {
    setFeedback(null)
    handoff.current = { kind, id: claim.id }
    entry.close()
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
          <ul className="flex flex-col gap-2">
            {claims.map((claim) => {
              const memberName = memberNames.get(claim.member_id) ?? '-'
              const purposeName = purposeNames.get(claim.purpose_id) ?? '-'
              const receiptCount = claim.receipt_ids?.length ?? 0
              return (
                <li
                  key={claim.id}
                  className="relative flex flex-col gap-2 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10"
                >
                  {/* One tap target (#768), the card shape Transaksi uses: a
                    full-card button underneath the content, which ignores
                    pointer events so a tap anywhere falls through to it.
                    Every action lives in the claim's detail. The receipt
                    count rides in the accessible name, since the glyph beside
                    the amount is hidden from screen readers. */}
                  <button
                    type="button"
                    aria-label={
                      text.detail.openAria(memberName, purposeName, formatIDR(claim.amount), formatIsoDate(claim.incurred_on)) +
                      (receiptCount > 0 ? `, ${copy.transactionDetail.receiptCountAria(receiptCount)}` : '')
                    }
                    onClick={() => entry.open(String(claim.id))}
                    className="absolute inset-0 rounded-lg transition-colors outline-none hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50"
                  />
                  <div className="pointer-events-none relative flex items-start justify-between gap-3">
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate">{memberName}</span>
                      <span className="truncate text-sm text-muted-foreground">{purposeName}</span>
                      <span className="text-sm text-muted-foreground">{formatIsoDate(claim.incurred_on)}</span>
                      {claim.note && <span className="truncate text-sm text-muted-foreground">{claim.note}</span>}
                      <StatusBadge claim={claim} tab={tab} />
                    </span>
                    <span className="flex shrink-0 flex-col items-end gap-1.5">
                      <span className="tabular font-medium">{formatIDR(claim.amount)}</span>
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
            })}
          </ul>
        )}
        <ReceiptDialog
          kind="reimbursements"
          parentId={receiptsForId}
          receiptIds={claims.find((c) => c.id === receiptsForId)?.receipt_ids ?? []}
          open={receiptsForId !== null}
          onClose={() => setReceiptsForId(null)}
          // Silent, so the list keeps its rows (and this dialog its claim)
          // while it re-reads - including after an uncertain save, when the
          // dialog's message has to stay on screen.
          onChanged={() => listRun(loadFirstPage, { silent: true })}
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
        <p role="alert" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention-text">
          {feedback.text}
        </p>
      )}

      {/* Outstanding is a subset of all, so a plain filter checkbox - the
          same control as Dues/Status.tsx's "belum bayar" filter. */}
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          className="size-4 rounded border-input"
          checked={outstandingShown}
          onChange={(event) => {
            const next = event.target.checked
            startTransition(() => {
              showOutstanding(next)
              chooseTab(next ? 'outstanding' : 'all')
            })
          }}
        />
        {text.outstandingFilterLabel}
      </label>

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

      <Button type="button" size="lg" onClick={() => navigate('/record?fronted=1', { state: recordOriginFrom(location) })}>
        {text.record.heading}
      </Button>

      {renderList()}

      <ClaimDetailDialog
        claim={entryClaim}
        memberName={(entryClaim && formDataState.data?.members.find((m) => m.id === entryClaim.member_id)?.name) ?? '-'}
        purposeName={(entryClaim && formDataState.data?.purposes.find((p) => p.id === entryClaim.purpose_id)?.name) ?? '-'}
        busy={submitting}
        error={feedback?.text ?? null}
        onClose={entry.close}
        onCloseAutoFocus={(event) => {
          const next = handoff.current
          if (next === null) return
          handoff.current = null
          event.preventDefault()
          if (next.kind === 'settle') dialog.open(`settle:${next.id}`)
          else if (next.kind === 'delete') dialog.open(`delete-claim:${next.id}`)
          else if (next.kind === 'correct') navigate(`/reimbursements/correct?id=${next.id}`)
          else setReceiptsForId(next.id)
        }}
        onSettle={(claim) => handOff('settle', claim)}
        onCorrect={(claim) => handOff('correct', claim)}
        onWaive={(claim) => handleWaive(claim.id)}
        onUnwaive={(claim) => handleUnwaive(claim.id)}
        onDelete={(claim) => handOff('delete', claim)}
        onViewReceipts={(claim) => handOff('receipts', claim)}
        onReceiptsChanged={() => void listRun(loadFirstPage, { silent: true })}
      />
      <SettleDialog claimId={settleId} onClose={dialog.close} onSettled={handleSettled} onUnusable={clearDialog} />
      <DeleteClaimDialog
        open={listHasDeleteClaim}
        claim={deleteClaim}
        memberName={(deleteClaim && formDataState.data?.members.find((m) => m.id === deleteClaim.member_id)?.name) ?? '-'}
        purposeName={(deleteClaim && formDataState.data?.purposes.find((p) => p.id === deleteClaim.purpose_id)?.name) ?? '-'}
        onClose={dialog.close}
        onDeleted={handleDeleted}
        refresh={() => listRun(loadFirstPage, { silent: true })}
      />
    </div>
  )
}

/**
 * Hapus talangan, a plain destructive confirm as a dialog (ADR-046), addressed
 * by `?edit=delete-claim:<id>`. It never posts a ledger entry - the claim has
 * moved no money - so it is the one dialog here that keeps outside-tap
 * dismissal. A refusal (the claim still has a nota) is said inside it, where she
 * is looking, and the dialog stays so she can read why. The body is its own
 * component so the error starts empty every time it opens.
 */
function DeleteClaimDialog({
  open,
  claim,
  memberName,
  purposeName,
  onClose,
  onDeleted,
  refresh,
}: {
  open: boolean
  claim: Reimbursement | null
  memberName: string
  purposeName: string
  onClose: () => void
  onDeleted: () => void
  refresh: () => Promise<void>
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        {claim !== null && (
          <DeleteClaimBody
            claim={claim}
            memberName={memberName}
            purposeName={purposeName}
            onClose={onClose}
            onDeleted={onDeleted}
            refresh={refresh}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function DeleteClaimBody({
  claim,
  memberName,
  purposeName,
  onClose,
  onDeleted,
  refresh,
}: {
  claim: Reimbursement
  memberName: string
  purposeName: string
  onClose: () => void
  onDeleted: () => void
  refresh: () => Promise<void>
}) {
  const [state, run] = useApi<unknown>()
  const busy = state.status === 'loading'

  return (
    <>
      <DialogHeader>
        <DialogTitle>{text.delete.dialogTitle}</DialogTitle>
      </DialogHeader>
      <ClaimSummary claim={claim} memberName={memberName} purposeName={purposeName} />
      <p className="text-sm">{text.delete.body}</p>
      {state.status === 'error' && state.error && (
        <p role="alert" className="text-sm text-attention-text">
          {errorText(state.error)}
        </p>
      )}
      <DialogFooter className="mt-1">
        <Button type="button" size="lg" variant="outline" onClick={onClose} disabled={busy}>
          {copy.common.close}
        </Button>
        <Button
          type="button"
          size="lg"
          variant="destructive"
          disabled={busy}
          onClick={() =>
            void run(
              async () => {
                await deleteReimbursement(claim.id)
                onDeleted()
              },
              { settle: refresh },
            )
          }
        >
          {busy ? text.actions.deleting : text.delete.confirm}
        </Button>
      </DialogFooter>
    </>
  )
}

function StatusBadge({ claim, tab }: { claim: Reimbursement; tab: 'outstanding' | 'all' }) {
  if (claim.waived_on) {
    return (
      <span className="mt-1 self-start rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
        {text.status.waived}
      </span>
    )
  }
  // `settled` travels on the wire: the list queries compute whether a
  // kind='reimbursement' payout references the claim, so this badge is honest
  // on both tabs: the list says "Dibayar" only for a row a payout settled.
  if (claim.settled) {
    return (
      <span className="mt-1 self-start rounded-full bg-success-soft px-2 py-0.5 text-xs font-medium text-success">
        {text.status.settled}
      </span>
    )
  }
  // The outstanding view's own filter already says "belum dibayar"; a badge
  // saying it on every card is the same sentence again. The all view needs it.
  if (tab === 'outstanding') return null
  return (
    <span className="mt-1 self-start rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
      {text.status.outstanding}
    </span>
  )
}
