import type { ReactNode } from 'react'

/** One fact in a record's detail dialog (Rincian transaksi, Rincian
 * talangan): the label above, the value under it across the full width - the
 * detail's reading layout, where a confirm dialog uses RecordSummary's compact
 * label-left lines. `wrap-anywhere` breaks a string with no spaces (a pasted
 * link) inside the dialog rather than past its edge. Sits inside a `<dl>`. */
export default function DetailField({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="wrap-anywhere">{children}</dd>
    </div>
  )
}
