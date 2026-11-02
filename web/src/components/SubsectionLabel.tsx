import type { ReactNode } from 'react'

/**
 * The label of a stretch inside a section - Cadangan's automatic backups, an
 * amplop's "Sumbangan lain", a dues tier's new rate, a payment's periods: small
 * semibold grey, one step under SectionLabel and never mistaken for it, as the
 * public report's own subheadings are. One look for every subheading, so a
 * reader learns it once.
 *
 * `as` is the heading level the outline needs; it never changes the look.
 */
export default function SubsectionLabel({ as: Tag = 'h3', children }: { as?: 'h2' | 'h3'; children: ReactNode }) {
  return <Tag className="text-sm font-semibold text-muted-foreground">{children}</Tag>
}
