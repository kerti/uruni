import { rowLabelFor } from '@/components/rowLabel'
import type { Transaction } from '@/lib/transactions'

/**
 * One row's label line: a small, muted, decorative icon (aria-hidden) then
 * the visible text, with the kind word added for screen readers only where
 * the visible text does not say it. Renders nothing for a row she recorded
 * herself.
 */
export default function TransactionRowLabel({
  transaction,
  wrap = false,
  bare = false,
  kindShown = false,
}: {
  transaction: Transaction
  /** Wraps instead of truncating - the entry detail, where the whole label is
   * the point. */
  wrap?: boolean
  /** Leaves the icon out, because the row already leads with it
   * (TransactionGlyph) - a transfer's glyph is its label's. */
  bare?: boolean
  /** The kind word is already visible on the line above (a transfer, #738),
   * so the sr-only copy of it would be read twice. */
  kindShown?: boolean
}) {
  const label = rowLabelFor(transaction)
  if (!label) return null

  const { Icon, srWord, text } = label
  const wraps = wrap || label.wraps === true
  return (
    <span className={`flex min-w-0 gap-1 text-sm text-muted-foreground ${wraps ? 'items-start' : 'items-center'}`}>
      {!bare && <Icon aria-hidden="true" className={`size-4 shrink-0 ${wraps ? 'mt-0.5' : ''}`} />}
      {srWord && !kindShown && <span className="sr-only">{srWord}</span>}
      <span className={wraps ? 'break-words' : 'truncate'}>{text}</span>
    </span>
  )
}
