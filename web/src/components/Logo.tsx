import { cn } from '@/lib/utils'

/**
 * The stacked lockup (docs/brand/uruni-lockup-stacked.svg): the U-vessel
 * mark over the lowercase wordmark. Drawn inline rather than loaded as an
 * image so it takes the theme's own tokens - Forest vessel, Sage dot, ink
 * wordmark - and the wordmark is live text in the app's own Plus Jakarta
 * Sans, as the brand asset specifies. Design-System.md's usage rules apply:
 * no shadow, gradient or recolour, and clear space of at least the dot's
 * diameter all round.
 *
 * Decorative next to a heading that already names the app, so it is hidden
 * from screen readers rather than announcing "uruni" twice.
 */
export default function Logo({ className }: { className?: string }) {
  return (
    <div aria-hidden="true" className={cn('flex flex-col items-center gap-1', className)}>
      <svg viewBox="16 13 32 32" className="size-14">
        <path
          d="M21 18 L21 31 A11 11 0 0 0 43 31 L43 18"
          fill="none"
          stroke="var(--primary)"
          strokeWidth="6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <circle cx="32" cy="30" r="5" fill="var(--accent)" />
      </svg>
      <span className="text-2xl leading-none font-bold tracking-[-0.03em] text-foreground">uruni</span>
    </div>
  )
}
