import { ArrowLeft } from 'lucide-react'

import { Button } from '@/components/ui/button'

/**
 * A sub-screen's one way back (#745): a quiet link at the top left, above the
 * screen's title, naming where it leads. Every screen reached from another
 * screen that has no save of its own (Status iuran, an amplop, a kategori,
 * the closed amplop list) carries exactly this, and nothing at the bottom. A
 * full-screen form keeps its Kembali beside its save button instead (#742):
 * the way out of a form sits beside the decision, as in a dialog.
 */
export default function BackLink({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button type="button" variant="link" className="h-auto min-h-11 self-start p-0 text-muted-foreground" onClick={onClick}>
      <ArrowLeft aria-hidden="true" />
      {label}
    </Button>
  )
}
