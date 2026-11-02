import type { ReactNode } from 'react'

/**
 * The label that opens a section of a screen made of sections - Beranda's
 * balances and activity, an amplop's participation and activity, each of
 * Pengaturan's settings and a dues tier's name and rates: led by the Sage dot
 * of the logo's vessel, in Forest, as the landing page opens its sections. Not
 * for a list's month dividers, a subheading inside a section, a field group
 * inside a form, or a disclosure's label, which are not a section's opening,
 * nor for a screen's lone section, where the dot would mark nothing.
 */
export default function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <h2 className="flex items-center gap-2 text-base font-semibold text-primary before:size-2 before:shrink-0 before:rounded-full before:bg-accent before:content-['']">
      {children}
    </h2>
  )
}
