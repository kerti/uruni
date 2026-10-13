import { Camera, ImagePlus } from 'lucide-react'

import { copy } from '@/copy/id'
import { cn } from '@/lib/utils'

/**
 * The row-level photo affordance, shared by TransactionList.tsx and
 * History/Reimbursements.tsx so the two lists read as one control.
 *
 * Plain ink, no chrome - as the row marks its other tappable thing, the
 * dotted-underline purpose name. A row with a nota shows a Forest `Camera`
 * plus the count (always shown, even at 1); a row with none shows a muted
 * `ImagePlus`, barely there, since most entries never get a photo. Colour and
 * glyph carry the difference, not a filled box.
 *
 * The 44px hit area (Design-System.md's minimum) is an invisible `after:`
 * layer centred on the icon, so it takes no layout space: the row keeps its
 * even padding instead of stretching around a 44px box.
 */
export default function ReceiptRowButton({
  receiptIds,
  onClick,
  className,
}: {
  receiptIds: number[]
  onClick: () => void
  className?: string
}) {
  const count = receiptIds.length
  const hasReceipt = count > 0

  return (
    <button
      type="button"
      aria-label={copy.receipts.rowControlAria(hasReceipt)}
      onClick={onClick}
      className={cn(
        "relative flex shrink-0 items-center gap-1 transition-colors after:absolute after:top-1/2 after:left-1/2 after:size-11 after:-translate-x-1/2 after:-translate-y-1/2 after:content-['']",
        hasReceipt ? 'text-primary hover:text-primary/80' : 'text-muted-foreground/70 hover:text-foreground',
        className,
      )}
    >
      {hasReceipt ? (
        <>
          <Camera aria-hidden="true" className="size-4" />
          <span className="tabular text-sm font-medium">{count}</span>
        </>
      ) : (
        <ImagePlus aria-hidden="true" className="size-4" />
      )}
    </button>
  )
}
