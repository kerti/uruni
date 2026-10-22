import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import type { Transaction } from '@/lib/transactions'

const text = copy.history.transactions

/**
 * The date the ledger will give a reversal (ADR-042), stated under the form's
 * title. The row's `counted` picks the line; the ledger decides at post time.
 */
export default function ReversalDateLine({ transaction }: { transaction: Transaction }) {
  return (
    <p className="text-sm text-muted-foreground">
      {transaction.counted ? text.reverseDateToday : text.reverseDateOriginal(formatIsoDate(transaction.occurred_on))}
    </p>
  )
}
