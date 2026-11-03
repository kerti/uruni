import type { ReactNode } from 'react'

import { Button } from '@/components/ui/button'

/**
 * The label that opens every section, on every screen - Beranda's balances
 * and activity, an amplop's, each of Pengaturan's settings, a dues tier's,
 * Anggota's roster, Riwayat's dues payments: led by the Sage dot of the logo's
 * vessel, in Forest, as the landing page opens its sections. One look for every
 * section, so a reader learns it once. A stretch inside a section takes
 * SubsectionLabel; a list's month divider and a disclosure's label are neither.
 *
 * `action` is the one link a section may carry at the label's right end
 * ("Lihat semua"), built here so it looks the same wherever it appears. It
 * gets its 44px from `touch-target` rather than its own height, so the label
 * row stays as tall as the label (#319) instead of min-h-11 pushing it 12px
 * further from what is above.
 */
export default function SectionLabel({ children, action }: { children: ReactNode; action?: { label: string; onClick: () => void } }) {
  const label = (
    <h2 className="flex items-center gap-2 text-base font-semibold text-primary before:size-2 before:shrink-0 before:rounded-full before:bg-accent before:content-['']">
      {children}
    </h2>
  )
  if (!action) return label
  return (
    <div className="flex items-center justify-between gap-3">
      {label}
      <Button type="button" variant="link" className="touch-target h-auto p-0" onClick={action.onClick}>
        {action.label}
      </Button>
    </div>
  )
}
