import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { ReconciliationLine } from '@/lib/reconciliations'

const text = copy.reconciliation

/**
 * The per-location line list within one reconciliation snapshot - shared by
 * Reconcile.tsx's Confirmation state and Cek kas's read-only detail sheet so
 * both render identically: location name, recorded versus actual, the
 * difference when there is one, and the resolution's label. Tabular figures
 * throughout, formatIDR at the display edge only (CLAUDE.md rule 1).
 */
export default function ReconciliationLines({ lines, accountNames }: { lines: ReconciliationLine[]; accountNames: Map<number, string> }) {
  return (
    <ul className="flex flex-col gap-2">
      {lines.map((line) => (
        <li key={line.id} className="flex flex-col gap-1 rounded-lg bg-card p-4 shadow-card ring-1 ring-foreground/10">
          <span className="font-medium">{accountNames.get(line.account_id) ?? copy.home.purposeUnknown}</span>
          {/* The figures being compared each get a line of their own, label
              left and amount right, so the amounts line up to compare. */}
          <Figure label={text.recordedLabel} amount={line.recorded_amount} />
          <Figure label={text.countedLabel} amount={line.actual_amount} />
          {line.difference_amount !== 0 && <Figure label={text.differenceLabel} amount={line.difference_amount} />}
          {line.resolution !== 'left_open' && line.remainder_amount !== 0 && (
            <span className="tabular text-sm text-attention-text">{text.remainingGap(formatIDR(Math.abs(line.remainder_amount)))}</span>
          )}
          <span className="text-sm">
            {text.resolutionOptions[line.resolution as keyof typeof text.resolutionOptions] ?? line.resolution}
          </span>
        </li>
      ))}
    </ul>
  )
}

function Figure({ label, amount }: { label: string; amount: number }) {
  return (
    <span className="tabular flex justify-between gap-3 text-sm text-muted-foreground">
      <span>{label}</span>
      <span>{formatIDR(amount)}</span>
    </span>
  )
}
