import { useEffect, useState } from 'react'
import { Undo2 } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { reverseDuesPayment } from '@/lib/dues'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Transaction } from '@/lib/transactions'

const text = copy.dues.history


/** Local YYYY-MM-DD - never toISOString(). Same helper as
 * RecordPayment.tsx's own. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

/**
 * One member's dues rows for one period, with a reversal action on each
 * payment that has not already been reversed (M6.14, PRD §7.3: "undo a dues
 * payment recorded in error - the payment is reversed by a new entry, never
 * edited away").
 *
 * There is no payment-history route to read: a dues payment is an ordinary
 * transaction row, so this reads GET /api/transactions and filters by
 * member and period client-side. Both halves of a reversal live in that
 * list and both are shown - the kind='dues' payment, and the
 * kind='adjustment' row that reverses it (ADR-029 copies the member and
 * period onto the reversal, which is what makes it findable here). Showing
 * only the payment would hide the correction that is the whole point of
 * never editing a posted row.
 *
 * Nothing here decides what a reversal contains: the request carries only a
 * date and a note, and the ledger copies account, purpose, amount, member
 * and period from the row being reversed.
 */
export default function MemberPayments({
  memberId,
  memberName,
  period,
  onReversed,
}: {
  memberId: number
  memberName: string
  period: string
  onReversed: () => void
}) {
  const [state, run] = useApi<Transaction[]>()
  const [submitState, submitRun] = useApi<unknown>()

  // Which payment's reversal form is open, if any - one at a time, so the
  // date and note below always belong to a single row.
  const [reversingId, setReversingId] = useState<number | null>(null)
  const [occurredOn, setOccurredOn] = useState(todayISODate)
  const [note, setNote] = useState('')

  useEffect(() => {
    void run(listTransactions)
    // run is a stable useCallback (useApi.ts); this component is mounted by
    // an expanded member row, so the fetch belongs to that expansion.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, memberId, period])

  const all = state.data ?? []
  const rows = all.filter(
    (t) => t.member_id === memberId && t.dues_period === period && (t.kind === 'dues' || t.reverses_transaction_id !== null),
  )
  // A payment is spent once any row in the fund reverses it - derived from
  // the whole list, not from `rows`, so it stays true even if a reversal
  // were ever filtered out above.
  const reversedIds = new Set(all.map((t) => t.reverses_transaction_id).filter((id): id is number => id !== null))

  const submitting = submitState.status === 'loading'

  function openReversal(transactionId: number) {
    setReversingId(transactionId)
    setOccurredOn(todayISODate())
    setNote('')
  }

  function submitReversal(transactionId: number) {
    void submitRun(async () => {
      const trimmed = note.trim()
      const result = await reverseDuesPayment(transactionId, occurredOn, trimmed === '' ? text.note(memberName) : trimmed)
      setReversingId(null)
      // Refetch this list *and* tell the roster above to refetch its own:
      // the member reads as unpaid for the period again, and neither
      // refresh is the treasurer's job.
      await run(listTransactions)
      onReversed()
      return result
    })
  }

  if (state.status === 'idle' || state.status === 'loading') {
    return <Loading />
  }

  if (state.status === 'error' && state.error) {
    return <ErrorState error={state.error} onRetry={() => void run(listTransactions)} />
  }

  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">{text.empty}</p>
  }

  // The panel's own surface is the caller's (Status.tsx); rows only separate
  // from each other - 12px of air on both sides of each hairline, symmetric
  // so the rule reads as belonging to neither record. A payment row is
  // three short lines and one occasional button, not a card: it reads
  // better dense, with the rule doing the separating.
  //
  // Deliberately not `gap-* divide-y`: Tailwind v4's divide-y draws the
  // border on the *bottom* of every item but the last, so a flex gap lands
  // entirely below the line and the row's own padding above it - the rule
  // ends up hugging the record above and floating far from the one below.
  // Each row owning its own padding and top border is the shape that
  // actually centres the hairline.
  return (
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
                {isReversal && ` · ${text.reversalRow}`}
              </span>
              <span className="font-medium">{formatIDR(row.amount)}</span>
            </div>

            {row.note && <span className="truncate text-muted-foreground">{row.note}</span>}

            {!isReversal && reversed && <span className="text-muted-foreground">{text.reversedBadge}</span>}

            {/* `lg` on every control in this panel, never `sm` (h-7 = 28px):
                Design-System.md's minimum touch target is 44x44px, and a
                control that undoes money is the last place to shave it. */}
            {!isReversal && !reversed && reversingId !== row.id && (
              <Button type="button" variant="outline" size="lg" className="self-start" onClick={() => openReversal(row.id)}>
                <Undo2 aria-hidden="true" />
                {text.reverse}
              </Button>
            )}

            {reversingId === row.id && (
              <div className="animate-reveal flex flex-col gap-2 rounded-lg bg-muted/40 p-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`dues-reversal-date-${row.id}`}>{text.dateLabel}</Label>
                  <Input
                    id={`dues-reversal-date-${row.id}`}
                    type="date"
                    className="h-11"
                    value={occurredOn}
                    onChange={(event) => setOccurredOn(event.target.value)}
                    disabled={submitting}
                    required
                  />
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`dues-reversal-note-${row.id}`}>{text.noteLabel}</Label>
                  <Input
                    id={`dues-reversal-note-${row.id}`}
                    type="text"
                    className="h-11"
                    value={note}
                    onChange={(event) => setNote(event.target.value)}
                    disabled={submitting}
                  />
                </div>

                {submitState.status === 'error' && submitState.error && <ErrorState error={submitState.error} />}

                <Button
                  type="button"
                  size="lg"
                  disabled={submitting || occurredOn === ''}
                  onClick={() => submitReversal(row.id)}
                >
                  {submitting ? text.submitting : text.confirm}
                </Button>
                <Button type="button" variant="outline" size="lg" disabled={submitting} onClick={() => setReversingId(null)}>
                  {text.cancel}
                </Button>
              </div>
            )}
          </li>
        )
      })}
    </ul>
  )
}
