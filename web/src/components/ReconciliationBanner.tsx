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
  footnotes = [],
  tucked = false,
}: {
  openLines: OpenReconciliationLine[]
  everReconciled: boolean
  onClick?: () => void
  /** Small grey lines under the status - Beranda's "last checked" and "new
   * entries since" - so everything about the count sits in one card. */
  footnotes?: string[]
  /** Beranda's look: tucked under the hero's lower edge (see Wrapper). */
  tucked?: boolean
}) {
  const shared = { onClick, footnotes, tucked }

  if (!everReconciled) {
    return (
      <Wrapper
        {...shared}
        className="text-foreground"
        ring="ring-foreground/10"
        icon={<Question aria-hidden="true" className="shrink-0 text-muted-foreground" />}
      >
        <span>{copy.reconciliation.neverChecked}</span>
      </Wrapper>
    )
  }

  if (openLines.length === 0) {
    return (
      <Wrapper
        {...shared}
        className="text-foreground"
        ring="ring-success/50"
        icon={<CheckCircle aria-hidden="true" className="shrink-0 text-success" />}
      >
        <span>{copy.reconciliation.matched}</span>
      </Wrapper>
    )
  }

  const totalDifference = openLines.reduce((sum, line) => sum + Math.abs(line.remainder_amount), 0)

  return (
    <Wrapper
      {...shared}
      className="text-attention-text"
      ring="ring-attention/50"
      icon={<MagnifyingGlass aria-hidden="true" className="shrink-0" />}
    >
      {/* The question on its own line, so the amount reads first. */}
      <span>{copy.reconciliation.discrepancy(formatIDR(totalDifference))}</span>
      <span>{copy.reconciliation.discrepancyPrompt}</span>
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
  icon,
  footnotes,
  tucked,
  children,
}: {
  onClick?: () => void
  className: string
  icon: ReactNode
  footnotes: string[]
  tucked: boolean
  /** The edge carries the state (#743): a white card like the pos rows below
   * it, so it never reads as a passing toast, with the icon and this border in
   * the status colour - green cocok, terracotta selisih, neutral never checked. */
  ring: string
  children: ReactNode
}) {
  // Tucked (Beranda, ADR-038's 2026-11-02 amendment): the card slides out
  // from under the hero - pulled up 24px beneath it, its top hidden there, its
  // bottom corners the hero's own radius - so balance and status read as one
  // object, while the status colour stays on white. On Forest, green would
  // merge into the hero and terracotta would turn harsh. The hero sits above
  // it (z-10 in Home). The ring is inset so the edge lands on the
  // hero's own width: an outer ring sat 1px past it on each side.
  const shape = tucked ? '-mt-6 rounded-b-3xl pt-9 pb-3 ring-inset' : 'rounded-lg py-3'
  // w-full + text-left: a <button> doesn't inherit <p>'s block-level,
  // full-width, left-aligned defaults.
  const combined = `flex w-full items-center gap-2 bg-card px-4 text-left ring-1 ${shape} ${ring} ${className}`
  const body = (
    <span className="flex items-center gap-2">
      {icon}
      <span className="flex flex-col">
        {children}
        {footnotes.map((line) => (
          <span key={line} className="text-sm text-muted-foreground">
            {line}
          </span>
        ))}
      </span>
    </span>
  )
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
        {body}
        <CaretRight aria-hidden="true" className="size-5 shrink-0 opacity-70" />
      </button>
    )
  }
  return <p className={combined}>{body}</p>
}
