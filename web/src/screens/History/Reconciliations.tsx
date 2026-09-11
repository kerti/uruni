import { useEffect, useRef, useState } from 'react'

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
 * Riwayat's Cek kas tab (#227, PRD section 7.8's "every reconciliation is saved as
 * a snapshot"): a paged, read-only history of past counts, newest first.
 * Opening a row shows its per-location detail in the #238 bottom-sheet
 * dialog, addressed by `?snapshot=<id>` so back, reload and a deep link all
 * agree with what is on screen.
 *
 * Read-only, hard rule (M6.10's own standing ruling: the home banner is the
 * reconcile flow's one door): nothing on this tab or in the sheet starts a
 * reconciliation. No search either (ADR-032's own table: "a handful of
 * dated snapshots a year").
 *
 * No heading, no back button and no body copy of its own - Riwayat's own
 * `<h1>` already names the page, and the tab strip is the way back, same as
 * the other three tabs.
 *
 * `refetchKey` is App.tsx's `location.key`, the same mechanism the other
 * tabs use to pick up a write made elsewhere - a fresh reconciliation just
 * taken on /reconcile, say.
 */
export default function Reconciliations({ refetchKey }: { refetchKey?: unknown }) {
  const { value, open, close } = useDialogParam('snapshot')

  const [listState, listRun] = useApi<ReconciliationsPage>()
  // Pages after the first, appended in order - reset whenever the first
  // page is refetched. `generation` stops a slow "load more" from a stale
  // fetch from appending its rows after a refetch has already landed - the
  // same guard History/Transactions.tsx and History/Reimbursements.tsx use.
  const [more, setMore] = useState<ReconciliationsPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  const [accountsState, accountsRun] = useApi<Account[]>()

  function refetchFirstPage() {
    generation.current += 1
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void listRun(() => listReconciliations())
  }

  useEffect(() => {
    refetchFirstPage()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refetchKey])

  useEffect(() => {
    // accountsRun is a stable useCallback (useApi.ts); fires once. Location
    // names for the detail sheet - the same list Reconcile.tsx already
    // fetches for its own count form.
    void accountsRun(listAccounts)
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  // ?snapshot= names a row by its numeric id - no prefix to parse (unlike
  // Locations.tsx's `location:new` / `location:<id>`, there is no "new" case
  // here: the sheet only ever opens on an existing snapshot).
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
                className="flex min-h-11 w-full items-center justify-between gap-3 rounded-lg bg-card px-4 py-3 text-left ring-1 ring-foreground/10 transition-colors hover:bg-muted/40"
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
 * with the open figure otherwise - never destructive-red: a discrepancy is
 * a normal thing to find at a count (CLAUDE.md rule 9), same reasoning
 * ReconciliationBanner.tsx's own comment gives. */
function SnapshotBadge({ row }: { row: ReconciliationListItem }) {
  if (row.open_difference_amount === 0) {
    return (
      <span className="shrink-0 rounded-full bg-success-soft px-2 py-0.5 text-xs font-medium text-success">
        {text.resolutionOptions.matched}
      </span>
    )
  }
  return (
    <span className="tabular shrink-0 rounded-full bg-attention-soft px-2 py-0.5 text-xs font-medium text-attention">
      {text.selisihBadge(formatIDR(row.open_difference_amount))}
    </span>
  )
}

/**
 * The read-only detail sheet (#238's dialog primitive): fetches
 * GET /api/reconciliations/{id} the moment `id` is set, and renders through
 * the same ReconciliationLines Confirmation on /reconcile uses. No footer,
 * no action - per #227's hard rule, this tab and this sheet never start a
 * reconciliation.
 */
function SnapshotDialog({
  id,
  open,
  onClose,
  accounts,
}: {
  id: number | null
  open: boolean
  onClose: () => void
  accounts: Account[]
}) {
  const [state, run] = useApi<ReconciliationDetail>()

  useEffect(() => {
    if (id === null) return
    void run(() => getReconciliation(id))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

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
