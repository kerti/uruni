import type { ReactNode } from 'react'

export interface SummaryItem {
  label: string
  value: ReactNode
  /** Free text (a note): the label above, the value below it, left-aligned
   * across the full width - a paragraph right-aligned beside its label reads
   * with a ragged left edge. */
  block?: boolean
}

/**
 * The record a dialog acts on, as label/value lines (ADR-046): a confirm or a
 * short edit shows what it is about to change, so she never has to close it
 * to remember what she opened it for. Label left, value right-aligned, one
 * line each - the shape #737 approved for Bayar talangan. Items with no value
 * are the caller's to leave out.
 *
 * Long values wrap inside the dialog, never past it: `min-w-0` lets the value
 * shrink in the row, and `wrap-anywhere` breaks a string with no spaces (a
 * pasted link) rather than letting it set the row's width. A short fact that
 * wraps is balanced, so a long name splits into even lines. A caller keeps an
 * amount whole with `whitespace-nowrap`.
 */
export default function RecordSummary({ items }: { items: SummaryItem[] }) {
  return (
    <dl className="flex flex-col gap-2 text-sm">
      {items.map((item) =>
        item.block ? (
          <div key={item.label} className="flex flex-col gap-0.5">
            <dt className="text-muted-foreground">{item.label}</dt>
            <dd className="text-pretty whitespace-pre-wrap wrap-anywhere">{item.value}</dd>
          </div>
        ) : (
          <div key={item.label} className="flex items-baseline justify-between gap-4">
            <dt className="shrink-0 text-muted-foreground">{item.label}</dt>
            <dd className="min-w-0 text-right text-balance wrap-anywhere">{item.value}</dd>
          </div>
        ),
      )}
    </dl>
  )
}
