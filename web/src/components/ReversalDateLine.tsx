import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import type { Transaction } from '@/lib/transactions'

const text = copy.history.transactions

/**
 * The line a reversal form states under its title: the date the ledger will
 * give the reversal (ADR-042). The client cannot see the cek kas counts, so
 * the row's `counted` picks the line: the original's own day, or today once a
 * count has covered it. The ledger decides at post time; this is only what it
 * will say now.
 */
export default function ReversalDateLine({ transaction }: { transaction: Transaction }) {
  return (
    <p className="text-sm text-muted-foreground">
      {transaction.counted ? text.reverseDateToday : text.reverseDateOriginal(formatIsoDate(transaction.occurred_on))}
    </p>
  )
}
