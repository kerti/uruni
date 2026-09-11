import { cn } from '@/lib/utils'

/**
 * The segmented control's two class recipes: one bordered track holding
 * mutually exclusive options, the active one filled Forest (the Button's
 * `default` variant), the rest `ghost` in muted text - the shape itself says
 * "exactly one of these", which a row of separate outline buttons did not.
 * Riwayat's tab strip (M6.23) set the look; the two-option list filters and
 * both in/out direction choices follow it.
 *
 * Classes rather than a component on purpose: Riwayat's options are
 * `NavLink`s (tabs are routes, History.tsx), the rest are `Button`s with
 * their own `aria-pressed` and click handlers, and a wrapper that fit both
 * would be more code than the two strings it shares.
 */

// Written out in full, never built from a number: Tailwind only generates
// class names it can find literally in source.
const columnClass = { 2: 'grid-cols-2', 4: 'grid-cols-4' } as const

/** The track - a grid, so every option gets an equal share of the row. */
export function segmentedTrackClass(columns: keyof typeof columnClass, className?: string): string {
  return cn('grid gap-1 rounded-xl border border-border bg-muted p-1', columnClass[columns], className)
}

/** One option inside the track. Pair it with `variant={active ? 'default' :
 * 'ghost'}`. h-11 keeps the 44px touch target inside the track's padding. */
export function segmentedItemClass(active: boolean, className?: string): string {
  return cn('h-11 w-full min-w-0 text-sm', !active && 'text-muted-foreground', className)
}
