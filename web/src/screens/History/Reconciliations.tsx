import { useCallback, useEffect, useRef, useState } from 'react'

import ReconciliationLines from '@/components/ReconciliationLines'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { listAccounts } from '@/lib/accounts'
import { formatUnixSeconds } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { getReconciliation, listReconciliations } from '@/lib/reconciliations'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { Account } from '@/lib/accounts'
import type { ReconciliationDetail, ReconciliationListItem, ReconciliationsPage } from '@/lib/reconciliations'

const text = copy.reconciliation
const tabText = copy.history.reconciliations

/**
 * Riwayat's Cek kas tab (PRD section 7.8's "every reconciliation is saved as a
 * snapshot"): a paged, read-only history of past counts, newest first. Opening
 * a row shows its per-location detail in a bottom-sheet dialog, addressed by
 * `?snapshot=<id>` so back, reload and a deep link all agree with what is on
 * screen.
 *
 * Read-only is a hard rule (the home banner is the reconcile flow's one door):
 * nothing on this tab or in the sheet starts a reconciliation. No search
 * either (ADR-032: "a handful of dated snapshots a year").
 *
 * No heading, no back button and no body copy of its own - Riwayat's `<h1>`
 * names the page, and the tab strip is the way back, as on the other tabs.
 *
 * `refetchKey` is App.tsx's `useRefetchKey()` (location.key, minus dialog
 * entries), the mechanism the other tabs use to pick up a write made
 * elsewhere - a reconciliation just taken on /reconcile, say.
 */
export default function Reconciliations({ refetchKey }: { refetchKey?: unknown }) {
  const { value, open, close } = useDialogParam('snapshot')

  const [listState, listRun] = useApi<ReconciliationsPage>()
  // Pages after the first, appended in order - reset whenever the first page is
  // refetched. `generation` stops a slow "load more" from a stale fetch
  // appending its rows after a refetch has landed, as in
  // History/Transactions.tsx and History/Reimbursements.tsx.
  const [more, setMore] = useState<ReconciliationsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  const [accountsState, accountsRun] = useApi<Account[]>()

  const refetchFirstPage = useCallback(() => {
    generation.current += 1
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void listRun(() => listReconciliations())
  }, [listRun])

  useEffect(() => {
    // oxlint-disable-next-line react/set-state-in-effect -- refetchFirstPage resets the appended pages and refetches when the key moves; the refetch is the external sync
    refetchFirstPage()
  }, [refetchFirstPage, refetchKey])

  useEffect(() => {
    // accountsRun is a stable useCallback (useApi.ts); fires once. Location
    // names for the detail sheet - the list Reconcile.tsx fetches for its count
    // form.
    void accountsRun(listAccounts)
  }, [accountsRun])

  async function loadMore(cursor: string) {
    const startedIn = generation.current
    setMoreLoading(true)
    setMoreError(null)
    try {
      const page = await listReconciliations({ cursor })
      if (startedIn !== generation.current) return
      setMore((prev) => ({
        reconciliations: [...(prev?.reconciliations ?? []), ...page.reconciliations],
        nextCursor: page.nextCursor,
      }))
    } catch (err) {
      if (startedIn !== generation.current) return
      setMoreError(err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err)))
    } finally {
      if (startedIn === generation.current) setMoreLoading(false)
    }
  }

  // ?snapshot= names a row by its numeric id - no prefix to parse, and no "new"
  // case: the sheet only ever opens on an existing snapshot.
  const snapshotId = value !== null && /^[0-9]+$/.test(value) ? Number(value) : null

  if (listState.status === 'idle' || listState.status === 'loading') {
    return <Loading />
  }

  if (listState.status === 'error' || !listState.data) {
    return listState.error ? <ErrorState error={listState.error} onRetry={refetchFirstPage} /> : null
  }

  const page = listState.data
  const rows = more ? [...page.reconciliations, ...more.reconciliations] : page.reconciliations
  const nextCursor = more ? more.nextCursor : page.nextCursor

  return (
    <div className="flex flex-col gap-4">
      {rows.length === 0 ? (
        <p className="text-muted-foreground">{tabText.emptyState}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {rows.map((row) => (
            <li key={row.id}>
              <button
                type="button"
                onClick={() => open(String(row.id))}
                className="flex min-h-11 w-full items-center justify-between gap-3 rounded-lg bg-card px-4 py-3 text-left shadow-card ring-1 ring-foreground/10 outline-none select-none transition-colors hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50"
              >
                <span>{formatUnixSeconds(row.performed_at)}</span>
                <SnapshotBadge row={row} />
              </button>
            </li>
          ))}
        </ul>
      )}

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
          {moreLoading ? copy.common.loading : tabText.loadMore}
        </Button>
      )}

      <SnapshotDialog id={snapshotId} open={snapshotId !== null} onClose={close} accounts={accountsState.data ?? []} />
    </div>
  )
}

/** cocok (green) for a snapshot with nothing left open, selisih (terracotta)
 * with the open figure otherwise - never destructive-red: a discrepancy is a
 * normal thing to find at a count (CLAUDE.md rule 9, as in
 * ReconciliationBanner.tsx). */
function SnapshotBadge({ row }: { row: ReconciliationListItem }) {
  if (row.open_difference_amount === 0) {
    return (
      <span className="shrink-0 rounded-full bg-success-soft px-2 py-0.5 text-xs font-medium text-success">
        {text.resolutionOptions.matched}
      </span>
    )
  }
  return (
    <span className="tabular shrink-0 rounded-full bg-attention-soft px-2 py-0.5 text-xs font-medium text-attention-text">
      {text.selisihBadge(formatIDR(row.open_difference_amount))}
    </span>
  )
}

/**
 * The read-only detail sheet: fetches GET /api/reconciliations/{id} the moment
 * `id` is set, and renders through the ReconciliationLines that Confirmation
 * on /reconcile uses. No footer, no action - this sheet never starts a
 * reconciliation.
 */
function SnapshotDialog({ id, open, onClose, accounts }: { id: number | null; open: boolean; onClose: () => void; accounts: Account[] }) {
  const [state, run] = useApi<ReconciliationDetail>()

  useEffect(() => {
    if (id === null) return
    void run(() => getReconciliation(id))
  }, [id, run])

  const accountNames = new Map(accounts.map((a) => [a.id, a.name]))

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{state.data ? formatUnixSeconds(state.data.performed_at) : text.heading}</DialogTitle>
        </DialogHeader>

        {(state.status === 'idle' || state.status === 'loading') && <Loading />}
        {state.status === 'error' && state.error && (
          <ErrorState error={state.error} onRetry={() => id !== null && void run(() => getReconciliation(id))} />
        )}
        {state.status === 'success' && state.data && (
          <div className="flex flex-col gap-3">
            <ReconciliationLines lines={state.data.lines} accountNames={accountNames} />
            {state.data.note && (
              <p className="text-sm text-muted-foreground">
                <span className="font-medium text-foreground">{tabText.noteLabel}:</span> {state.data.note}
              </p>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
