import type { ReactNode } from 'react'

/**
 * The label that opens a section of a reading screen - Beranda's balances and
 * activity, an amplop's participation and activity: led by the Sage dot of the
 * logo's vessel, in Forest, as the landing page opens its sections. Not for a
 * list's month dividers, a subheading inside a section, or a disclosure's
 * label, which are not a section's opening.
 */
export default function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <h2 className="flex items-center gap-2 text-sm font-semibold text-primary before:size-2 before:shrink-0 before:rounded-full before:bg-accent before:content-['']">
      {children}
    </h2>
  )
}
