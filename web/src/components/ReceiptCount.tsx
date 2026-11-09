import { Camera } from '@phosphor-icons/react'

import { copy } from '@/copy/id'

/**
 * A list card's nota count, under its amount: read-only status, shown only
 * once a nota exists (adding one is the detail's Nota button). The camera is
 * decoration; the count is visible text and a visually hidden word follows
 * it, so the card button's name reads "2 foto nota" (#808, label in name).
 * The space is its own text node - one inside the sr-only span is trimmed
 * from the computed name.
 */
export default function ReceiptCount({ count }: { count: number }) {
  if (count <= 0) return null
  return (
    <span className="flex items-center gap-1 text-primary">
      <Camera aria-hidden="true" className="size-4" />
      <span className="tabular text-sm font-medium">{count}</span>{' '}
      <span className="sr-only">{copy.transactionDetail.receiptCountWord}</span>
    </span>
  )
}
