import { useCallback, useEffect } from 'react'
import { Undo2 } from 'lucide-react'

import ReversalDialog from '@/components/ReversalDialog'
import { sharedErrorText, type ReversalCopy } from '@/components/reversalCopy'
import { Button } from '@/components/ui/button'
import { useToast } from '@/components/toast/useToast'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { Transaction } from '@/lib/transactions'

const text = copy.dues.history

/** The dues payment's own wording for the shared reversal dialog: no
 * explainer, and the shared error map (a dues payment says "iuran"). */
const reversalWords: ReversalCopy = {
  title: text.reverse,
  noteLabel: text.noteLabel,
  cancel: text.cancel,
  confirm: text.confirm,
  submitting: text.submitting,
}

/**
 * One member's dues rows for one period, with a reversal action on each
 * payment not already reversed (PRD section 7.3: "undo a dues payment
 * recorded in error - the payment is reversed by a new entry, never edited
 * away").
 *
 * Not GET /api/dues-payments (the Iuran tab's fund-wide history, with no
 * member or period filter): a dues payment is an ordinary transaction row, so
 * this reads GET /api/transactions?member_id=&dues_period= (filtered
 * server-side, never client-side over a paged list). Both halves of a
 * reversal come back under that filter - the kind='dues' payment, and the
 * kind='adjustment' row that reverses it (ADR-029 copies the member and
 * period onto the reversal; the schema only lets an adjustment carry a
 * member_id/dues_period when it is a reversal). Showing only the payment
 * would hide the correction that is the point of never editing a posted row.
 *
 * Nothing here decides what a reversal contains: the request carries only a
 * note, and the ledger copies account, purpose, amount, member and period from
 * the reversed row and dates it itself (ADR-042). A blank reason sends null, never a generated
 * default - the row explains itself through TransactionList's display label.
 */
export default function MemberPayments({ memberId, period, onReversed }: { memberId: number; period: string; onReversed: () => void }) {
  const [state, run] = useApi<Transaction[]>()
  const toast = useToast()

  // The reversal dialog, `?edit=reverse:<id>` (ADR-046), shared with
  // TransactionList. Several members' payments can be expanded at once, so a
  // value naming a row this one does not hold is left alone, never cleared:
  // it may be a neighbour's.
  const reversal = useDialogParam()
  const reversalTarget = parseDialogTarget('reverse', reversal.value)
  const reversalId = reversalTarget.kind === 'edit' ? reversalTarget.id : null

  const loadRows = useCallback(async (): Promise<Transaction[]> => {
    const page = await listTransactions({ memberId, duesPeriod: period })
    return page.transactions
  }, [memberId, period])

  useEffect(() => {
    void run(loadRows)
    // run is a stable useCallback (useApi.ts); an expanded member row mounts
    // this component, so the fetch belongs to that expansion.
  }, [run, loadRows])

  // The server already filtered to this member+period - everything it returns
  // is either the kind='dues' payment or a kind='adjustment' reversal of it
  // (schema CHECK: only a reversal may carry member_id+dues_period on a
  // non-'dues' row), so no client-side filtering.
  const rows = state.data ?? []
  // A payment is spent once any row reverses it.
  const reversedIds = new Set(rows.map((t) => t.reverses_transaction_id).filter((id): id is number => id !== null))

  const reversing = rows.find((row) => row.id === reversalId) ?? null

  function handleReversed() {
    reversal.close()
    // Refetch this list *and* tell the roster above to refetch: the member
    // reads as unpaid for the period again, and neither refresh is the
    // treasurer's job.
    void run(loadRows, { silent: true })
    onReversed()
    toast.success(text.success)
  }

  if (state.status === 'idle' || state.status === 'loading') {
    return <Loading />
  }

  if (state.status === 'error' && state.error) {
    return <ErrorState error={state.error} onRetry={() => void run(loadRows)} />
  }

  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">{text.empty}</p>
  }

  // The panel's surface is the caller's (Status.tsx); rows only separate from
  // each other - 12px of air on both sides of each hairline, symmetric so the
  // rule belongs to neither record. A payment row is three short lines and one
  // occasional button, not a card: it reads better dense.
  //
  // Deliberately not `gap-* divide-y`: Tailwind v4's divide-y draws the border
  // on the *bottom* of every item but the last, so a flex gap lands entirely
  // below the line and the rule ends up hugging the record above and floating
  // far from the one below. Each row owning its padding and top border
  // centres the hairline.
  return (
    <>
      <ul className="flex flex-col [&>li]:border-t [&>li]:border-foreground/10 [&>li]:py-3 [&>li:first-child]:border-t-0 [&>li:first-child]:pt-0 [&>li:last-child]:pb-0">
        {rows.map((row) => {
          const isReversal = row.reverses_transaction_id !== null
          const reversed = reversedIds.has(row.id)
          return (
            <li key={row.id} className="flex flex-col gap-1 text-sm">
              <div className="tabular flex items-center justify-between gap-3">
                {/* The date leads the row, so it carries the same weight the
                  amount does at the other end of it. */}
                <span className="font-semibold text-muted-foreground">
                  {formatIsoDate(row.occurred_on)}
                  {isReversal && ` \u00b7 ${text.reversalRow}`}
                </span>
                <span className="font-medium">{formatIDR(row.amount)}</span>
              </div>

              {row.note && <span className="truncate text-muted-foreground">{row.note}</span>}

              {!isReversal && reversed && <span className="text-muted-foreground">{text.reversedBadge}</span>}

              {/* `lg` on every control in this panel, never `sm` (h-7 = 28px):
                Design-System.md's minimum touch target is 44x44px, and a
                control that undoes money is the last place to shave it. */}
              {!isReversal && !reversed && (
                <Button type="button" variant="outline" size="lg" className="self-start" onClick={() => reversal.open(`reverse:${row.id}`)}>
                  <Undo2 aria-hidden="true" />
                  {text.reverse}
                </Button>
              )}
            </li>
          )
        })}
      </ul>
      <ReversalDialog
        transaction={reversing}
        open={reversing !== null}
        onClose={reversal.close}
        onReversed={handleReversed}
        refresh={() => run(loadRows, { silent: true })}
        words={reversalWords}
        errorText={sharedErrorText}
      />
    </>
  )
}
