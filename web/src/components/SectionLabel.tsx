import type { ReactNode } from 'react'

/**
 * The label that opens every section, on every screen - Beranda's balances
 * and activity, an amplop's, each of Pengaturan's settings, a dues tier's,
 * Anggota's roster, Riwayat's dues payments: led by the Sage dot of the logo's
 * vessel, in Forest, as the landing page opens its sections. One look for every
 * section, so a reader learns it once. A stretch inside a section takes
 * SubsectionLabel; a list's month divider and a disclosure's label are neither.
 */
export default function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <h2 className="flex items-center gap-2 text-base font-semibold text-primary before:size-2 before:shrink-0 before:rounded-full before:bg-accent before:content-['']">
      {children}
    </h2>
  )
}
