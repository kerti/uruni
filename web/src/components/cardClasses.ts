/**
 * The white card every list row and summary box sits on, written once. It
 * was ~25 hand-copied strings that had drifted: some tappable rows answered a
 * tap and some did not, two showed a focus ring and the rest none, and a
 * few cards carried their own corner or padding.
 *
 * Classes rather than a component, like `segmented.ts`: the card is a `li`,
 * a `button`, a `div`, a `dl` or a `details` depending on the screen, and a
 * wrapper that fit all of them would be more code than the strings.
 * Callers add layout (flex, gap) and pass through `cn` when they need to
 * override.
 */

/** The surface alone - for a card whose rows carry their own padding (a
 * divided list, a panel with a header band). */
export const cardSurface = 'rounded-lg bg-card shadow-card ring-1 ring-foreground/10'

/** A card that holds content: the surface plus the one inner padding. */
export const card = `${cardSurface} px-4 py-3`

/** A card that is itself the control: full width, 44px at the least, and the
 * same answer to hover, press and keyboard focus on every screen. */
export const tappableCard = `${card} min-h-11 w-full text-left select-none outline-none transition-colors hover:bg-muted/40 active:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50`

/** A grey well inside a card or a dialog - a sub-panel that belongs to what
 * holds it (a cek kas line's fix, a member's payment history, the roll an
 * amplop left, a chosen receipt). One fill and one padding; a panel framing a
 * photo passes a tighter padding through `cn`. */
export const insetPanel = 'rounded-lg bg-muted p-3'
