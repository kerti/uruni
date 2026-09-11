import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { ReconciliationLine } from '@/lib/reconciliations'

const text = copy.reconciliation

/**
 * The per-location line list within one reconciliation snapshot - shared by
 * Reconcile.tsx's own Confirmation state and Cek kas's read-only detail
 * sheet (#227, extracted out of Confirmation so both render identically):
 * location name, recorded versus actual, the difference when there is one,
 * and the resolution's own label. Tabular figures throughout (`tabular`),
 * formatIDR at the display edge only (CLAUDE.md rule 1).
 */
export default function ReconciliationLines({
  lines,
  accountNames,
}: {
  lines: ReconciliationLine[]
  accountNames: Map<number, string>
}) {
  return (
    <ul className="flex flex-col gap-2">
      {lines.map((line) => (
        <li key={line.id} className="flex flex-col gap-1 rounded-lg bg-card p-4 ring-1 ring-foreground/10">
          <span className="font-medium">{accountNames.get(line.account_id) ?? copy.home.purposeUnknown}</span>
          <span className="tabular text-sm text-muted-foreground">
            {text.recordedLabel}: {formatIDR(line.recorded_amount)} {'\u00b7'} {formatIDR(line.actual_amount)}
          </span>
          {line.difference_amount !== 0 && (
            <span className="tabular text-sm text-muted-foreground">
              {text.differenceLabel}: {formatIDR(line.difference_amount)}
            </span>
          )}
          <span className="text-sm">
            {text.resolutionOptions[line.resolution as keyof typeof text.resolutionOptions] ?? line.resolution}
          </span>
        </li>
      ))}
    </ul>
  )
}
