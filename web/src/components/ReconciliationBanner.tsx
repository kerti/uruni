import { CaretRight, CheckCircle, MagnifyingGlass, Question } from '@phosphor-icons/react'
import type { ReactNode } from 'react'

import { copy } from '@/copy/id'
import { formatIDR } from '@/lib/money'
import type { OpenReconciliationLine } from '@/lib/reconciliations'

/**
 * The home screen's reconciliation status (PRD section 7.7). Three states,
 * not two:
 *
 *   never counted -> neutral. A fund nobody has reconciled has no open lines
 *     either, so a plain "openLines.length === 0" test would paint it green
 *     and tell the treasurer her cash matches her records - which Uruni
 *     cannot know. Uruni only sums its own ledger; the two figures meet
 *     exactly once, in the reconcile flow (PRD section 7.8), when she types
 *     what she counted. Green is earned by a count, never by the absence of
 *     one.
 *   counted, nothing open -> "cocok", `--success`/`--success-soft`.
 *   gaps still open -> "selisih", `--attention`/`--attention-soft`, never
 *     `--destructive`. A discrepancy is a normal thing to find at a count,
 *     not an alarm (CLAUDE.md rule 9, as in ErrorState).
 *
 * The figure is the sum of every open line's remainder_amount by
 * *magnitude* (what is still open after the line's fix, not the gap found at
 * the count). remainder_amount is signed like difference_amount - the
 * schema's CHECK is `difference_amount = actual_amount - recorded_amount`, so
 * a location holding less cash than the ledger says carries a negative one.
 * Summing the raw values would let a short location and an over location
 * cancel out and render "selisih Rp0" while two gaps sit open, and a single
 * short location would put a minus sign in the middle of the sentence.
 * Neither is what "how much is unaccounted for" means to a treasurer; the
 * per-line direction is the reconcile screen's job.
 *
 * Plain integer arithmetic throughout - no float ever touches a rupiah
 * figure (CLAUDE.md rule 1) - formatted only here, at the display edge.
 *
 * An optional onClick makes the banner the reconcile screen's entry point
 * from home. It renders as a <button> only when one is passed; every other
 * render - standalone in tests, with no router beneath it - stays the plain
 * <p>.
 */
export default function ReconciliationBanner({
  openLines,
  everReconciled,
  onClick,
}: {
  openLines: OpenReconciliationLine[]
  everReconciled: boolean
  onClick?: () => void
}) {
  if (!everReconciled) {
    return (
      <Wrapper onClick={onClick} className="text-foreground" ring="ring-foreground/10">
        <Question aria-hidden="true" className="shrink-0 text-muted-foreground" />
        {copy.reconciliation.neverChecked}
      </Wrapper>
    )
  }

  if (openLines.length === 0) {
    return (
      <Wrapper onClick={onClick} className="text-foreground" ring="ring-success/50">
        <CheckCircle aria-hidden="true" className="shrink-0 text-success" />
        {copy.reconciliation.matched}
      </Wrapper>
    )
  }

  const totalDifference = openLines.reduce((sum, line) => sum + Math.abs(line.remainder_amount), 0)

  return (
    <Wrapper onClick={onClick} className="text-attention-text" ring="ring-attention/50">
      <MagnifyingGlass aria-hidden="true" className="shrink-0" />
      {/* The question on its own line, so the amount reads first. */}
      <span className="flex flex-col">
        <span>{copy.reconciliation.discrepancy(formatIDR(totalDifference))}</span>
        <span>{copy.reconciliation.discrepancyPrompt}</span>
      </span>
    </Wrapper>
  )
}

/** Renders a <button> when onClick is given, a <p> otherwise - two real
 * branches rather than a dynamic `Tag = onClick ? 'button' : 'p'`, which
 * would let a `type="button"` meant for the button leak into the <p> branch's
 * props at the type level. */
function Wrapper({
  onClick,
  className,
  ring,
  children,
}: {
  onClick?: () => void
  className: string
  /** The edge carries the state (#743): a white card like the pos rows below
   * it, so it never reads as a passing toast, with the icon and this border in
   * the status colour - green cocok, terracotta selisih, neutral never checked. */
  ring: string
  children: ReactNode
}) {
  // w-full + text-left: a <button> doesn't inherit <p>'s block-level,
  // full-width, left-aligned defaults.
  const combined = `flex w-full items-center gap-2 rounded-lg bg-card px-4 py-3 text-left ring-1 ${ring} ${className}`
  if (onClick) {
    // The tinted panel alone reads as a status message, not something to tap.
    // What makes it a control: a chevron pointing where it goes, a hairline
    // edge, the pressed-state nudge and shadow every Button has, a visible
    // focus ring, and 44px of height guaranteed by min-h-11
    // (Design-System.md's minimum touch target, which px-4 py-3 alone does not
    // promise).
    return (
      <button
        type="button"
        className={`${combined} min-h-11 justify-between shadow-card transition-all outline-none hover:brightness-[0.98] focus-visible:ring-3 focus-visible:ring-ring/50 active:translate-y-px motion-reduce:transition-none`}
        onClick={onClick}
      >
        <span className="flex items-center gap-2">{children}</span>
        <CaretRight aria-hidden="true" className="size-5 shrink-0 opacity-70" />
      </button>
    )
  }
  return <p className={combined}>{children}</p>
}
