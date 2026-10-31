import { ArrowDownLeft, ArrowUpRight, ArrowsLeftRight } from '@phosphor-icons/react'

import { isTransfer, rowLabelFor } from '@/components/rowLabel'
import type { Transaction } from '@/lib/transactions'

/**
 * The glyph a row leads with: money in, money out, or - for a transfer, which
 * is neither - the movement's own glyph, muted. Decorative (aria-hidden); what
 * a screen reader hears is the row's label and its sr-only kind word.
 */
export default function TransactionGlyph({ transaction, className = 'mt-0.5' }: { transaction: Transaction; className?: string }) {
  if (isTransfer(transaction)) {
    const Icon = rowLabelFor(transaction)?.Icon ?? ArrowsLeftRight
    return <Icon aria-hidden="true" className={`shrink-0 text-muted-foreground ${className}`} />
  }
  return transaction.direction === 'in' ? (
    <ArrowDownLeft aria-hidden="true" className={`shrink-0 text-success ${className}`} />
  ) : (
    <ArrowUpRight aria-hidden="true" className={`shrink-0 text-attention ${className}`} />
  )
}
