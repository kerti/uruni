import { cn } from '@/lib/utils'

/**
 * The segmented control's two class recipes: one bordered track holding
 * mutually exclusive options, the active one filled Forest (the Button's
 * `default` variant), the rest `ghost` in muted text - the shape itself says
 * "exactly one of these", which a row of separate outline buttons did not.
 * Riwayat's tab strip (M6.23) set the look; the two-option list filters and
 * the record form's kind choices follow it - five of them since #383 added
 * Peruntukan beside Keluar, Masuk, Lokasi and Iuran.
 *
 * Classes rather than a component on purpose: Riwayat's options are
 * `NavLink`s (tabs are routes, History.tsx), the rest are `Button`s with
 * their own `aria-pressed` and click handlers, and a wrapper that fit both
 * would be more code than the two strings it shares.
 */

// Written out in full, never built from a number: Tailwind only generates
// class names it can find literally in source.
const columnClass = { 2: 'grid-cols-2', 3: 'grid-cols-3', 4: 'grid-cols-4', 5: 'grid-cols-5' } as const

/** The track - a grid, so every option gets an equal share of the row.
 *
 * Flush since #319: no inner padding and no gaps, so the options take every
 * pixel of the row and the active fill meets the border. overflow-hidden
 * backs up the outer options' own corner radii (flushItem, below). */
export function segmentedTrackClass(columns: keyof typeof columnClass, className?: string): string {
  return cn('grid overflow-hidden rounded-xl border border-border bg-muted shadow-well', columnClass[columns], className)
}

/** What every option shares inside a flush track: square inner corners, and
 * the two outer options rounded to the track's inner radius - radius-xl less
 * its 1px border - so the active fill follows the border's curve instead of
 * poking a square corner into it. Explicit radii rather than trusting the
 * track's overflow-hidden to clip: WebKit does not clip a child that has its
 * own compositing layer (Button's transition and press transform), which is
 * exactly what showed on an iPhone (#319). The focus ring is drawn inside,
 * since the track would clip one drawn outside.
 *
 * border-0 first: Button's base carries a transparent 1px border, which left
 * the active fill a pixel short of the track's edge and painted over any
 * divider. The hairline between options is the item's own left border
 * instead - without the old gaps, two unselected neighbours would otherwise
 * read as one. */
const flushItem =
  'rounded-none border-0 not-first:border-l not-first:border-l-border first:rounded-l-[calc(var(--radius-xl)-1px)] last:rounded-r-[calc(var(--radius-xl)-1px)] focus-visible:ring-inset'

/** The active option's lift (#356): it rises out of the track's well with a
 * light sheen, a top highlight and a bottom inner shade. Important, because
 * Button's own shadow-button sits on the same element and the order two
 * custom shadow utilities land in the stylesheet is not something to rely
 * on. */
const raisedItem = 'bg-[linear-gradient(180deg,rgb(255_255_255/0.14),transparent_60%)] shadow-segment!'

/** One option inside the track. Pair it with `variant={active ? 'default' :
 * 'ghost'}`. h-11 is the 44px touch target. */
export function segmentedItemClass(active: boolean, className?: string): string {
  return cn('h-11 w-full min-w-0 text-sm', flushItem, active ? raisedItem : 'text-muted-foreground', className)
}

/**
 * A stacked option: the icon above the caption rather than beside it, which
 * is what the footer nav already does with five slots at 375px.
 *
 * Five options across a phone leave each about 75px, which is why the
 * captions are one short word (Keluar, Masuk, Lokasi, Peruntukan, Iuran) and
 * why the icon is stacked above the caption instead of beside it: an icon
 * plus a gap plus a word does not fit on one line at that width, and
 * stacking gives the word the whole column. The icon carries the verb - the
 * arrows say out, in, and moved - so the caption does not need to repeat
 * "Uang" or "Pindah". The full name ("Uang keluar", "Pindah lokasi") lives in
 * each option's aria-label, so a screen reader, and anything that finds the
 * button by its accessible name, still meets the same words every other
 * surface uses; a shortened caption is a visible abbreviation, not a second
 * name for the concept (CONTEXT.md).
 *
 * min-h-11 rather than h-11: two rows are taller than 44px, and the point of
 * the original was a floor, not a fixed height. h-auto overrides
 * Button's default h-8, which min-h-11 had been pinning every option to
 * exactly 44px whatever its padding said - so py-2.5 (#319) is the first
 * padding that actually shows: an icon and a caption no longer sit tight
 * against the track's edges now that it has no padding of its own.
 */
export function segmentedStackedItemClass(active: boolean, className?: string): string {
  return cn(
    'flex h-auto min-h-11 w-full min-w-0 flex-col items-center justify-center gap-0.5 px-1 py-2.5 text-xs leading-tight [&_svg]:shrink-0',
    flushItem,
    active ? raisedItem : 'text-muted-foreground',
    className,
  )
}
