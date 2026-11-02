import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'
import { formatIDR } from '@/lib/money'

/**
 * A short column of labelled amounts that add up to a last line - an amplop's
 * Terkumpul / Terpakai / Sisa, Riwayat's month from Saldo awal to Saldo
 * akhir, the roll left when an amplop closed. Two screens had drawn it with
 * different gaps and a differently weighted total; this is the one look:
 * grey label, tabular medium amount, and a total set apart by a hairline
 * with label and amount both semibold.
 *
 * `Figures` is the list itself; the caller supplies the surface (a card, an
 * inset panel) through `className`.
 */
export function Figures({ className, children }: { className?: string; children: ReactNode }) {
  return <dl className={cn('flex flex-col gap-2 text-sm', className)}>{children}</dl>
}

export function FigureRow({ label, amount, total = false }: { label: ReactNode; amount: number; total?: boolean }) {
  return (
    <div className={cn('flex items-baseline justify-between gap-3', total && 'border-t border-border pt-2 font-semibold')}>
      <dt className={total ? undefined : 'text-muted-foreground'}>{label}</dt>
      <dd className={cn('tabular', total ? 'font-semibold' : 'font-medium')}>{formatIDR(amount)}</dd>
    </div>
  )
}
