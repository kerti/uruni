import { CircleCheck, CircleHelp, TriangleAlert } from 'lucide-react'

import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { OpenReconciliationLine } from '@/lib/reconciliations'

/**
 * The home screen's reconciliation status (M6.9, PRD §7.7). Three states,
 * not two:
 *
 *   never counted -> neutral. A fund nobody has ever reconciled has no open
 *     lines either, so a plain "openLines.length === 0" test would paint it
 *     green and tell the treasurer her cash matches her records - which
 *     Uruni cannot know and has never checked. Uruni only ever sums its own
 *     ledger; the two figures meet exactly once, in the reconcile flow
 *     (M6.10, PRD §7.8), when she types what she actually counted. Green is
 *     earned by a count, never by the absence of one.
 *   counted, nothing open -> "cocok", `--success`/`--success-soft`.
 *   gaps still open -> "selisih", `--attention`/`--attention-soft`, never
 *     `--destructive`. A discrepancy is a normal thing to find at a count,
 *     not an alarm (CLAUDE.md rule 9, ErrorState's own reasoning for the
 *     same choice).
 *
 * The figure is the sum of every open line's difference_amount by
 * *magnitude*. difference_amount is signed - the schema's CHECK is
 * `difference_amount = actual_amount - recorded_amount`, so a location
 * holding less cash than the ledger says carries a negative one. Summing
 * the raw values would let a short location and an over location cancel
 * out and render "selisih Rp 0" while two gaps sit open, and a single
 * short location would put a minus sign in the middle of the sentence.
 * Neither is what "how much is unaccounted for" means to a treasurer; the
 * per-line direction is the reconcile screen's job (M6.10), not the
 * banner's.
 *
 * Plain integer arithmetic throughout - no float ever touches a rupiah
 * figure (CLAUDE.md rule 1) - formatted only here, at the display edge.
 */
export default function ReconciliationBanner({
  openLines,
  everReconciled,
}: {
  openLines: OpenReconciliationLine[]
  everReconciled: boolean
}) {
  if (!everReconciled) {
    return (
      <p className="flex items-center gap-2 rounded-lg bg-muted px-4 py-3 text-muted-foreground">
        <CircleHelp aria-hidden="true" />
        {copy.reconciliation.neverChecked}
      </p>
    )
  }

  if (openLines.length === 0) {
    return (
      <p className="flex items-center gap-2 rounded-lg bg-success-soft px-4 py-3 text-success">
        <CircleCheck aria-hidden="true" />
        {copy.reconciliation.matched}
      </p>
    )
  }

  const totalDifference = openLines.reduce((sum, line) => sum + Math.abs(line.difference_amount), 0)

  return (
    <p className="flex items-center gap-2 rounded-lg bg-attention-soft px-4 py-3 text-attention">
      <TriangleAlert aria-hidden="true" />
      {copy.reconciliation.discrepancy(formatIDR(totalDifference))}
    </p>
  )
}
